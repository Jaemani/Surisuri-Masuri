---
id: ADR-0024
title: Immutable cleanup dry-run target from sealed classification evidence
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0024: 봉인된 분류 근거를 불변 cleanup dry-run target으로 고정한다

## 맥락

[ADR-0023](./ADR-0023-fenced-cleanup-lease-claim.md)은 quiet-period 뒤 `cleanup_pending` receipt를 한 cleanup owner가 소유하도록 만들었다. 그러나 `CleanupLeaseGrant`는 control-plane 소유권일 뿐 Cloud Storage inventory read, target 생성 또는 delete 권한이 아니다.

다음 단계에서 lease를 곧바로 삭제 권한으로 사용하면 세 경계가 사라진다.

- Forward recovery는 `reserved`와 current consent 관계를 전진시키지만, reservation-expiry cleanup은 `cleanup_pending`의 partial artifact를 조사한다.
- Storage 분류와 Firestore target 생성 사이에 lease takeover나 receipt revision 변경이 일어날 수 있다.
- 분류 결과가 공개 DTO라면 genuine request binding을 보존한 채 classification, inventory 또는 generation을 바꿔 미래 삭제 후보를 오염시킬 수 있다.

R8b의 목적은 삭제가 아니라, read-only 분류가 실제로 관측한 exact generation과 bounded evidence를 한 cleanup attempt에 create-once로 고정하는 것이다.

## 결정 기준

- 최소권한: cleanup lease, artifact read, target create와 future delete 권한을 서로 교환하지 않는다.
- 근거 무결성: request뿐 아니라 분류 결과 전체를 classifier-produced seal에 묶는다.
- 경쟁 안전성: 같은 attempt의 concurrent create와 응답 유실 replay는 target 하나로 수렴한다.
- 불변성: Target 생성 뒤 path, generation, hash, classification 또는 inventory를 최신 값으로 교체하지 않는다.
- Fail-closed: Stale revision/fence, malformed attempt, ambiguous inventory와 conflicting target은 write 0이다.
- 범위 제한: GCS delete, receipt `expired`, attempt completion, lease renewal/release와 runtime activation은 별도 gate다.

## 검토한 선택지

### 선택지 A: Cleanup lease로 Storage read와 delete를 직접 허용

- 장점: 단계와 capability 수가 적다.
- 단점: Control-plane ownership이 provider mutation 권한으로 과도하게 확장되고, 관측 근거를 delete 전에 고정할 수 없다.
- 판단: 최소권한과 감사 가능성을 깨므로 제외한다.

### 선택지 B: Forward recovery purpose와 planner를 그대로 사용

- 장점: 기존 classifier·authorizer 코드를 변경하지 않는다.
- 단점: `reserved + sweeper fence + current consent` 의미를 `cleanup_pending + cleanup fence`에 잘못 적용하고 forward action을 허용할 위험이 있다.
- 판단: 분류 알고리즘은 재사용하되 purpose, issuer, fence와 후속 disposition은 분리한다.

### 선택지 C: Cleanup 전용 read capability와 sealed evidence, create-once dry-run target

- 장점: Read와 target creation이 각각 fresh current-state 검증을 수행하며 실제 관측한 generation만 고정한다. Delete port 없이도 경쟁·replay 계약을 검증할 수 있다.
- 단점: 별도 capability, canonical evidence seal, target schema와 Firestore transaction이 필요하다.
- 판단: R8b 범위를 지키면서 future delete의 입력을 안전하게 준비하므로 채택한다.

## 결정

### 1. `cleanup_dry_run`은 별도 artifact read purpose다

`ArtifactReadCleanupDryRun`은 다음 shape만 허용한다.

- Receipt state: `cleanup_pending`
- Accepted lineage: 없음
- Forward fence: 없음
- Cleanup fence: exact owner, token, expiry
- Opaque issuer: cleanup dry-run 전용

`SystemCleanupAuthorizer`는 linked indexes와 authoritative receipt, exact `started` cleanup attempt를 같은 read-only Firestore transaction에서 읽는다. 현재 receipt revision, mode, origin, policy, transition·quiescence, cleanup owner와 fence가 `CleanupLeaseGrant`와 정확히 같고 quiet boundary 뒤이면서 lease expiry 전일 때만 최대 30초 read grant를 발급한다.

Current consent는 reservation-expiry artifact 조사 권한의 전제가 아니다. 동의 철회 때문에 이미 만들어진 partial artifact의 삭제 검증이 막히지 않도록 하되, read capability는 exact cleanup receipt와 fence에만 제한한다.

### 2. 분류 알고리즘은 공유하지만 결과 전체를 봉인한다

Cleanup purpose는 기존 manifest-first, exact-generation, complete inventory classifier 알고리즘을 사용한다. Forward action planner는 호출하지 않는다.

Classifier terminal 결과는 다음 전체를 unexported evidence seal에 canonical binding한다.

```text
exact request binding
classification + reason
retention phase
manifest/raw inventory summary
manifest/raw pinned sha256, crc32c, size, generation, metageneration
validator version
observed_at
```

Package 외부 caller가 genuine result를 복사해 shape-valid SHA, generation, classification, inventory 또는 time을 바꾸면 seal validation이 실패한다. Cleanup target authorizer와 기존 forward planner가 같은 evidence validator를 사용한다.

### 3. Classification은 bounded target disposition으로만 변환한다

| Classification | Target decision | Target status | 의미 |
| --- | --- | --- | --- |
| `none/no_candidates` | `verified_empty` | `planned` | 두 inventory가 complete/0인 관측 사실; `expired` 권한 아님 |
| `valid_raw_only` | `delete_candidate` | `planned` | Exact raw lineage만 고정 |
| `valid_complete` | `delete_candidate` | `planned` | Exact raw·manifest lineage 고정 |
| `manifest_only` | `delete_candidate` | `planned` | Exact manifest lineage만 고정 |
| content·metadata conflict, generation drift | `hold` | `hold` | 관측 근거만 보존, 자동 delete 금지 |
| `unavailable` | Target 없음 | 해당 없음 | Immutable transient result로 굳히지 않고 takeover/retry에 남김 |

`stored_missing`, unknown tuple와 classification/pin cardinality가 맞지 않는 결과는 fail-closed한다. `reservation_expiry`는 보통 artifact retention expiry 전이므로 retention phase를 무조건 `at_or_after_expiry`로 강제하지 않고 관측 시각과 `artifact_expires_at`으로 계산한 사실을 고정한다.

### 4. Cleanup target ID는 exact attempt ID다

R8b의 document path는 다음과 같다.

```text
/tenants/{tenantId}/ingestCleanupTargets/{cleanupAttemptId}
```

Cleanup attempt ID는 UUID이며 fence owner와 같다. 따라서 한 claim당 target 하나가 deterministic path로 수렴한다. Target은 다음 provenance를 포함한다.

- Receipt, reservation, attempt, mode와 origin
- Receipt revision과 fencing token
- Transition, quiescence와 lease timestamps
- Classification, reason, retention phase와 inventory summaries
- 실제 pin이 있는 exact raw/manifest path·SHA-256·CRC32C·size·generation·metageneration
- Worker·validator version, classified/created time와 canonical target hash

좌표, raw body, decompressed sample, Firebase UID, App ID, token·credential과 provider 원문 오류는 저장하지 않는다. Collection은 client read/write를 명시적으로 거절한다.

### 5. Target create transaction은 current receipt와 attempt를 다시 검증한다

Target creation capability는 exact request binding, full command hash, receipt revision, cleanup fence와 짧은 expiry를 봉인한다. Firestore transaction은 write 전에 다음을 모두 읽는다.

1. 두 uniqueness index와 authoritative receipt의 3-way linkage
2. Exact `started` cleanup attempt
3. Deterministic path의 existing target

Receipt와 attempt의 revision, owner, token, expiry, worker version, started time과 terminal residue를 다시 검사한다. Target이 없으면 `Create` 한 번만 수행하고 receipt, index와 attempt는 수정하지 않는다. Existing target의 schema, canonical hash와 전체 semantic content가 같으면 `replayed`로 write 0 성공한다. 다르면 `ErrCleanupTargetConflict`, 전체 write 0이다.

Concurrent create는 Firestore transaction retry 뒤 정확히 target 하나, `created` 하나와 `replayed` 하나로 수렴한다.

### 6. R8b target은 삭제 권한이 아니다

Target에 `planned`와 exact lineage가 있어도 다음 권한은 없다.

- GCS object 또는 manifest delete
- Target status를 `raw_deleted|manifest_deleted|completed`로 전환
- Cleanup attempt completed/failed 기록
- Lease renewal/release
- Receipt `expired`와 purge eligibility 설정
- Scheduler, startup, readiness 또는 production deployment

R8c executor는 persisted target만 신뢰하지 않고 current receipt/fence와 provider의 exact generation을 다시 검증해야 한다. Raw exact generation을 먼저 처리하고 post-delete version-aware inventory를 확인하는 계약은 다음 ADR에서 정의한다.

## 결과와 위험

- Cleanup lease가 artifact read나 target create 권한으로 자동 승격되지 않는다.
- Request와 result 전체가 봉인돼 valid-looking generation 치환도 target으로 만들 수 없다.
- Target은 response-loss replay와 concurrent creator에서 create-once로 수렴한다.
- Hold와 unavailable을 delete candidate로 낮추지 않는다.
- Target에는 server-only object path와 digest가 있으므로 logging·사람 리포트에는 원문을 복사하지 않는다.
- 실제 delete E2E, staging IAM·lifecycle·retention과 cleanup completion은 아직 검증하지 않았다.

## 연결 문서

- 선행 결정: [ADR-0023](./ADR-0023-fenced-cleanup-lease-claim.md), [ADR-0018](./ADR-0018-generation-pinned-read-only-classifier.md)
- 증거: [EVD-20260722-032](../evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target)
- 사람 대상 리포트: [HR-20260722-23](../reports/human/HR-20260722-23-immutable-cleanup-dry-run-target.md)
- 제품 업데이트: 해당 없음 — executable·사용자·운영 경로와 GCS delete 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
