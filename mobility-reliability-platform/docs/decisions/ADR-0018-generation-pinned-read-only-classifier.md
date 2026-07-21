---
id: ADR-0018
title: Generation-pinned read-only telemetry artifact classifier
status: accepted
decided_at: 2026-07-21
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0018: Generation-pinned read-only telemetry artifact classifier

## 맥락

[ADR-0016](./ADR-0016-immutable-telemetry-artifact-lineage.md)은 수신 시점의 immutable raw object·canonical manifest·Firestore receipt 계보를 정했고, [ADR-0017](./ADR-0017-fenced-ingest-recovery.md)은 pending receipt의 lease·fencing·cleanup 상태 전이를 정했다. 다음 recovery 단계는 Storage에 남은 artifact를 읽어 다음 사실을 구분하는 것이다.

- 아무 artifact도 없는가
- raw generation만 유효하게 남았는가
- manifest와 그 manifest가 참조한 raw generation이 함께 유효한가
- manifest만 남았거나 receipt와 다른 계보인가
- object는 있으나 bytes, metadata 또는 generation이 달라졌는가
- accepted receipt가 고정한 exact generation이 사라졌는가
- NotFound가 아니라 provider permission·quota·timeout·응답 손상 때문에 검증할 수 없는가

기존 write adapter의 replay 검증은 collision 뒤 현재 live generation 하나를 exact generation으로 고정하는 데 적합하지만, bucket versioning 아래 0개·1개·복수 generation을 구분하지 않는다. 이를 그대로 recovery classifier로 쓰면 여러 candidate 중 최신을 권위값으로 잘못 선택하거나 provider 오류를 artifact 없음으로 오분류할 수 있다.

또한 `ClaimRecoveryLease`는 control-plane owner를 정할 뿐 위치 artifact를 읽을 권한을 부여하지 않는다. pending receipt의 current tenant·trip·assignment·precise-location consent가 무효인데 claim만 보고 Storage를 조회하면 [ADR-0017](./ADR-0017-fenced-ingest-recovery.md)의 privacy 경계를 우회한다.

## 결정 기준

- 권한: claim과 artifact read permission을 분리하고 current authorization 전 Storage call을 0으로 유지할 것
- 계보: path의 latest가 아니라 유일하게 고정한 exact generation과 read 전후 attrs를 사용할 것
- 정직성: NotFound, conflict, ambiguity와 provider unavailable을 서로 바꾸지 않을 것
- 무변경: R5 classifier는 object create/delete, receipt/index mutation과 recovery attempt completion을 수행하지 않을 것
- 검증성: provider-neutral fake와 pinned official Storage testbench에서 분류·drift·bound를 재현할 것
- 개인정보: result·error·log에 raw body, 좌표, UID, App ID, token과 사람 식별정보를 포함하지 않을 것

## 검토한 선택지

### 선택지 A: write adapter의 replay 검증을 그대로 호출

- 장점: 기존 exact-generation read와 snapshot 비교 코드를 재사용한다.
- 단점: write precondition failure를 출발점으로 삼고 latest live object를 먼저 선택한다. 복수 version과 manifest-first authority를 표현하지 못하며 create 경로와 read-only 경로가 결합된다.
- 판단: 공통 digest·snapshot validator는 재사용하되 public port와 orchestration은 분리한다.

### 선택지 B: receipt path의 현재 object만 읽고 내용으로 판단

- 장점: list operation이 없어 단순하고 비용이 낮다.
- 단점: generation drift와 복수 candidate를 놓치며 lifecycle·versioning 상태에서 최신 object를 자동 채택하게 된다.
- 판단: recovery·삭제 계보에 필요한 권위성을 제공하지 못한다.

### 선택지 C: bounded version inventory 뒤 exact-generation만 읽는 read-only classifier

- 장점: 0·1·복수 candidate, exact NotFound와 read 중 drift를 구분하고 write 없이 분류할 수 있다.
- 단점: version-aware list, strict manifest/raw validator와 더 세분된 오류 계약이 필요하다.
- 판단: fail-closed recovery와 포트폴리오 증거에 필요한 복잡성이므로 채택한다.

## 결정

### 1. Recovery claim은 artifact permission이 아니다

artifact classifier의 orchestration은 다음 순서를 강제한다.

```text
3-way receipt linkage + eligible state
  -> recovery 또는 integrity-audit authorization
  -> current fence/binding 검증
  -> read-only classifier
```

- `forward_recovery`는 `reserved` receipt에만 사용한다. current tenant, installation, trip, assignment와 precise-location consent를 system recovery policy로 다시 읽고 성공해야 한다.
- `accepted_integrity_audit`은 `stored|queued|projected`가 고정한 기존 lineage를 읽는 별도 보존·무결성 권한이다. 동의 철회만으로 accepted receipt를 downgrade하거나 자동 삭제하지 않는다.
- `deleting|deleted`는 승인된 deletion outcome과 target을 함께 읽어야 하므로 R5 대상에서 제외하고 accepted deletion auditor가 담당한다. `rejected`, `recovery_hold`, `cleanup_pending`, `expired`도 R5 public classifier 대상이 아니며 별도 승인·port를 사용한다.
- authorization grant는 purpose, tenant ID, receipt ID, reservation key, receipt revision, consent revision, policy version, checked/expiry time을 exact binding한다. `forward_recovery`는 current lease fence도 묶는다.
- grant는 binding field만 채운 일반 DTO가 아니라 authorizer만 생성할 수 있는 opaque server capability다. forward grant는 system recovery authorizer, accepted audit grant는 integrity-audit authorizer가 발급하며 서로의 purpose로 재사용할 수 없다.
- production grant는 unexported capability field, issuer/policy version과 canonical request-binding hash를 포함한다. trusted authorizer constructor만 mint하고 classifier가 이를 재검증한다. test grant constructor는 `_test.go`에서만 제공한다.
- classifier context deadline은 grant expiry보다 늦을 수 없다. 각 inventory/inspect/read 경계 전에 grant expiry와 request binding을 다시 확인하며, 만료 뒤 새 provider call을 시작하지 않는다.
- grant가 없거나 만료·불일치하면 classifier port와 Storage adapter를 호출하지 않는다. 합성 unit test용 grant도 production constructor와 섞지 않는다.
- system recovery authorizer와 runtime wiring이 구현되기 전 R5 구현은 local/test 전용이며 readiness를 열지 않는다.

### 2. Write port와 read port를 분리한다

write의 `TelemetryArtifactStore.StoreBatch`는 그대로 유지한다. R5는 provider-neutral read port를 새로 둔다.

```go
type ArtifactInventoryReader interface {
    ListExactPathGenerations(ctx context.Context, path string, limit int) (GenerationInventory, error)
    InspectGeneration(ctx context.Context, path string, generation int64) (ArtifactSnapshot, error)
    ReadManifestGeneration(ctx context.Context, target ArtifactTarget, maxBytes int64) ([]byte, error)
    ReadRawGenerationCompressed(ctx context.Context, target ArtifactTarget, maxBytes int64) ([]byte, error)
}
```

- `ListExactPathGenerations`는 exact path만 반환하며 bucket prefix의 다른 object를 섞지 않는다.
- caller는 candidate가 둘 이상인지만 알아도 자동선택을 중지할 수 있도록 `limit=2` 이상을 사용한다. adapter는 `limit+1`이 아니라 계약에 정의된 `truncated`를 반환해 bound 초과를 숨기지 않는다.
- inventory는 version-aware live·noncurrent candidate와 soft-deleted candidate의 존재를 분리한다. GCS는 `Versions:true`와 `SoftDeleted:true`가 별도 query이므로 adapter가 둘을 한 결과로 합치되 candidate 종류를 잃지 않는다.
- `GenerationInventory`는 `non_soft_deleted_candidates`와 `soft_deleted_candidates`, 각 query의 `performed`, `truncated`와 coverage를 반환한다. `non_soft_deleted`는 `Versions:true`로 관찰한 current live와 noncurrent generation을 모두 포함한다. soft delete가 승인된 bucket policy에서 비활성임이 검증됐거나 soft-deleted query가 성공해야만 coverage가 complete다. policy가 불명확하거나 query permission이 없으면 artifact 없음으로 분류하지 않고 unavailable이다.
- manifest와 raw 전용 read method가 compressed-read 동작을 고정한다. generic boolean flag를 public domain port에 노출하지 않는다.
- 모든 read는 positive exact generation을 요구하고 max+1 byte를 관찰해 size limit 초과를 거부한다.
- exact stored gzip bytes는 GCS Go SDK의 HTTP transport에서만 `ReadCompressed(true)` 계약이 있다. production reader factory가 `storage.NewClient` 기반 HTTP client와 bucket handle의 생명주기를 직접 소유하며 arbitrary `BucketHandle`이나 `NewGRPCClient` 결과를 받는 public constructor를 두지 않는다. gRPC transport를 감지하거나 배제할 수 없으면 raw read를 시작하지 않고 unavailable로 닫는다.
- low-level reader는 classifier composition 내부에만 주입한다. HTTP handler, scheduler와 일반 worker가 `ArtifactInventoryReader`를 직접 받아 authorization gate를 우회하지 못하게 startup wiring과 compile-time composition test를 둔다.

### 3. Typed request와 result를 고정한다

classifier service의 public contract는 authorization grant와 request를 별도 값으로 받는다.

```go
type ArtifactClassifier interface {
    Classify(
        context.Context,
        ArtifactReadAuthorizationGrant,
        ArtifactClassificationRequest,
    ) (ArtifactClassificationResult, error)
}
```

service는 grant와 request binding을 먼저 검증하고 성공한 경우에만 내부 `ArtifactInventoryReader`를 호출한다. invalid request·grant·issuer·binding·expiry는 고정된 domain error와 reader call 0, caller context cancel/deadline은 표준 context error로 중단한다. caller context가 살아 있는데 provider가 반환한 timeout·cancel·quota 등 normalize 가능한 실패는 typed `unavailable` result로 반환한다.

`ArtifactClassificationRequest`는 provider 문서를 직접 넣지 않고 receipt에서 만든 immutable expectation만 받는다.

```text
purpose,
receipt_id, reservation_key,
receipt_state, receipt_revision,
tenant_id, device_id, trip_id, installation_id,
batch_id, client_batch_id, consent_revision_id,
payload_schema_version, validator_version,
body_hash, expected_sample_count,
first_captured_at, last_captured_at,
received_at, artifact_expires_at,
expected_raw_path, expected_manifest_path,
accepted_raw_lineage?, accepted_manifest_lineage?
forward_fence(owner_id, token, expires_at)?
```

- `forward_recovery`에는 accepted lineage가 없어야 한다.
- `forward_recovery`에는 request와 grant가 exact match하는 current owner ID·fencing token·lease expiry가 있어야 한다. receipt ID·reservation key·revision·tenant·consent binding도 하나라도 다르면 reader call 0이다.
- `accepted_integrity_audit`에는 receipt가 고정한 raw/manifest path·SHA-256·CRC32C·size·generation·metageneration 전체가 있어야 한다.
- expected path는 immutable input으로 다시 계산한 deterministic path와 같아야 한다.
- `observed_at`은 request field가 아니다. classifier가 trusted server UTC clock과 provider observation으로 한 번 정하고, artifact expiry 전/후 phase를 결과에 표시하는 데만 사용한다.
- forward 분류는 `grant.checked_at <= observed_at < min(grant.expires_at, fence.expires_at)`을 만족해야 한다. 이 관계가 깨졌거나 작업 중 expiry에 도달하면 새 provider call을 시작하지 않고 고정 domain error로 중단한다.

`ArtifactClassificationResult`는 다음 정보만 반환한다.

```text
classification, reason_code, retention_phase,
manifest_inventory(performed, non_soft_deleted_count, soft_deleted_count, truncated, coverage),
raw_inventory(performed, non_soft_deleted_count, soft_deleted_count, truncated, coverage),
pinned_manifest(generation, metageneration, sha256, crc32c, size)?,
pinned_raw(generation, metageneration, sha256, crc32c, size)?,
validator_version, observed_at
```

path는 이미 authorization-bound request에 있으므로 result에 복제하지 않는다. raw·manifest bytes, decoded samples, 좌표, Firebase UID/App ID, authorization token과 사람 식별자는 result·log에 넣지 않는다. pinned digest·generation도 내부 result에만 두고 result 구조체 전체의 structured logging을 금지한다. metrics·일반 log·사람 리포트에는 classification, reason, duration과 비식별 count만 허용한다.

### 4. Classification과 reason을 분리한다

R5 coarse classification은 다음 열 가지다. 기존 계획의 후보 목록에 manifest 자체의 손상과 receipt cross-lineage 위반을 metadata mismatch와 섞지 않기 위해 `manifest_conflict`를 추가한다.

```text
none
valid_raw_only
valid_complete
manifest_only
raw_content_conflict
manifest_conflict
metadata_conflict
generation_drift
stored_missing
unavailable
```

주요 reason code는 다음처럼 고정한다.

| classification | reason 예시 | 의미 |
| --- | --- | --- |
| `none` | `no_candidates` | reserved expected path 두 곳 모두 version-aware NotFound |
| `valid_raw_only` | `raw_valid_manifest_absent` | manifest 0개, raw 유일 generation과 payload가 모두 valid |
| `valid_complete` | `manifest_and_referenced_raw_valid` | 유일 manifest와 그 안의 exact raw generation이 valid |
| `manifest_only` | `referenced_raw_not_found` | 유일 valid manifest가 참조한 exact raw generation만 provider-confirmed NotFound |
| `raw_content_conflict` | `decompressed_body_hash_mismatch`, `payload_lineage_mismatch`, `strict_payload_invalid` | exact raw의 해제된 body·strict identity가 reserved lineage와 다름 |
| `manifest_conflict` | `manifest_malformed`, `manifest_noncanonical`, `manifest_lineage_mismatch` | manifest bytes 또는 raw reference가 receipt expectation과 다름 |
| `metadata_conflict` | `attrs_malformed`, `required_metadata_mismatch`, `content_headers_mismatch` | object bytes 판단 전후의 static attrs가 계약과 다름 |
| `generation_drift` | `multiple_manifest_generations`, `multiple_raw_generations`, `referenced_generation_missing_other_present`, `accepted_generation_missing_other_present`, `soft_deleted_candidate_present`, `generation_changed_during_read`, `metageneration_changed_during_read` | authoritative generation을 자동선택할 수 없음 |
| `stored_missing` | `accepted_manifest_missing`, `accepted_raw_missing`, `accepted_both_missing`, `accepted_generation_soft_deleted` | accepted receipt가 고정한 exact generation 중 하나 이상이 complete inventory에서 없거나 soft-deleted됨 |
| `unavailable` | `permission_denied`, `quota_limited`, `provider_timeout`, `provider_cancelled`, `provider_unavailable`, `validator_unavailable`, `codec_profile_unavailable`, `inventory_coverage_incomplete`, `response_unverifiable` | artifact 존재 여부나 무결성을 판단하지 못함 |

classification은 downstream action이 아니다. 예를 들어 `raw_content_conflict`를 반환해도 R5는 receipt를 reject하지 않고, `stored_missing`이어도 accepted receipt를 hold/expired로 바꾸지 않는다.

여러 사실이 동시에 관찰되면 다음 precedence를 사용한다. authorization/request invalid는 classification 전 domain error다. 그 뒤 `unavailable 또는 incomplete coverage -> generation ambiguity/drift -> metadata conflict -> manifest conflict -> raw content conflict -> missing 계열 -> valid 계열` 순서로 하나의 coarse classification을 고정하고 세부 사실은 bounded reason/evidence field에 남긴다.

inventory가 `truncated=true`인데 관찰 candidate가 2개 미만이면 provider/page 이상으로 coverage incomplete `unavailable`이다. 이미 exact-path candidate를 2개 이상 관찰했다면 더 읽어 하나를 고르지 않고 `generation_drift/multiple_*_generations`로 닫는다.

### 5. Receipt state별 missing 의미

| purpose/state | 관측 | 결과 |
| --- | --- | --- |
| forward / `reserved` | complete manifest inventory 0, complete raw inventory 0 | `none/no_candidates` |
| forward / `reserved` | manifest 유일, complete raw inventory candidate 0 | `manifest_only/referenced_raw_not_found` |
| forward / `reserved` | manifest가 참조한 raw generation은 없고 다른 raw generation 존재 | `generation_drift/referenced_generation_missing_other_present` |
| forward / `reserved` | manifest 0, raw inventory 중 pin 뒤 exact NotFound | `generation_drift/generation_changed_during_read` |
| accepted integrity | complete inventory에 receipt의 manifest 또는 raw exact generation이 없고 다른 candidate도 없음 | `stored_missing/*` |
| accepted integrity | receipt의 exact generation은 없고 다른 generation 존재 | `generation_drift/accepted_generation_missing_other_present` |
| forward / `reserved` | soft-deleted candidate 존재 | `generation_drift/soft_deleted_candidate_present` |
| accepted integrity | receipt의 exact generation이 soft-deleted됨 | `stored_missing/accepted_generation_soft_deleted` |
| 모든 허용 상태 | permission·quota·timeout·cancel·unknown provider error | `unavailable/*`; missing으로 강등 금지 |

inventory에 candidate가 있었는데 exact read 전에 사라진 것은 `none`이 아니다. 권위값이 관찰 중 바뀐 `generation_drift`다. direct object read의 404만으로 missing을 결정하지 않으며, bucket과 exact path query가 성공하고 soft-delete coverage까지 complete인 inventory와 결합된 NotFound만 missing 계열로 사용할 수 있다.

### 6. Manifest-first exact validation

1. expected manifest path의 version-aware inventory를 bounded 조회한다.
2. candidate가 복수면 bytes가 같아 보여도 `generation_drift`로 끝내고 어떤 generation도 읽어 권위값으로 선택하지 않는다.
3. 유일 candidate면 generation을 고정하고 attrs를 검사한 뒤 exact generation bytes를 bounded read한다.
4. manifest v1은 UTF-8, duplicate key, unknown field, trailing value를 거부하고 모든 timestamp를 strict UTC로 parse한다.
5. manifest version, payload schema, compression, content type와 receipt immutable input 전체를 교차검증한다.
6. decoded manifest를 `CanonicalTelemetryManifest`로 다시 생성해 exact bytes와 비교한다.
7. manifest가 가리킨 raw path는 version-aware inventory로 coverage와 candidate를 확인한다. referenced generation이 유일한 권위 candidate일 때만 exact inspect/read하고 raw latest 또는 다른 generation으로 fallback하지 않는다.
8. manifest/raw path에 복수 live·noncurrent candidate나 예상 밖 soft-deleted candidate가 있으면 bytes를 비교해 자동선택하지 않고 precedence에 따라 drift 또는 stored-missing으로 닫는다. `manifest_only`는 referenced raw path의 live·noncurrent·soft-deleted candidate가 모두 0일 때만 허용한다.
9. 두 artifact 모두 read 뒤 같은 exact generation을 다시 inspect하고 path·generation·metageneration·hash·CRC·size·headers·metadata가 첫 snapshot과 같은지 확인한다.
10. manifest candidate가 0일 때만 deterministic raw path inventory를 forward raw-only 탐색에 사용한다. manifest가 유일한 경우의 raw inventory는 그 manifest가 참조한 generation 존재·ambiguity 확인에만 사용한다.

### 7. Raw strict validation과 validator registry

- raw exact compressed bytes의 size·SHA-256·CRC32C를 먼저 확인한다.
- gzip은 max raw body+1 bound로 해제하고 trailing compressed stream·overflow·corrupt stream을 거부한다.
- receipt의 `validator_version`으로 decoder, payload validator, canonical manifest builder와 codec profile을 explicit registry에서 찾는다. codec profile은 compressor 구현/version, parameters와 canonical raw fixture의 compressed-byte golden digest 묶음을 함께 고정한다.
- unknown·retired validator를 current 구현으로 대체하지 않고 `unavailable/validator_unavailable`을 반환한다.
- Go `compress/flate`의 byte output은 Go 버전 간 API 호환성이 보장되지 않는다. registry startup self-test가 지원 profile의 golden compressed bytes와 다르면 그 profile을 활성화하지 않고 `unavailable/codec_profile_unavailable`로 닫는다. 장기 prior-version 지원은 compressor 구현을 vendor/freeze하거나 별도 compatibility reader image로 보존하며 현재 toolchain 이름만으로 호환성을 주장하지 않는다.
- strict decoder와 validator는 tenant/device/trip/installation/consent/client batch, schema, sample count, captured bounds와 body hash를 receipt immutable input과 비교한다.
- exact compressed digest는 실제 bytes와 attrs의 자체 무결성을 검증하고, decompressed body hash·strict identity는 receipt lineage를 검증한다. recompression은 codec provenance 보조 검사이지 terminal content-conflict 근거가 아니다. raw body는 일치하지만 recompressed bytes가 다르면 reject하지 않고 codec profile unavailable로 hold한다.

### 8. Provider error contract

- GCS `storage.ErrObjectNotExist`, HTTP 404 또는 gRPC NotFound도 complete exact-path inventory와 결합해 요청한 exact generation 부재를 확인할 때만 NotFound다. direct 404 하나만으로 bucket 부재, hidden permission과 object 부재를 구분했다고 주장하지 않는다.
- caller context가 끝났으면 `context.Canceled|DeadlineExceeded` Go error로 즉시 중단한다. caller context가 살아 있을 때의 provider-side cancel/timeout, permission, unauthenticated, quota/resource exhausted, rate limit, 5xx와 알 수 없는 provider 오류는 `unavailable` typed result다.
- soft-delete가 비활성이라는 승인된 bucket policy 증거나 성공한 soft-deleted inventory query가 없으면 coverage incomplete이며 missing으로 분류하지 않는다.
- object attrs를 받았으나 필수 identity·header·metadata가 malformed이면 `metadata_conflict`다.
- inventory/read/reinspect 사이 generation·metageneration 또는 attrs가 바뀌면 `generation_drift`다.
- error string을 reason이나 log에 그대로 복사하지 않는다. provider error는 고정된 low-cardinality reason으로 normalize한다.

### 9. R5 독립 완료 gate

다음이 모두 충족돼야 R5 classifier를 local 완료로 본다.

- 열 classification과 허용 reason matrix의 table test
- forward와 accepted request shape·state·grant의 receipt/reservation/revision/consent/fence binding invalid fixture가 Storage call 0
- current recovery authorization 전 reader call 0
- manifest unique pin과 manifest/raw 복수 generation 자동선택 0
- exact-path prefix sibling(`expectedPath + ".bak"` 등)이 candidate에 포함되지 않는 inventory adapter test
- exact compressed generation read, max+1 bound와 pre/post attrs drift test
- manifest duplicate/unknown/trailing/noncanonical/cross-lineage test
- raw gzip/digest/strict payload/body hash, codec golden self-test와 validator registry test
- complete live/noncurrent·soft-delete inventory와 incomplete coverage, NotFound, 403/429/timeout/quota/cancel/malformed/provider unknown 분리; incomplete/error를 missing으로 분류한 case 0
- 모든 case에서 Storage create/delete와 receipt/index mutation 0
- result/log privacy scan에서 raw body·좌표·token·UID/App ID 0
- provider-neutral unit·race test, pinned official Storage testbench와 GitHub clean runner 통과
- HTTP GCS reader factory만 exact compressed raw read를 수행하고 gRPC/arbitrary bucket injection은 unavailable 또는 composition 실패가 되는 test

R5 완료는 forward reconciler, attempt completion, sweeper, cleanup, runtime 또는 staging 완료를 뜻하지 않는다.

## 결과와 위험

- recovery가 최신 object를 암묵적으로 선택하지 않고 관측 가능한 ambiguity를 별도 결과로 남긴다.
- write adapter와 reader가 일부 low-level validation을 공유하더라도 public port와 side effect 책임은 분리된다.
- version-aware inventory와 read 전후 inspect로 Storage operation 수가 늘어난다. bounded page와 classifier 전 authorization으로 비용을 제한한다.
- version inventory에는 bucket-wide `storage.objects.list`가 필요하다. reader service account는 telemetry artifact 전용 bucket·project에 한정하고 application exact-path·tenant binding을 재검증한다. IAM이 object prefix를 세밀하게 제한해 준다고 가정하지 않으며 list 권한의 blast radius를 staging security review에 남긴다.
- accepted integrity audit와 forward recovery가 같은 분류기를 재사용할 수 있지만 authorization·후속 action은 섞지 않는다.
- versioned bucket과 soft delete의 실제 candidate 의미는 staging lifecycle 검증 없이는 확정할 수 없다. 해당 검증 전 production wiring은 차단한다.

## 구현·rollback 순서

1. provider-neutral request/result/reason과 validator registry
2. GCS exact-path bounded version inventory와 narrow NotFound mapping
3. strict manifest verifier와 canonical byte comparison
4. manifest-referenced raw exact read 및 manifest-absent raw inventory
5. raw strict validator·deterministic recompression과 pure classification matrix
6. official testbench·clean CI evidence

rollback은 write 또는 receipt 상태를 되돌리는 작업이 아니다. reader/classifier runtime 연결을 중지하고 receipt와 artifact를 변경하지 않은 채 이전 read-only build로 돌아간다.

## 연결 문서

- 선행 결정: [ADR-0016](./ADR-0016-immutable-telemetry-artifact-lineage.md), [ADR-0017](./ADR-0017-fenced-ingest-recovery.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 사전절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 제품 업데이트: 해당 없음 — 결정 문서이며 runtime 변경 없음
- 증거: [EVD-20260721-023](../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
