---
id: ADR-0016
title: Immutable telemetry object와 manifest의 검증 가능한 계보
status: accepted
decided_at: 2026-07-21
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0016: Immutable telemetry object와 manifest의 검증 가능한 계보

## 맥락

[ADR-0015](./ADR-0015-atomic-telemetry-admission.md)는 현재 권한 확인, 두 uniqueness index와 최초 receipt 생성을 하나의 Firestore transaction으로 묶었다. transaction commit 뒤에는 deterministic gzip 원본을 Cloud Storage에 기록하고 별도 transaction으로 receipt를 `stored`로 바꿔야 한다. Firestore와 Cloud Storage는 하나의 분산 transaction을 제공하지 않으므로 다음 정상적인 부분 실패가 남는다.

- receipt는 `reserved`인데 raw object write의 성공 여부를 호출자가 모른다.
- raw object는 존재하지만 manifest 작성 전 요청이 중단된다.
- raw object와 manifest는 존재하지만 receipt finalizer가 실패한다.
- 같은 pending receipt를 복구하는 둘 이상의 호출자가 같은 경로에 동시에 쓴다.
- 기존 경로의 bytes나 metadata가 현재 receipt 계보와 다르다.
- lifecycle로 object만 먼저 삭제되거나 운영 변경으로 object와 manifest의 만료가 어긋난다.

기존 `ObjectStore.PutIfAbsent(path, bytes, bodyHash)`는 overwrite 차단 여부만 표현하고 실제 저장된 generation, exact compressed-byte hash, CRC32C, 크기와 manifest 계보를 반환하지 않는다. receipt에는 object path와 sample count만 남으므로 이후 worker, 삭제 workflow와 감사 과정이 같은 immutable bytes를 읽었다고 증명할 수 없다.

## 결정 기준

- 데이터 진실성·개인정보: raw object, manifest와 receipt가 동일한 tenant·batch·consent 계보를 가리키고 path와 metadata에 직접 식별정보를 넣지 않을 것
- 실패 복구: 어느 단계에서 중단돼도 같은 입력의 재시도가 기존 artifact를 검증한 뒤 전진할 수 있을 것
- 동시성: overwrite와 last-writer-wins를 허용하지 않고 한 경로가 한 immutable byte sequence에만 대응할 것
- 검증 가능성: SHA-256, CRC32C, size와 generation으로 실제 저장 bytes를 재현·검사할 수 있을 것
- 운영 비용: sample별 Firestore write 없이 raw object 하나, manifest 하나, 작은 receipt 하나로 유지할 것
- 포트폴리오 증거: partial failure, collision, generation-pinned read와 replay를 재현 가능한 테스트로 남길 것

## 검토한 선택지

### 선택지 A: raw object만 저장하고 Firestore receipt를 manifest로 사용

- 장점: Storage write가 한 번이고 구현이 가장 단순하다.
- 단점: Storage lifecycle·worker·export가 Firestore 가용성과 현재 projection에 결합된다. receipt 변경 이력과 immutable object metadata를 분리할 수 없고 Storage inventory만으로 dataset lineage를 재구성하기 어렵다.
- 판단: 원본 위치의 삭제·분석 계보를 장기간 검증하기에 부족하다.

### 선택지 B: raw object 내부에 envelope와 samples를 함께 저장

- 장점: 한 object만 읽어도 metadata와 payload를 얻는다.
- 단점: 기존 wire body를 byte-for-byte 보존하지 못하거나 별도 중첩 format으로 재작성해야 한다. object generation·CRC처럼 write 뒤에 정해지는 값을 같은 immutable bytes에 넣을 수 없다.
- 판단: 수신 payload 증거와 저장 결과 증거를 분리하는 편이 명확하다.

### 선택지 C: immutable raw object, canonical manifest, Firestore receipt의 3-way lineage

- 장점: raw bytes, 저장 결과, control-plane 상태를 각 책임에 맞게 분리하고 replay 때 세 계층을 교차 검증할 수 있다.
- 단점: 두 Storage write 사이와 Storage/Firestore 사이의 부분 실패, 복구·정리 로직과 lifecycle 정렬이 필요하다.
- 판단: 복잡성은 늘지만 운영 복구와 데이터 provenance를 직접 검증할 수 있어 이 선택지를 채택한다.

## 결정

### 1. Port 계약

ingest core는 provider-neutral port 하나를 사용한다.

```go
type TelemetryArtifactStore interface {
    StoreBatch(context.Context, BatchArtifactWrite) (StoredBatchArtifacts, error)
}
```

`BatchArtifactWrite`에는 검증이 끝난 tenant·device·trip·installation·consent·client batch·server batch 식별자, payload schema, body SHA-256, sample 수와 시간 범위, receipt 생성·만료시각, deterministic gzip bytes, 예상 raw/manifest path가 포함된다. Firebase UID, App ID, 이름, 전화번호, 주소, QR public code는 포함하지 않는다.

`StoredBatchArtifacts`는 다음 저장 결과를 반환한다.

```text
raw: path, sha256, crc32c, size, generation, metageneration
manifest: path, sha256, crc32c, size, generation, metageneration
raw_replay, manifest_replay
```

호출자는 path나 generation을 추측하지 않고 반환된 계보 전체를 receipt finalizer에 전달한다. `cloud.google.com/go/storage`는 우연한 transitive dependency가 아니라 adapter의 direct dependency로 고정한다.

### 2. Raw object

- path는 Target Domain Model의 `telemetry/v2/.../{batchId}.json.gz`를 사용한다.
- bytes는 validation에 사용한 HTTP body의 deterministic gzip 결과다.
- `body_hash`는 압축 전 request bytes의 SHA-256이고 `object_sha256`은 실제 gzip bytes의 SHA-256이다. 둘을 혼용하지 않는다.
- writer에는 `Content-Type: application/json`, `Content-Encoding: gzip`, `Cache-Control: no-store`와 서버 계산 custom metadata를 설정한다.
- CRC32C는 Castagnoli polynomial로 서버가 계산하고 writer의 CRC 검사를 활성화한다.
- write는 반드시 `bucket.Object(path).If(storage.Conditions{DoesNotExist: true})`로 실행한다. `GenerationMatch: 0`을 직접 구성하지 않는다.
- 성공 후 writer attrs의 path, generation, metageneration, size, CRC32C를 예상값과 대조한다.

### 3. Canonical manifest

raw object가 검증된 뒤 manifest bytes를 생성한다. manifest는 versioned Go struct를 `encoding/json`으로 한 번 marshal한 compact UTF-8 JSON이다. map iteration, 들여쓰기, 현재시각 재호출과 client 제공 metadata를 사용하지 않는다.

manifest v1은 최소한 다음을 포함한다.

```text
manifest_version, payload_schema_version,
tenant_id, device_id, trip_id, installation_id,
batch_id, client_batch_id, consent_revision_id,
body_hash, object_sha256, object_crc32c, object_size,
object_path, object_generation, object_metageneration,
compression, content_type, sample_count,
first_captured_at, last_captured_at,
received_at, expires_at, validator_version
```

- manifest path는 `telemetry-manifests/v2/.../{batchId}.manifest.json`이다.
- manifest 자체도 `DoesNotExist` precondition, SHA-256, CRC32C, size, generation과 metageneration을 가진 immutable artifact다.
- manifest는 raw object generation을 반드시 포함하며 같은 path의 최신 generation을 암묵적으로 가리키지 않는다.

### 4. Collision과 replay 검증

새 write가 HTTP `412 Precondition Failed` 또는 gRPC `FailedPrecondition`일 때만 immutable collision 후보로 분류한다. timeout, permission, quota, cancellation과 알 수 없는 provider 오류를 기존 object 존재로 추정하지 않는다.

collision 후보는 조건이 없는 새 object handle로 attrs를 읽고 그 순간의 exact generation을 고정한다. 이후 `Generation(generation)` handle과 `ReadCompressed(true)`를 사용해 Storage가 gzip을 자동 해제하지 않은 실제 저장 bytes를 읽는다. 다음이 모두 일치할 때만 replay success다.

- expected path, generation과 attrs의 object identity
- exact byte size
- SHA-256
- CRC32C
- raw이면 deterministic gzip bytes 전체
- manifest이면 canonical manifest bytes 전체
- 필수 content type, content encoding과 custom metadata

hash나 metadata 하나만 같다고 기존 artifact를 신뢰하지 않는다. 불일치는 `artifact_conflict`, object 부재·generation 변경·malformed attrs와 검증 불능 상태는 fail-closed `artifact_unavailable`로 처리한다.

### 5. 부분 실패와 receipt finalizer

처리 순서는 다음과 같다.

```text
reserved receipt
  -> raw DoesNotExist write 또는 exact replay 검증
  -> canonical manifest DoesNotExist write 또는 exact replay 검증
  -> receipt MarkStored(두 artifact 전체 계보)
```

- raw 성공 후 manifest 실패: receipt는 `reserved`로 유지한다. retry가 raw를 exact replay로 검증하고 manifest 단계부터 전진한다.
- manifest 성공 후 finalizer 실패: retry가 raw와 manifest를 모두 exact replay로 검증한 뒤 같은 finalizer를 재호출한다.
- finalizer는 두 uniqueness index와 receipt의 기존 3-way linkage 외에 raw/manifest path·hash·CRC·size·generation·metageneration, body hash, sample count를 다시 검증한다.
- 이미 `stored`인 receipt에 동일한 전체 계보를 전달하면 idempotent success다. 필드 하나라도 다르면 unavailable로 닫는다.
- raw collision이 다른 bytes이면 현재 허용된 terminal `object_conflict`로 reject할 수 있다. manifest 충돌이나 기존 artifact 손상은 자동 reject·overwrite·delete하지 않고 reserved 상태와 복구 대상에 남긴다.
- 만료된 reserved receipt는 새 저장이나 finalizer를 허용하지 않는다. orphan 여부는 generation-pinned reconciliation에서 확인한다.

### 6. 보존·삭제와 운영 경계

- raw와 manifest에는 같은 `expires_at` 계보를 보존하며 Storage lifecycle은 두 prefix가 같은 정책 의도를 갖도록 관리한다.
- lifecycle 삭제 시점의 편차를 정상으로 간주하되, manifest만 존재하거나 receipt가 stored인데 artifact가 누락된 상태를 탐지한다.
- 삭제 workflow는 path만으로 삭제하지 않고 receipt/manifest에 기록된 exact generation을 대상으로 한다.
- bucket versioning, retention policy와 lifecycle 실제 값은 staging 인프라 ADR·배포 증거에서 별도로 확정한다.
- adapter·finalizer만 구현해도 runtime readiness를 열지 않는다. lease/fencing/sweeper, startup wiring, staging IAM/lifecycle과 E2E가 모두 통과해야 한다.

## 결과와 위험

- raw bytes, 저장 metadata와 control-plane 상태의 의미가 분리되고 worker·삭제·ML dataset이 exact generation을 참조할 수 있다.
- 재시도는 overwrite가 아니라 동일 bytes의 검증 가능한 replay가 된다.
- Storage read-after-collision 때문에 replay 비용이 늘지만 정상 신규 수신은 object 2회 write와 receipt finalizer만 사용한다.
- raw 성공 후 manifest가 장기간 실패하면 orphan raw object가 남는다. lease owner, fencing token, reserved receipt sweeper와 generation-pinned orphan cleanup은 다음 차단 게이트다.
- `DoesNotExist`와 generation은 bucket 내부 overwrite만 막으며 잘못된 IAM, lifecycle, retention 설정을 증명하지 않는다.
- canonical JSON은 Go struct와 manifest version에 결합된다. field 의미나 encoding을 바꿀 때 manifest version과 fixture를 함께 올린다.
- rollback은 기존 artifact를 덮어쓰는 방식이 아니다. 새 adapter 배포를 중지하고 reserved receipt를 보존한 채 검증된 이전 reader/reconciler로 복구한다.

## 후속 검증

- provider-neutral canonical bytes/hash/CRC 단위·property test
- fake backend에서 신규, raw collision replay/conflict, raw 성공 후 manifest 실패, manifest 성공 후 finalizer 실패 검증
- GCS emulator 또는 staging bucket에서 `DoesNotExist`, exact generation read, compressed bytes와 metadata 검증
- Firestore finalizer의 전체 artifact lineage와 손상 fixture 검증
- lease/fencing/sweeper 및 receipt expiry 전후 orphan reconciliation
- staging IAM 최소권한, lifecycle·retention drift와 generation-pinned 삭제 drill

## 연결 문서

- 선행 결정: [ADR-0015](./ADR-0015-atomic-telemetry-admission.md)
- 데이터 계약: [Target Domain Model](../data/TARGET_DOMAIN_MODEL.md)
- 위험: [RSK-06, RSK-10](../plans/RISK_REGISTER.md)
- 제품 업데이트: 해당 없음 — runtime·사용자·운영 경로에 아직 연결하지 않음
- 증거: 구현·검증 후 EVD를 연결하며 현재 문서는 결정 증거만 제공
- 인시던트: 해당 없음 — production·field 배포와 사용자 영향 없음
