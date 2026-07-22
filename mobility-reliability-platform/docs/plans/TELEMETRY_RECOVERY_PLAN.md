# Telemetry Reservation Recovery 실행계획

## 1. 문서 성격

이 문서는 [ADR-0017](../decisions/ADR-0017-fenced-ingest-recovery.md)을 구현하기 위한 작업 분해와 검증 기준이다. 아래 항목은 계획이며 코드·배포·운영 성과를 의미하지 않는다. 실제 결과는 `docs/evidence/`, 사람용 설명은 `docs/reports/human/`, runtime에 연결된 변화만 `docs/product-updates/`, 실제 SEV 영향만 `docs/incidents/`에 기록한다.

## 2. 목표와 비목표

### 목표

- 한 `reserved` receipt를 동시에 처리할 owner를 한 명으로 제한한다.
- lease takeover 뒤 stale worker의 모든 Firestore mutation을 fencing token으로 차단한다.
- 최초 reservation에 canonical manifest 재검증에 필요한 immutable input을 보존한다.
- raw-only, raw+manifest, manifest-only, no-artifact, stored-missing을 generation-pinned 방식으로 분류한다.
- 만료 전 forward reconciliation과 만료 후 exact-generation cleanup을 분리한다.
- current consent 철회 뒤 pending recovery가 새 artifact를 만들지 않게 한다.
- WSL2 local, Firestore Emulator, official Storage testbench와 GitHub clean runner에서 재현 가능한 crash/race evidence를 남긴다.

### 이번 gate의 비목표

- production Firebase/GCP 배포 또는 실제 GPS 수집 활성화
- path·prefix·latest object 기준 대량 삭제
- receipt 없는 bucket object의 자동 삭제
- operator 승인 없는 `recovery_hold` 해제
- stored-missing artifact의 추정 재생성
- Cloud Scheduler·Cloud Run IAM 검증 전 readiness 활성화
- projector, ML dataset, 알림과의 연결

## 3. 구현 단위와 의존성

```text
R1 immutable reservation input
  └─> R2 lease/fence domain contract
        └─> R3 Firestore atomic lease + fenced finalizer
              ├─> R4 HTTP request ownership
              └─> R5 read-only artifact classifier
                    └─> R6 forward reconciler
                          └─> R7 bounded sweeper + attempt ledger
                                └─> R8 expiry cleanup target
                                      └─> R9 staging/runbook gate
```

- R1~R4가 없으면 동일 request replay가 계속 Storage에 진입한다.
- R5 없이 sweeper가 write/delete를 수행하지 않는다.
- R6은 `reservation_deadline` 전만 허용한다.
- R8은 R6과 다른 mode·claim·상태 전이를 사용한다.
- R9 전에는 executable adapter wiring과 readiness를 열지 않는다.

## 4. 단계별 작업

### R1. Immutable reservation input

대상:

- `internal/ingest.Reservation`, `Receipt`
- Firestore receipt와 두 ingest index DTO·validator·fixtures
- Target Domain Model과 golden manifest fixture

추가 필드:

```text
expected_sample_count
first_captured_at
last_captured_at
validator_version
reservation_deadline
artifact_expires_at
receipt_retention_floor
purge_eligible_at?  # reservation 때 null
```

불변조건:

- sample count는 telemetry max 범위 안이다.
- first/last captured time은 payload validation 결과와 일치한다.
- validator version은 비어 있지 않고 reservation 뒤 바뀌지 않는다.
- `created_at < reservation_deadline < artifact_expires_at`이고 receipt retention floor가 유효하다.
- `purge_eligible_at`은 terminal cleanup·감사 완료 전 null이며 완료 transaction이 두 index와 receipt에 함께 설정한다. 실제 linkage 삭제 전에는 nested attempt와 linked cleanup target·integrity finding을 bounded purge job으로 먼저 제거한다.
- 기존 `expires_at` 필드는 의미를 겹쳐 쓰지 않고 migration/compatibility 결정 후 제거 또는 명시적 alias로 제한한다.

완료 증거:

- create/decode/round-trip test
- 기존 index `expires_at` 의미를 retention floor/nullable purge eligibility로 분리한 linkage·subcollection purge test
- 필드 하나씩 누락·변조한 fail-closed table test
- immutable reservation input과 exact `StoredArtifact` lineage를 함께 넣으면 동일 canonical manifest bytes가 생성되는 golden test

### R2. Lease와 fence domain contract

추가 type 후보:

```go
type LeaseOwner struct {
    ID   string
    Kind LeaseOwnerKind
}

type LeaseProposal struct {
    Owner    LeaseOwner
    Duration time.Duration
}

type LeaseFence struct {
    OwnerID  string
    Token    int64
    ExpiresAt time.Time
}
```

status를 다음처럼 분리한다.

```text
created_lease_acquired
replay_lease_acquired
replay_in_progress
replay_complete
replay_rejected
replay_recovery_hold
replay_expired
idempotency_conflict
client_batch_conflict
```

완료 증거:

- invalid UUID, kind, token, duration, timestamp ordering 거부
- replay-in-progress가 “artifact 처리 권한 있음”으로 해석되지 않는 service test
- accepted `stored|queued|projected|deleting|deleted`는 동일 lineage replay에 `replay_complete`, rejected는 승인된 side cleanup 중에도 `replay_rejected`를 유지
- cleanup 시작 시 `cleanup_mode`와 `cleanup_origin_status`가 고정되고 이후 변경되지 않는 invariant test
- client input에 owner/fence field가 존재하지 않는 wire-contract test

### R3. Firestore atomic lease와 fenced finalizer

신규 reservation transaction:

1. current authorization exact read
2. 두 index read
3. 신규 index 2개와 `reserved` receipt 생성
4. 같은 receipt에 initial owner, `fencing_token=1`, lease timestamps와 `next_recovery_at=lease_expires_at` 기록

pending replay transaction:

1. current authorization 재평가
2. 3-way linkage·receipt state·deadline 검증
3. active lease면 read-only `replay_in_progress`
4. expired lease면 owner 교체, token·receipt revision +1

모든 `RenewLease`, `ReleaseLease`와 실제 forward 상태를 바꾸는 `MarkStored`, `MarkRejected`, `MarkRecoveryHold`는 owner ID·token·deadline을 transaction에서 확인한다. 단, deadline이 지난 receipt에는 유효 forward fence가 존재할 수 없으므로 `BeginCleanupTransition`을 별도 claim primitive로 둔다. 이 transaction만 `reserved + reservation_deadline <= cleanupNow + active lease 없음 + 3-way linkage 정상`을 확인한다. 여기서 조기 삭제를 막는 `cleanupNow=min(application UTC, transaction read time)`이다. 조건을 만족하면 token·revision을 정확히 1 증가시켜 `cleanup_pending`, `cleanup_mode=reservation_expiry`, `cleanup_origin_status=reserved`와 quiet-period 기준을 원자 기록한다. reserved-origin hold는 `BeginHeldCleanup`, accepted receipt는 `BeginAcceptedDeletion`, rejected artifact는 명시적 보안 승인 전용 `BeginRejectedArtifactCleanup`으로 분리한다. 이미 accepted+동일 전체 lineage 또는 rejected+동일 code인 read-only finalizer replay는 update·revision 증가 없이 기존 receipt를 반환하며 active lease를 요구하지 않는다.

필수 경쟁 테스트:

| 경쟁 | 기대 결과 |
| --- | --- |
| 신규 동일 batch 2개 | acquired 1, in-progress 1, receipt/index 각 1 |
| expired lease takeover 2개 | token +1 winner 1 |
| renew 대 takeover | transaction commit 순서에 맞는 owner 하나 |
| old token finalizer 대 new claim | old finalizer update 0 |
| old token reject 대 new stored | reject update 0 |
| `now == lease_expires_at` | 기존 owner 만료, takeover 허용 |
| deadline보다 긴 lease | claim 거부 |
| deadline 전 recovery claim 대 `BeginCleanupTransition` | recovery claim만 허용 |
| deadline 경계/후 recovery claim 대 `BeginCleanupTransition` | cleanup transition winner 1, recovery lease 0 |
| stale forward finalizer 대 cleanup transition | cleanup 상태 유지, stale update 0 |
| accepted deletion 대 동일 batch replay | replay-complete, Storage call 0 |
| rejected artifact cleanup 요청, 승인/ownership 없음 | receipt rejected 유지, target/delete 0 |
| 승인된 rejected artifact cleanup 대 replay | receipt rejected 및 replay-rejected 유지 |

Firestore index 계획:

- server sweeper query용 `status ASC, next_recovery_at ASC, __name__ ASC`
- tenant별 query를 택하면 collection index, 전체 service worker면 collection-group index를 명시한다.
- 같은 due time은 document name tie-breaker와 `(next_recovery_at, __name__)` cursor로 deterministic page한다.
- claim loss는 현재 owner의 due time을 건드리지 않고, processing failure/safe release만 bounded backoff로 `next_recovery_at`을 갱신한다.
- candidate query는 advisory이며 실제 소유권은 transaction claim만 결정한다.

### R4. HTTP request ownership

- request마다 server UUID owner를 transaction 밖에서 한 번 생성한다.
- acquired 상태에서만 deterministic gzip과 `StoreBatch`를 실행한다.
- in-progress는 artifact adapter call 0을 보장하고 pending receipt와 bounded retry hint를 반환한다.
- transient error 뒤 safe release가 성공하면 backoff를 기록한다.
- context timeout으로 release 여부가 불명확하면 lease expiry가 takeover를 허용한다.
- lease remaining threshold 아래에서는 manifest/finalizer 전 heartbeat한다.
- raw exact bytes conflict만 현재 fence owner가 terminal reject할 수 있다.

HTTP status는 mobile retry contract와 함께 별도 test로 고정한다. 구현 편의를 위해 200 replay와 202 pending을 같은 의미로 섞지 않는다.

### R5. Generation-pinned read-only classifier

세부 classifier 계약은 [ADR-0018](../decisions/ADR-0018-generation-pinned-read-only-classifier.md), current system authorization 계약은 [ADR-0019](../decisions/ADR-0019-current-forward-recovery-authorization.md)을 기준으로 한다. public artifact recovery port는 write port와 분리하며, recovery claim은 artifact permission으로 사용하지 않는다. `forward_recovery`는 authoritative receipt와 current tenant·beneficiary membership·installation·trip·assignment·precise-location consent를 같은 Firestore transaction snapshot에서 재평가하고 exact grant binding을 확인한 뒤에만 classifier를 호출한다. 이 경계 전에는 Storage call이 0이어야 한다.

```text
InspectExpectedArtifacts
PinManifestGeneration
ReadManifestGeneration
PinReferencedRawGeneration
ReadRawGenerationCompressed
ClassifyAgainstReceipt
```

검증 순서:

1. version-aware inventory에서 manifest candidate가 하나인지 확인한다. bytes가 동일해 보여도 candidate가 복수면 hold하고 유일할 때만 pin·strict decode한다.
2. manifest의 raw generation을 exact read한다.
3. manifest가 없을 때만 raw deterministic path의 candidate generation을 pin한다.
4. read 전후 attrs·metageneration·metadata drift를 확인한다.
5. gzip bytes digest와 decompressed strict payload를 receipt immutable input과 비교한다.
6. permission, quota, timeout, malformed attrs는 `missing`으로 바꾸지 않는다.
7. receipt `validator_version`을 explicit decoder/validator/manifest-builder registry에서 찾고 unknown version은 hold한다.

classifier output 후보:

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

coarse classification과 low-cardinality reason code를 분리한다. 복수 manifest/raw candidate와 read 중 attrs drift는 `generation_drift`, unknown validator와 provider 검증 불능은 `unavailable`, malformed·noncanonical·cross-lineage manifest는 `manifest_conflict`다. reserved의 provider-confirmed no-artifact만 `none`, accepted receipt가 고정한 exact generation의 provider-confirmed NotFound만 `stored_missing`으로 분류한다.

이 단계는 object create/delete, receipt/index mutation, attempt completion, hold/reject/finalizer와 runtime wiring을 하지 않는다. R5 독립 완료 조건은 ADR-0018의 classification/reason matrix, strict manifest/raw validation, version inventory ambiguity, NotFound/error 분리, max+1 bound, privacy scan, official testbench와 clean CI다.

2026-07-21 현재 provider-neutral classifier 구현과 local 독립 gate는 [EVD-20260721-023](../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier)에서 `verified`됐다. 이 상태는 request/grant 계약, HTTP GCS reader, strict content validator와 read-only orchestration을 합성한 **local R5 증거**다. current system recovery/integrity authorizer, startup composition, staging version·soft-delete semantics와 R6 이후 mutation은 포함하지 않으므로 runtime readiness는 계속 닫아 둔다.

2026-07-21 current-state forward recovery authorizer와 read-only Firestore adapter도 [EVD-20260721-024](../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization)에서 local/Emulator `verified`됐다. Authoritative receipt, current beneficiary 관계·동의와 sweeper fence를 확인해 30초 이하 forward grant를 발급하며 consent withdrawal 뒤 새 grant는 0이다. Accepted-integrity authorizer, authorizer→classifier startup composition과 R6 action은 포함하지 않는다.

### R6. Forward reconciler

구체적인 protocol, capability 경계와 action matrix는 [ADR-0020](../decisions/ADR-0020-two-pass-forward-reconciliation.md)을 기준으로 한다. 이 절의 항목이 ADR-0020보다 넓거나 모호하면 ADR-0020의 fail-closed 규칙을 우선한다.

전제:

- 현재 fence owner
- `now < lease_expires_at`
- `now < reservation_deadline`
- current system recovery authorization valid
- classifier request/result 계약이 유효하고 action matrix가 해당 결과를 명시적으로 허용함

동작:

- raw-only: validated raw generation을 이용해 reservation의 pinned validator version으로 canonical manifest를 생성하고 별도 opaque manifest-repair capability로 `DoesNotExist` write한다. raw create·rewrite·delete는 0이다.
- validator registry는 current와 아직 deadline/cleanup이 끝나지 않은 prior version decoder·manifest builder를 함께 보존한다. unknown version은 current code로 fallback하지 않는다.
- complete: fresh current authorization과 두 번째 full classifier pass로 raw와 manifest cross-lineage를 다시 확인한다.
- raw-only도 manifest create 뒤 fresh current authorization과 두 번째 full classifier pass를 수행한다. 두 경우 모두 두 번째 결과가 exact `valid_complete`이고 pinned lineage가 앞선 증거와 일치한 뒤, final action 전용 opaque grant와 같은 Firestore transaction 안의 current 관계·동의 재평가까지 성공할 때만 같은 current fence로 `MarkStored`한다.
- manifest-only, metadata conflict, generation drift는 `recovery_hold`로 보낸다.
- no-artifact는 `awaiting_client_replay` code와 bounded backoff로 release한다.
- current authorization denied는 새 artifact write 0, `recovery_hold(current_authorization_denied)`다. current authorization 조회 자체가 unavailable이면 artifact write 0, `release_lease(authorization_unavailable)`와 bounded backoff다. 철회 자체는 기존 artifact 자동삭제 trigger가 아니며 명시적 삭제 요청 또는 승인된 retention expiry만 cleanup을 시작한다.
- reserved-origin 만료 전 hold는 reason과 `now <= hold_review_due_at < artifact_expires_at`을 기록하고 reserved query에서 제외한다. 이미 만료된 뒤 처음 발견된 reserved finding은 `hold_review_due_at=now`와 즉시 cleanup 필요 상태로 기록한다. review 미완료 상태로 retention expiry가 오면 `BeginHeldCleanup`을 통해 exact-generation cleanup으로 넘긴다. accepted integrity finding은 receipt를 hold/expired로 downgrade하지 않고 `deleting/deleted` workflow를, rejected artifact는 상태를 유지한 채 ownership 증명·보안 승인 workflow를 사용한다. ambiguous lineage나 provider 실패로 삭제할 수 없으면 alert를 유지하고 incident 기준으로 escalate한다.
- receipt state를 바꾸는 stored/rejected/hold/release는 current attempt completion과 같은 Firestore transaction에 기록한다. 응답 유실 또는 과거 partial ledger는 receipt revision과 recovery correlation으로 idempotent하게 재구성한다.
- planner는 initial/confirmation/post-manifest-confirmation phase를 입력으로 받는다. Manifest create 뒤에는 exact complete만 stored로 진행하며 raw-only 재복구, none release와 raw conflict reject로 되돌아가지 않는다. Transient provider 오류만 release하고 나머지는 hold한다.
- cancellation·crash로 action이 없었던 started attempt는 current fence에서 bounded failed로 닫거나, 다음 claim transaction이 expired prior attempt를 `failed/lease_expired`로 닫은 뒤 새 attempt를 시작한다.

2026-07-22 현재 phase-aware planner와 manifest-only write 경계는 [EVD-20260722-025](../evidence/2026-07.md#evd-20260722-025--two-pass-forward-recovery-planner와-manifest-only-repair-boundary), terminal stored·rejected·hold·release transaction, attempt-only failure, expired prior closure와 fresh outcome correlation은 [EVD-20260722-026](../evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계)에서 local/Emulator `verified`됐다. Current authorization denied를 고정 hold로, readable-malformed unavailable을 고정 release로 변환하는 별도 capability와 transaction 시점 재평가·decision-domain outcome도 [EVD-20260722-027](../evidence/2026-07.md#evd-20260722-027--current-authorization-disposition-원자-경계)의 local/Emulator gate를 통과했다. 이 component를 이미 claim된 receipt 하나에 대해 authorize→classify→optional manifest repair→fresh classify→action/disposition→outcome 순서로 실행하는 bounded reconciler와 renewal evidence 폐기, commit finalizer tail·late-commit barrier는 [EVD-20260722-028](../evidence/2026-07.md#evd-20260722-028--bounded-forward-reconciler-composition)에서 local/Emulator/testbench/clean CI 검증됐다. 이 단락의 R6 증거 자체는 candidate discovery·claim outer loop를 포함하지 않으며, R7의 별도 현재 상태는 아래에서 구분한다.

### R7. Bounded sweeper와 recovery attempt ledger

worker 한 회 실행 제한:

- page size, max pages, per-receipt deadline, total run deadline을 config로 제한한다.
- poison receipt 한 개의 실패가 다음 receipt를 막지 않는다.
- exponential backoff에는 상한과 deterministic test용 jitter seam을 둔다.
- candidate count, claim won/lost, classification, action, latency, hold count를 metric으로 낸다.
- 로그와 attempt document에 body·좌표·UID/App ID를 넣지 않는다.
- `stored`·`rejected`는 reserved query로 찾지 않는다. `next_integrity_check_at`과 version-aware Storage Inventory를 사용하는 별도 bounded integrity-audit cursor로 stored-missing·rejected-artifact를 검사한다.

recovery attempt document:

```text
attempt_id, tenant_id, receipt_id,
owner_kind, fencing_token, worker_version,
status(started|completed|failed),
classification?, action?, outcome?, error_class?,
object_path?, object_generation?,
manifest_path?, manifest_generation?,
started_at, completed_at?
```

expired lease를 takeover한 request와 sweeper/cleanup claim은 claim transaction에서 `started` attempt와 count 증가를 함께 기록한다. 최초 request는 recovery attempt가 아니다. completion ledger write 실패가 receipt의 이미 성공한 finalizer를 rollback할 수 없으므로, receipt revision과 correlation ID로 재구성 가능한 운영 규칙을 둔다.

2026-07-22 현재 [ADR-0021](../decisions/ADR-0021-bounded-forward-recovery-worker.md)에 따라 tenant-scoped due query, deterministic cursor, fixed scan-cutoff epoch, advisory CAS checkpoint, fresh transactional claim과 R6 handoff를 합성한 worker component를 구현했다. 기본 한 회 budget은 page size 25, 최대 8 page·100 candidate, page 10초, claim 10초, item 2분, run 15분이며 checkpoint 장애나 CAS conflict가 receipt authority가 되지 않도록 이후 checkpoint write만 비활성화하고 현재 scan은 계속한다. Acquired 뒤 parent가 취소되면 이미 시작된 attempt만 detached bounded context로 drain하고 새 claim은 만들지 않는다. Local/Emulator 근거는 [EVD-20260722-029](../evidence/2026-07.md#evd-20260722-029--bounded-forward-recovery-worker와-cross-run-checkpoint)에 분리한다.

이 구현은 startup·scheduler·metrics exporter에 연결되지 않았고 production composite index `READY`, ADC/IAM, 실제 사용자 GPS 또는 staging Storage lifecycle을 증명하지 않는다. 또한 `status`·`next_recovery_at` 자체가 누락되거나 query 비호환 type인 receipt는 due query에 보이지 않으므로 별도 bounded control-integrity audit가 필요하다. Fixed cutoff는 page 간 Firestore snapshot을 고정하지 않으므로 state/due 변경과 backfill은 중복 scan 또는 다음 epoch까지의 지연을 만들 수 있다.

### R8. Expiry·integrity cleanup과 bounded purge

forward sweeper와 별도 mode로 구현한다.

1. `BeginCleanupTransition` transaction이 `reserved`, deadline 경과, active lease 없음과 3-way linkage를 확인한다. 만료 forward lease에 recovery attempt count가 있으면 exact nested attempt owner·token·version·started time을 함께 읽고, `started`를 같은 transaction에서 `failed/lease_expired`로 닫은 뒤 token·revision +1, `cleanup_pending`, `cleanup_mode=reservation_expiry`, `cleanup_origin_status=reserved`를 기록한다. Missing·malformed·completed prior attempt는 receipt write도 0으로 fail-closed한다. Cleanup effective time은 application·receipt·attempt snapshot 전체의 최솟값이며 전체 clock 폭이 허용 skew를 넘으면 거부한다. 일반 forward finalizer는 이 전환을 수행할 수 없다. 상세 결정과 local/Emulator 근거는 [ADR-0022](../decisions/ADR-0022-atomic-cleanup-transition-attempt-closure.md), [EVD-20260722-030](../evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료)을 따른다.
2. `cleanup_quiescence_until = max(last lease expiry, transition time) + late-write grace`를 고정한다. 현재 policy v1은 `11분 > 최대 lease 5분 + raw·manifest 전체 StoreBatch 5분`을 strict하게 만족하며 transition time·quiescence·mode·origin·policy version을 claim이 다시 쓰지 못한다.
3. quiet period 뒤 owner kind `cleanup`, worker version `telemetry-cleanup.v1`로 별도 lease를 claim한다. First claim과 expired takeover는 token·revision·attempt count를 1 증가시키고 `started` cleanup attempt를 같은 transaction에 만든다. Forward mutation port는 cleanup owner를 거부하고 `next_recovery_at`은 제거한다. 상세 결정과 local/Emulator/testbench 근거는 [ADR-0023](../decisions/ADR-0023-fenced-cleanup-lease-claim.md), [EVD-20260722-031](../evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim)을 따른다.
4. Cleanup 전용 read purpose·issuer와 exact cleanup fence로 version-aware classifier를 실행한다. Request binding뿐 아니라 classification, inventory, pinned lineage와 observed time 전체를 classifier-produced seal로 검증한다.
5. 삭제 전 immutable dry-run target document에 exact path·generation·hash, bounded inventory와 decision을 create-once로 기록한다. 동일 target replay는 write 0이며 conflict는 fail-closed한다. 이 target은 delete 또는 `expired` 권한이 아니다. 상세 결정과 local/Emulator 근거는 [ADR-0024](../decisions/ADR-0024-immutable-cleanup-dry-run-target.md), [EVD-20260722-032](../evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target)을 따른다.
6. [ADR-0025](../decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md)에 따라 persisted target과 current cleanup fence를 다시 승인하고 raw exact generation+metageneration을 먼저 조건부 삭제한다. Delete 2xx와 direct 404는 부재 증거가 아니다.
7. Raw path의 regular-version·soft-deleted inventory가 complete empty일 때만 manifest exact generation+metageneration을 조건부 삭제한다. Raw 결과가 drift·unknown·present·soft-deleted·audit unavailable이면 manifest delete는 0이다.
8. Manifest를 포함한 모든 expected path의 complete empty inventory를 별도로 확인한다. R8c는 delete 전송 outcome과 post-delete audit을 분리한 non-authoritative in-process success observation을 만든다. R8d는 dispatch와 delete RPC outcome을 fresh fenced attempt ledger에 보존한다. R8e dedicated signed path는 Firestore current state에서 발급한 read-only grant, paired GCS auditor의 Ed25519 evidence와 bounded transaction 재검증을 모두 통과한 raw·manifest `confirmed_absent` phase만 durable하게 저장한다. R8g phase executor는 artifact별 dispatch를 먼저 commit하고 `applied` winner에게만 single-artifact mutation grant를 준다. Bounded outcome을 다음 revision에 저장한 뒤 known outcome만 signed audit로 전진하며, `unknown` 또는 replayed dispatch에서는 audit·counterpart 호출 없이 정지한다. Generic progress API는 계속 audit phase를 거부한다.
9. R8h success finalizer는 exact `manifest_absence_confirmed/revision 7`, fresh two-path evidence, target·plan·receipt revision과 active immutable fence를 다시 검증한 뒤 attempt `completed/outcome=expired`, receipt `expired`와 receipt·두 index의 같은 `purge_eligible_at`을 4문서 transaction으로 commit한다. Immutable target은 쓰지 않는다. Linked late generation, unknown outcome 또는 linkage drift가 있으면 finalization은 0이며 후속 retry·hold 또는 별도 linked target으로 분리한다.
9a. R8i failure disposition은 allowed phase 2/3/5/6과 revision을 그대로 보존하고 10개 bounded error class를 exhaustive retry·hold policy로 매핑한다. Attempt `completed/cleanup_retry|cleanup_hold`와 receipt cleanup cursor만 2문서 transaction으로 commit하며 target과 두 index는 write 0이다. Retry는 `max(old fence expiry, completed_at + backoff)`에서 exact terminal attempt·old target·evidence·fence를 다시 검증한 claim만 pristine attempt를 만들고, hold는 review due 뒤에도 자동 claim하지 않는다. Commit 응답 유실은 첫 pre-state query를 보존한 read-only `committed|not_committed|unverifiable` correlation으로만 판별한다.
9b. R8j terminal orchestrator는 R8g의 package-private sealed intent만 소비해 R8h finalization 또는 R8i disposition 중 하나를 invocation당 최대 한 번 호출한다. Exported result·Go error·phase enum만으로 terminal을 선택하지 않으며 durable `unknown`은 저장된 class와 exact binding을 복원한다. Outcome/audit persistence ambiguity, unknown mutation status, generic/internal error와 복수 class는 terminal mutation 0이다. Terminal commit response loss는 valid pre-state query가 있을 때만 parent cancellation과 분리한 최대 5초 read-only correlation으로 `committed|not_committed|unverifiable`을 판별하고 mutation을 재호출하지 않는다.
10. reserved-origin `recovery_hold`는 retention expiry가 오면 `BeginHeldCleanup` transaction으로 `cleanup_pending`에 진입하고 `cleanup_origin_status=recovery_hold`를 고정한다. accepted `stored|queued|projected` finding은 승인된 lifecycle/deletion evidence를 대조한 뒤 `BeginAcceptedDeletion`으로 `deleting -> deleted`를 사용하며 replay-complete를 보존한다. rejected artifact는 receipt를 `rejected`로 유지하고 exact ownership과 명시적 보안 승인이 있을 때만 side cleanup target을 만든다.
11. terminal cleanup·감사 완료 뒤에만 두 index와 receipt의 `purge_eligible_at`을 같은 transaction에서 설정한다. linkage 문서는 독립 TTL로 삭제하지 않는다.
12. [ADR-0033](../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md)의 proposed R8k contract에 따라 eligibility가 도달하면 tenant+receipt digest key의 top-level purge job과 receipt purge fence를 같은 transaction으로 만들고 새 attempt/cleanup target/integrity finding 생성을 차단한다. Receipt ID만 job key로 쓰지 않는다. Top-level target/finding create는 receipt 하위 inverse `purgeLinks` registry를 같은 transaction으로 만들어야 한다.
13. Receipt 하위 `recoveryAttempts`와 `purgeLinks`를 phase별 `__name__` cursor·최대 100개 page로 처리한다. Link page는 registry가 가리키는 exact target/finding child와 link를 함께 삭제한다. Exact delete와 job cursor/count/revision 전진을 같은 transaction으로 commit하며 malformed·foreign child를 skip하지 않는다. Fresh attempt/link empty와 well-formed unregistered top-level child 0을 확인한 뒤에만 `ready`가 된다. Registry 도입 전 legacy child와 out-of-band malformed writer는 staging inventory·writer-exclusion gate로 분리한다.
14. 마지막 transaction은 ready job·receipt fence·receipt revision/linkage hash와 두 uniqueness index를 재검증하고 job update+두 index+receipt 네 mutation을 같은 commit으로 수행한다. Purge job에는 위치·Firebase UID·device/trip/person ID·object path 없이 hash·count·완료시각만 남기고 별도 자체 retention worker 전에는 삭제하지 않는다.

[ADR-0026](../decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)은 8~9단계와 11단계의 구현 계약을 확정한다. Immutable target은 갱신하지 않고 exact cleanup attempt를 dispatch intent·delete outcome·complete-empty audit의 단조 실행 원장으로 확장한다. Pure ledger와 Firestore planned initialization·dispatch/outcome persistence는 [EVD-20260722-034](../evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence)의 local/Emulator 범위에서 구현했다. Target 생성 뒤 lease renewal은 금지하며, generic progress API는 별도 read-only audit capability 없이 `confirmed_absent`를 저장하지 않는다. Fresh success finalization과 response-loss correlation은 [ADR-0030](../decisions/ADR-0030-atomic-cleanup-expiry-finalization.md), phase-preserving retry·hold disposition과 cleanup cursor claim은 [ADR-0031](../decisions/ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)에서 구현했다.

[ADR-0027](../decisions/ADR-0027-paired-read-only-cleanup-absence-attestation.md)은 8단계의 read-only audit와 persistence 경계를 구현한다. Grant는 destructive cleanup grant와 다른 concrete type이고 exact request·target/plan hash·receipt revision·fence·ledger revision·artifact·path·next phase에 결합된다. Paired GCS auditor만 private Ed25519 key를 소유하며 evidence는 concrete grant seal과 `ObservedAt`까지 서명한다. Firestore는 paired verifier와 current transaction으로 이를 다시 확인하고 exact attempt만 갱신한다. [EVD-20260722-035](../evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence)는 raw·manifest persistence, exact replay write-zero, wrong key/binding과 drift write-zero의 local/Emulator/clean CI 근거다.

[ADR-0028](../decisions/ADR-0028-progress-aware-expired-cleanup-takeover.md)은 progress가 있는 expired cleanup attempt의 인계 경계를 구현한다. Historical builder는 old receipt·attempt·immutable target binding만 재구성하고 provider 권한을 만들지 않는다. 각 phase의 마지막 persisted time에서 old ledger를 검증한 뒤 prior progress를 보존한 `failed/lease_expired` closure, receipt fence·revision·attempt count +1과 pristine new attempt create를 같은 transaction으로 commit한다. [EVD-20260722-036](../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover)은 all-phase local race, progress-preserving Emulator commit과 duplicate rollback의 근거다.

[ADR-0029](../decisions/ADR-0029-durable-artifact-phase-cleanup-execution.md)은 8단계의 dispatch→mutation outcome→signed absence 호출 순서를 local component로 구현한다. Dispatch transaction `applied` caller만 exact artifact grant를 받고 replay는 write 0·zero grant다. GCS executor는 counterpart와 auditor surface 없이 artifact 하나만 conditional delete하며, mutation 30초 상한과 5초 outcome persistence grace를 분리한다. Timeout·cancel·unavailable·deadline crossing·response-unverifiable은 durable `unknown`으로 남아 다음 audit와 manifest를 차단한다. [EVD-20260722-037](../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution)은 local race, Firestore Emulator, official Storage testbench와 clean CI 근거다. 성공해도 `ready_for_finalization`까지만 반환한다.

[ADR-0030](../decisions/ADR-0030-atomic-cleanup-expiry-finalization.md)은 9단계와 11단계의 success-only terminal transaction을 구현한다. Immutable cleanup fence deadline 안에서 attempt·receipt·두 index를 함께 commit하고 target write는 0이다. Commit 응답 유실 query는 completed time을 선결정하지 않고 original target·plan·fence와 pre/final revision만 봉인하며, terminal stored time으로 evidence와 purge를 다시 계산해 `committed|not_committed|unverifiable`만 반환한다. [EVD-20260722-038](../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation)은 local race와 Firestore Emulator 원자성·동시성·write-zero 근거다.

이 evidence는 원자적 Cloud Storage snapshot이 아니다. HTTP reader는 regular generation과 soft-deleted generation을 순차 조회하므로 두 호출 사이의 out-of-band write race가 남는다. Post-quiescence application writer fencing과 least-privilege IAM/write exclusion을 staging에서 검증하기 전에는 production finalization 근거로 사용하지 않는다.

Local R8c executor와 official testbench synthetic generation delete는 구현·검증했지만 executable에는 연결하지 않는다. 실제 staging/production artifact delete는 staging versioning·soft-delete·lifecycle·retention·IAM과 복구 절차를 승인·검증한 뒤에만 별도 활성화한다.

2026-07-23 현재 1~9b 단계의 reserved-origin success·failure control path와 11단계의 purge eligibility 설정까지 local component로 구현·검증했다. R8c는 current Firestore target/fence를 다시 승인하고 exact conditional delete, raw-first·missing-counterpart audit와 fail-closed error taxonomy를 제공하며 근거는 [EVD-20260722-033](../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit)에 기록한다. R8d foundation은 authoritative state를 다시 읽어 planned ledger와 dispatch/outcome phase를 저장하고 exact replay를 write 0으로 만든다. R8e는 paired signed evidence로 raw·manifest absence phase를 저장한다. R8f는 old progress를 보존하며 새 fence의 pristine attempt로 인계하지만 prior target·outcome·absence를 권한으로 상속하지 않는다. R8g는 dispatch-before-mutation, single-artifact grant, durable outcome과 raw-first signed audit를 합성하며 replay dispatch와 `unknown`에서 안전 정지한다. R8h는 exact success evidence를 attempt `completed/outcome=expired`, receipt `expired`와 같은 purge eligibility로 원자 확정하고 response loss를 read-only로 상관한다. R8i는 execution failure를 실제 phase/revision의 `cleanup_retry|cleanup_hold`로 닫고 old-fence-bounded retry claim과 hold 자동정지를 제공한다. R8j는 sealed intent로 R8h/R8i를 상호 배타적으로 선택하고 commit response loss를 bounded read-only correlation으로 닫는다. Operator hold release, held/accepted/rejected cleanup, nested purge job과 `cmd/server`·scheduler/startup/readiness는 미구현·미연결이다.

[ADR-0031](../decisions/ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)의 accepted R8i 계약과 local/Emulator 근거는 [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition)에 기록한다. Durable `unknown` error class, exhaustive policy, phase-preserving terminal, attempt+receipt 2문서 commit, exact response-loss correlation, retry boundary single winner와 hold auto-claim 0까지가 증명 범위다. Runtime composition·operator workflow·staging/production mutation은 이 완료 범위에 포함하지 않는다.

[ADR-0032](../decisions/ADR-0032-bounded-cleanup-terminal-orchestration.md)의 accepted R8j 계약과 local 근거는 [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration)에 기록한다. Sealed terminal intent, single terminal mutation, durable unknown class 복원, ambiguity barrier, cancellation-isolated correlation과 bounded result가 증명 범위다. Firestore terminal store의 cross-terminal 경쟁은 Emulator에서 확인했지만 `TerminalOrchestrator.Run` 전체를 실제 Firestore/GCS에 연결한 vertical slice, `cmd/server`·scheduler runtime, operator workflow와 staging/production mutation은 완료 범위가 아니다.

[ADR-0033](../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md)은 R8k-a job admission/fence, R8k-b nested attempt paging, R8k-c target·finding paging과 final linkage transaction을 분리한 `proposed` 계약이다. R8k-a pure contract, strict job codec, receipt fence와 job+receipt create-once Firestore admission은 commit `7a1d3ed`와 [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission)에서 local/Emulator 검증됐다. R8k-b/c child delete, inverse registry·backfill, final linkage transaction, Rules/index와 runtime은 아직 구현하지 않았다.

### R9. Staging과 운영 gate

- 별도 staging Firebase/GCP project
- Cloud Scheduler caller identity와 Cloud Run `run.invoker`
- gateway service account의 Firestore·Storage 최소권한
- audit 대상 prefix의 모든 writer identity 인벤토리와 out-of-band write exclusion. Regular·soft-deleted 두 listing 사이 race를 재현하고 IAM 차단 뒤에만 bounded absence를 production completion 후보로 승인
- bucket versioning, lifecycle, retention, soft-delete retention/restore window, KMS 실제 값과 `deleted` 판정 의미
- Firestore composite index와 TTL가 receipt/index/cleanup 근거를 먼저 지우지 않는지 확인
- application clock과 Firestore read time offset metric, 허용 skew 초과 fail-closed fault test
- consent/deadline acceptance는 `max(application UTC, transaction read time)`, 조기 cleanup 방지는 `min(...)`을 사용하는 경계 test
- crash injection과 alert sink
- operator hold 조회·승인·삭제 runbook
- 비용: candidate query/read/write, replay bytes, attempt ledger 월 예상량

## 5. Crash·artifact 상태 matrix

| crash 지점/상태 | 다음 worker가 가져야 할 사실 | 허용 행동 | 성공 기준 |
| --- | --- | --- | --- |
| receipt commit 전 | 문서 0 또는 transaction retry | request 재시도 | partial 3-way 문서 0 |
| receipt+lease 뒤 raw 전 | payload는 client에만 있음, next recovery는 lease expiry | client replay 대기 | sweeper 누락·artifact write 0 |
| raw write 성공 뒤 응답 유실 | raw exact generation | replay 검증 또는 raw-only forward | overwrite 0 |
| raw 뒤 manifest 전 crash | valid raw-only | manifest 생성 후 fenced stored | 같은 raw generation 유지 |
| manifest 뒤 finalizer 전 crash | valid complete | fenced finalizer | object/manifest generation 유지 |
| finalizer commit 뒤 응답 유실 | stored full lineage | identical replay | revision 불필요 증가 0 |
| lease takeover 뒤 old worker 재개 | old token | 모든 Firestore mutation 거절 | winner state 유지 |
| manifest-only | invalid ordering/data loss | hold | raw create 0, stored 전환 0 |
| stored-missing, artifact expiry 전 | exact generation NotFound | high-severity finding | 최신 generation fallback 0 |
| stored-missing, artifact expiry 후 | exact generation NotFound + lifecycle/deletion evidence | 정상 deletion workflow 대조 또는 integrity cleanup | 정상 lifecycle을 false data-loss로 경보 0 |
| deadline 뒤 partial artifact | cleanup_pending fence와 quiet period | 이후 pinned cleanup handoff | late recreate·forward write/finalize 0 |
| purge 대상 receipt에 attempt 다수 | terminal receipt와 bounded cursor | attempt/target 선삭제 후 마지막 3-way linkage 삭제 | orphan subcollection/target 0 |

## 6. 검증 명령과 환경

host Go가 없는 현재 WSL2에서는 Go 1.26.5 Docker image와 named module/build cache를 사용한다. 모든 local command는 저장소 `AGENTS.md`에 따라 `rtk` prefix를 사용한다.

검증 묶음:

- pure domain/table/property tests
- `go test -race ./...`, `go vet ./...`, `go mod tidy -diff`
- Firestore Emulator concurrent claim/takeover/stale finalizer
- R8i exhaustive policy·phase preservation, 2문서 atomic disposition과 target/index write-zero, commit-response-loss correlation, exact-boundary retry single winner·pristine attempt와 hold auto-claim 0
- pinned official Cloud Storage testbench generation read와 조건부 delete
- Firebase Rules server-only collection deny regression
- workspace check, document links, Android/iOS export
- container build와 `/readyz=503` fail-closed smoke
- GitHub clean runner

실제 staging 전에는 `generated`, clean CI 뒤에는 `verified`로 evidence 상태를 바꾸되 production·field 완료로 표현하지 않는다.

## 7. 문서·Git 전달 규칙

- 결정 변경: `ADR-0017`과 Target Domain Model
- 구현 상태·검증: 월별 EVD
- 사람이 읽을 기술 결과: 별도 HR
- runtime route·사용자 흐름 연결 전 Product Update 없음
- production·field 영향이 없으면 Incident 없음
- 구현 전 결정 문서 commit, 구현+local evidence commit, clean CI evidence 확정 commit으로 분리한다.
- commit마다 `Jaemani / leejaeman0227@gmail.com`, `main`, `origin`을 확인한다.
- 운영 분류·hold·cleanup 재개 절차는 [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)을 따른다.

## 8. 종료 조건

local gate 완료는 다음을 모두 만족할 때다.

- stale fence mutation 0
- active lease 중복 artifact call 0
- valid raw-only와 complete recovery 성공
- no-artifact의 추정 복구 0
- manifest-only/stored-missing의 자동 재생성·삭제 0
- consent invalid forward write 0
- cleanup 전 quiet period와 삭제 후 live-generation 재검사로 late orphan 0
- recovery attempt와 cleanup target을 먼저 비운 뒤 마지막 3-way linkage 삭제, orphan 0
- bounded sweeper가 poison candidate 뒤에도 진행
- recovery ledger privacy scan 위반 0
- Firestore Emulator와 official Storage testbench race test 통과
- clean CI 통과

staging/pilot gate는 별도이며 IAM·lifecycle·retention·Scheduler·실제 ADC·alert·operator runbook이 없으면 계속 차단한다.
