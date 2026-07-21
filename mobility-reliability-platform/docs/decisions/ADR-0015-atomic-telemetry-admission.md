# ADR-0015: Firestore 권한 검증과 3-way receipt reservation의 원자 경계

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0009](./ADR-0009-fail-closed-ingest-kernel.md), [ADR-0010](./ADR-0010-authenticated-telemetry-references.md), [ADR-0012](./ADR-0012-firebase-dual-token-verifier-policy.md), [ADR-0013](./ADR-0013-telemetry-authorization-snapshot.md)

## 맥락

[ADR-0013](./ADR-0013-telemetry-authorization-snapshot.md)의 read-only authorizer는 active tenant·membership·installation·trip·기기 배정·현재 동의를 exact read로 검사한다. 그러나 권한 snapshot을 읽은 뒤 별도 receipt store가 reservation을 만들면 그 사이 membership, installation 또는 동의가 철회될 수 있다. 이 TOCTOU 구간에서는 더 이상 권한이 없는 batch가 receipt와 Storage object를 만들 수 있다.

또한 서버 파생 idempotency key, tenant 범위 client batch key, server batch receipt를 독립적으로 만들면 동시 요청이나 부분 실패 후 일부 index만 남을 수 있다. Firestore transaction callback은 충돌 시 재실행될 수 있으므로 callback 안에서 batch ID, 시각, Storage object 또는 로그 같은 외부 side effect를 만들면 한 논리 요청이 서로 다른 계보를 가지거나 object를 중복 작성할 위험도 있다.

production adapter는 local fake transaction seam과 Firestore Emulator에서 검증한다. Emulator concurrent same-batch에서 한 요청만 3-way create에 성공하고 다른 요청은 같은 receipt의 pending replay로 수렴했으며, current consent 문서가 없을 때 세 admission 문서를 만들지 않았다. 다만 실제 철회 transaction과 admission의 경쟁, ADC/IAM·production transaction, executable wiring과 Cloud Storage adapter는 아직 검증하거나 연결하지 않았다.

## 결정 기준

- 권한 철회와 reservation 사이의 경쟁 조건을 닫을 것
- 같은 logical batch가 하나의 server batch와 receipt 계보에만 연결될 것
- Firestore transaction retry가 외부 side effect를 반복하지 않을 것
- replay와 conflict를 확인하기 전에도 현재 권한을 다시 검사할 것
- partial index와 손상된 linkage를 추측으로 복구하거나 외부에 노출하지 않을 것
- Firestore와 Storage 사이의 비원자적 경계를 복구 가능한 상태로 남길 것
- GPS sample별 Firestore write를 만들지 않을 것

## 검토한 선택지

### 선택지 1: read-only authorization 후 별도 reservation

- 기존 authorizer와 receipt store를 순서대로 호출한다.
- 구현은 단순하지만 authorization read와 reservation create 사이 철회 경쟁 조건이 남는다.
- 두 고유 index와 receipt의 원자 생성도 별도로 해결해야 한다.

### 선택지 2: receipt를 먼저 예약한 뒤 authorization

- 중복 요청은 빨리 판정할 수 있다.
- 권한 없는 요청도 index와 receipt의 존재 여부를 관찰하거나 문서를 만들 수 있어 위치정보 수집의 신뢰 경계에 맞지 않는다.
- authorization 실패 뒤 예약을 되돌리는 보상 작업이 추가된다.

### 선택지 3: authorization과 세 문서 reservation을 한 transaction으로 통합

- 현재 authorization snapshot, 두 고유 index, receipt를 같은 serializable read-write 경계에서 처리한다.
- callback retry마다 권한을 재평가할 수 있고 신규 reservation은 세 문서가 함께 commit된다.
- transaction 비용과 구현 복잡도는 늘지만 production ingest를 열기 위한 원자성 조건을 직접 충족한다.

## 결정

선택지 3을 채택한다. ingest service는 별도 `Authorize`와 `Reserve` 호출을 없애고 `AdmissionStore.AuthorizeAndReserve` 하나를 호출한다.

### 처리 순서

```text
strict request validation
  -> raw body SHA-256, 두 파생 key, proposed server batch ID와 reservation 시각 생성
  -> Firestore read-write transaction
       1. authorization exact reads
       2. provider-neutral pure policy 평가
       3. idempotency index read
       4. client-batch index read
       5. 필요한 경우 receipt read
       6. replay·conflict·손상 상태 판정
       7. 신규이면 index 두 개와 reserved receipt create
  -> transaction commit
  -> Cloud Storage PutIfAbsent
  -> 별도 Firestore transaction으로 MarkStored 또는 terminal MarkRejected
```

- raw body hash, proposed UUIDv7 server batch ID, reservation `created_at`과 `expires_at`은 transaction callback에 들어가기 전에 한 번만 만든다.
- authorization `BatchScope`의 tenant·device·trip·installation·consent revision과 persisted reservation lineage는 transaction 시작 전에 정확히 같아야 하며, 두 derived key도 reservation field에서 다시 계산해 일치해야 한다.
- authorization 평가는 callback retry마다 현재 snapshot과 현재 server time으로 다시 실행한다.
- callback의 모든 read는 첫 create/update보다 먼저 끝나야 한다.
- callback 안에서는 UUID 생성, Storage write, 외부 API, 로그·metric emit 같은 외부 side effect를 실행하지 않는다.
- callback 외부의 result는 매 retry 시작 때 비워 마지막으로 commit된 callback 결과만 반환한다.
- Storage write와 finalizer는 transaction commit 이후에만 실행한다.

### 문서 경로와 key

transaction은 다음 tenant-scoped server-only 문서를 사용한다.

```text
/tenants/{tenantId}/ingestIdempotency/{reservationKey}
/tenants/{tenantId}/ingestClientBatches/{clientBatchKey}
/tenants/{tenantId}/ingestReceipts/{serverBatchId}
```

두 index key는 UTF-8 문자열을 SHA-256으로 계산한 lowercase hex다.

```text
reservationKey =
sha256(schemaVersion + U+001F + tenantId + U+001F
       + installationId + U+001F + clientBatchId)

clientBatchKey =
sha256(tenantId + U+001F + clientBatchId)
```

신규 요청은 두 index와 receipt를 같은 transaction에서 `create`한다. receipt ID와 batch ID는 같아야 하며 index는 둘 다 같은 reservation key, client batch key, receipt ID, batch ID, installation ID, client batch ID, payload schema version, body hash와 만료시각을 가리킨다.

### Authorization 우선순위와 오류

- transaction은 replay·conflict index를 읽기 전에 active tenant·beneficiary membership·installation·trip·assignment·현재 정밀위치 동의를 읽고 [ADR-0013](./ADR-0013-telemetry-authorization-snapshot.md)의 pure policy를 평가한다.
- 철회되거나 관계가 맞지 않는 호출자는 기존 receipt 존재 여부와 관계없이 generic `batch_unauthorized`로 거절한다.
- Firestore 오류, malformed trusted document, partial index, missing receipt, index/receipt linkage mismatch와 알 수 없는 상태는 generic `ingest_unavailable`로 닫는다.
- provider·document path·UID·App ID와 내부 linkage 상세는 HTTP 오류에 포함하지 않는다.

### Replay와 conflict 상태

두 index가 모두 존재하고 lineage가 일치할 때 receipt 상태를 다음처럼 매핑한다.

| receipt 상태 | admission 결과 |
| --- | --- |
| `reserved` | `replay_pending` |
| `stored`, `queued`, `projected`, `deleting`, `deleted` | `replay_complete` |
| `rejected` | `replay_rejected` |
| 알 수 없는 상태 | `ingest_unavailable` |

- 같은 reservation key와 client batch key가 같은 body hash를 가리키면 replay다.
- 같은 reservation key가 다른 body hash로 재사용되면 `idempotency_conflict`다.
- 같은 tenant/client batch가 다른 reservation에 연결되면 `client_batch_conflict`다.
- 현재 reservation index만 남았거나, client-batch index가 가리키는 기존 reservation index·receipt가 없거나, 두 index가 서로 다른 receipt·batch·lineage를 가리키면 conflict로 추측하지 않고 unavailable로 처리한다. 완전한 기존 lineage가 다른 reservation을 가리킬 때만 client-batch conflict다.

### Receipt와 finalizer

첫 receipt는 다음 lineage를 보존하고 `reserved`, `revision=1`로 생성한다.

```text
tenant_id, batch_id, device_id, trip_id, installation_id,
consent_revision_id, client_batch_id, payload_schema_version,
reservation_key, client_batch_key, body_hash,
status, revision, created_at, updated_at, expires_at
```

- 기본 receipt 보존기간은 reservation 생성 후 30일이다.
- `MarkStored`와 `MarkRejected`도 idempotency index·client-batch index·receipt의 3-way linkage와 상태별 field 불변조건을 transaction에서 다시 검사한다.
- Storage 성공 뒤의 `MarkStored`는 reservation 시각을 재사용하지 않고 성공 후 새 server time으로 `updated_at`을 기록하며 revision을 증가시킨다.
- 현재 terminal rejection은 `object_conflict`만 허용한다.
- 이미 같은 값으로 완료된 finalizer retry는 idempotent하게 기존 receipt를 반환한다. 다른 object path·sample count·rejection code 또는 손상된 linkage는 unavailable로 닫는다.

## 결과와 위험

- authorization과 최초 receipt/index 생성 사이 TOCTOU를 같은 Firestore transaction 경계로 줄였다.
- local Firestore Emulator concurrent same-batch에서 한 3-way document set만 남는 직렬화를 검증했다. 이는 production 부하·네트워크·ADC/IAM의 증거가 아니다.
- Firestore commit과 Cloud Storage write는 분산 transaction이 아니다. receipt가 `reserved`인 채 남거나 object가 저장된 뒤 `MarkStored`가 실패할 수 있다.
- pending replay 여러 개가 같은 object write를 시도할 수 있다. Storage `DoesNotExist`는 overwrite를 막지만 lease owner·fencing token과 sweeper 복구가 후속으로 필요하다.
- 만료 직전 `reserved` replay가 admission을 통과한 뒤 Storage 처리 중 만료되면 finalizer는 막히지만 orphan object가 생길 수 있다. object/manifest에 원 receipt 만료시각을 보존하고 lease deadline·fencing·orphan cleanup을 구현하기 전에는 runtime을 열지 않는다.
- 이 결정 작성 시점의 ObjectStore 한계는 [ADR-0016](./ADR-0016-immutable-telemetry-artifact-lineage.md)에서 raw·manifest 전체 계보를 반환하는 artifact store와 finalizer 계약으로 확장했다. runtime wiring 전이라는 운영 한계는 유지된다.
- consent 철회가 transaction commit 뒤에 발생하면 이미 승인·저장된 object를 자동 취소하지 않는다. 이후 수집 차단과 삭제 workflow를 별도로 적용한다.
- transaction read 수와 retry 비용이 늘지만 GPS sample별 Firestore write는 만들지 않는다.
- adapter는 `cmd/server`에 연결하지 않았고 lease·fencing·sweeper도 없으므로 `/healthz` 외 readiness와 ingest는 계속 fail-closed여야 한다.

## Rollout gate와 후속 검증

- actual Firestore Emulator의 신규·concurrent same-batch·missing authorization 검증은 CI gate로 유지하고, 실제 철회 transaction 경쟁과 partial/corrupt fixture를 추가한다.
- staging ADC/IAM과 실제 tenant-scoped document decode를 검증한다.
- Cloud Storage `DoesNotExist`, object SHA-256·generation과 immutable manifest adapter 구현 뒤 staging lifecycle·IAM을 검증한다.
- pending reservation lease, fencing token, sweeper와 orphan reconciliation을 구현한다.
- verifier, admission store, Storage adapter를 executable startup에 함께 연결한 뒤에만 readiness를 연다.
- 위 gate 전에는 production authorization·원자 수신·운영 저장 완료를 주장하지 않는다.

## 연결 문서

- 증거: [EVD-20260721-014](../evidence/2026-07.md#evd-20260721-014--원자적-telemetry-admission과-receipt-lineage)
- Firestore Emulator integration: [EVD-20260721-015](../evidence/2026-07.md#evd-20260721-015--firestore-admission-transaction-emulator-integration)
- 사람 대상 리포트: [HR-20260721-07](../reports/human/HR-20260721-07-atomic-telemetry-admission.md)
- 제품 업데이트: 해당 없음 — runtime과 사용자·운영 경로에 연결하지 않음
- 인시던트: 해당 없음 — production·field 배포와 사용자 영향 없음
