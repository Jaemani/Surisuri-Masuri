---
id: ADR-0026
title: Fenced cleanup execution ledger and atomic expiry finalization
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0026: cleanup 실행 원장과 expiry 최종화를 current attempt에 결합한다

## 맥락

[ADR-0025](./ADR-0025-generation-pinned-cleanup-delete-and-audit.md)는 persisted target과 current cleanup fence를 다시 승인한 뒤 exact generation만 raw-first로 삭제하고, regular·soft-deleted inventory가 모두 complete empty인지를 별도로 감사한다. 성공 반환값은 process 안에서만 유효한 관측 shape이며 receipt `expired`, attempt completion 또는 purge 권한이 아니다.

다음 실패 경계를 닫아야 한다.

- Delete RPC가 provider에서 처리된 직후 process가 종료될 수 있다.
- Complete-empty audit 뒤 Firestore write 응답이 유실될 수 있다.
- Raw 단계의 결과를 기록하지 못한 채 manifest 단계로 넘어가면 다음 worker가 안전한 재개 지점을 알 수 없다.
- Cleanup target은 create-once evidence라서 실행 상태를 in-place update하면 target hash와 replay 계약이 깨진다.
- Target은 receipt revision과 lease heartbeat·expiry까지 고정하므로 target 생성 뒤 lease renewal은 target을 즉시 stale하게 만든다.
- `expired`와 `purge_eligible_at`을 별도 write로 기록하면 receipt, attempt와 두 uniqueness index가 혼합 상태가 될 수 있다.

R8d의 목적은 provider I/O intent와 bounded outcome을 exact cleanup attempt에 단조적으로 기록하고, fresh current state와 durable completion evidence만으로 receipt를 원자적으로 `expired`로 전환하는 local control-plane 계약을 만드는 것이다.

## 결정 기준

- 불변 근거: Cleanup target의 schema, status, timestamps와 target hash는 생성 뒤 바꾸지 않는다.
- 응답 유실 안전성: RPC 전 intent와 RPC 후 audit을 구분하고, 불명확한 결과를 성공이나 404로 덮어쓰지 않는다.
- 최소권한: Execution progress, completion, retry·hold와 purge는 서로 다른 state transition이다.
- Fresh fencing: 모든 progress와 terminal write는 current receipt, exact attempt, target과 lease를 같은 transaction에서 재검증한다.
- 원자성: 성공 최종화는 receipt, attempt와 두 uniqueness index를 한 transaction에서 갱신한다.
- 정보 최소화: Attempt ledger에 좌표, body, object path, UID, App ID, credential 또는 provider 원문 오류를 넣지 않는다.
- 운영 격리: Local component 구현만으로 scheduler, startup, readiness 또는 실제 bucket delete를 활성화하지 않는다.

## 검토한 선택지

### 선택지 A: `ingestCleanupTargets`를 실행 상태 문서로 갱신

- 장점: Target 하나만 조회하면 계획과 실행 상태를 볼 수 있다.
- 단점: Create-once target과 mutable execution state가 섞이고 `updated_at == created_at`, canonical hash와 semantic replay write 0 계약이 깨진다.
- 판단: 제외한다.

### 선택지 B: 별도 top-level execution collection 추가

- 장점: Target과 attempt를 건드리지 않고 단계 원장을 만들 수 있다.
- 단점: Client Rules, orphan 방지, purge cursor와 linkage 집합이 하나 더 생긴다. Exact attempt가 이미 owner, fence와 lifecycle을 가진다.
- 판단: 추가 collection 없이 해결할 수 있으므로 제외한다.

### 선택지 C: Current cleanup attempt를 durable execution ledger로 확장

- 장점: Owner·token·worker version과 이미 결합되어 있고 attempt completion·takeover·purge 계약을 재사용한다. Target은 불변으로 남는다.
- 단점: Forward attempt와 cleanup attempt의 field invariants를 분리하고 progress-aware takeover validation을 추가해야 한다.
- 판단: 채택한다.

## 결정

### 1. Immutable target과 mutable execution ledger를 분리한다

`/ingestCleanupTargets/{attemptId}`는 앞으로도 create-only다. `planned|hold`, classification, inventory, pinned lineage와 target hash는 실행 결과로 갱신하지 않는다.

실행 상태는 exact cleanup attempt에만 기록한다.

```text
/ingestReceipts/{receiptId}/recoveryAttempts/{attemptId}

decision_domain=expiry_cleanup?
cleanup_schema_version=telemetry-cleanup-execution.v1?
cleanup_target_hash?
cleanup_plan_hash?
cleanup_receipt_revision?
cleanup_execution_revision?
cleanup_phase?

cleanup_raw_targeted?
cleanup_raw_dispatch_at?
cleanup_raw_delete_outcome?
cleanup_raw_outcome_recorded_at?
cleanup_raw_audit_outcome?
cleanup_raw_audited_at?

cleanup_manifest_targeted?
cleanup_manifest_dispatch_at?
cleanup_manifest_delete_outcome?
cleanup_manifest_outcome_recorded_at?
cleanup_manifest_audit_outcome?
cleanup_manifest_audited_at?

cleanup_disposition?(complete|retry|hold)
cleanup_error_class?
cleanup_evidence_hash?
```

이 field는 server-only bounded control metadata다. Artifact path와 digest 원문은 immutable target에만 남고 attempt에는 target·plan·evidence correlation hash만 기록한다.

Forward `started|completed|failed` attempt는 cleanup field가 모두 비어 있어야 한다. Cleanup attempt는 forward action, authorization disposition, raw·manifest lineage terminal field를 사용하지 않는다. 두 validator를 분리해 한 mode의 residue가 다른 mode에서 통과하지 못하게 한다.

### 2. 상태 전이는 dispatch intent와 absence audit을 분리한다

Cleanup execution phase는 다음 방향으로만 진행한다.

```text
planned
  -> raw_dispatch_recorded
  -> raw_outcome_recorded
  -> raw_absence_confirmed
  -> manifest_dispatch_recorded
  -> manifest_outcome_recorded
  -> manifest_absence_confirmed
  -> completed
```

- Target lineage가 없는 expected path도 `targeted=false`, delete outcome `not_attempted`로 기록하고 complete-empty audit을 수행한다.
- `*_dispatch_recorded`는 provider mutation 가능성이 시작됐다는 intent다. 이 상태에서 재개하면 delete를 바로 반복하지 않고 inventory audit을 먼저 수행한다.
- Delete outcome은 `deleted_observed | not_found_observed | not_attempted | unknown`이다. 이것만으로 absence가 아니다.
- `*_outcome_recorded`는 delete RPC가 반환한 bounded outcome만 영속한다. Audit이 실패해도 outcome write를 잃지 않으며 provider 원문 오류는 저장하지 않는다.
- Audit outcome `confirmed_absent`는 ADR-0025의 complete regular/soft-deleted empty inventory만 의미한다.
- Raw absence 전에는 manifest dispatch를 기록하거나 provider call을 실행할 수 없다.
- 같은 execution revision과 semantic command replay는 write 0이다. 다른 hash, 다른 outcome 또는 이전 phase로의 전이는 전체 write 0이다.
- 각 성공 write는 attempt의 `cleanup_execution_revision`만 정확히 1 증가시킨다. Receipt revision과 lease timestamps는 intermediate progress write로 바꾸지 않는다.

Raw 또는 manifest dispatch 뒤 provider 응답이 유실됐다면 outcome을 `unknown`으로 기록하고 기존 intent를 보존한다. Firestore outcome write 응답이 유실되면 exact ledger를 먼저 읽어 commit 여부를 판별한다. Not-committed인 경우 다음 audit에서 empty가 확인돼도 과거 delete outcome을 `not_found_observed`로 재작성하지 않고 `unknown`과 별도의 audit fact를 남긴다. Provider timeout·unavailable의 `unknown`과 error residue가 있는 attempt는 같은 attempt에서 다음 artifact로 진행하지 않고 retry disposition으로 닫는다. Firestore write 응답만 유실됐고 provider success가 이미 확정된 경우에는 exact outcome correlation 뒤 단조 phase를 재개할 수 있다.

### 3. 모든 progress write는 fresh current transaction을 요구한다

각 intermediate transaction은 다음을 함께 읽고 검증한다.

1. 두 uniqueness index와 authoritative `cleanup_pending` receipt
2. Exact current `started` cleanup attempt
3. Exact immutable target
4. Current receipt revision, cleanup owner, fencing token과 아직 만료되지 않은 lease
5. Target의 mode, origin, policy, revision, owner와 target hash
6. Persisted execution revision과 현재 phase

Capability 또는 command는 exact target hash, plan hash, receipt revision, fence, execution revision, expected phase와 next phase에 묶는다. Dispatch, outcome과 audit은 각각 독립 command다. Transaction read time과 application time을 보수적으로 결합하며 expiry 뒤 write는 0이다.

Non-authoritative `CleanupExecutionObservation`을 terminal receipt action에 직접 전달하지 않는다. Observation은 해당 phase command를 만드는 입력일 뿐이며, 최종화는 Firestore에 남은 durable ledger를 다시 읽는다.

`verified_empty/planned` target과 dispatch 응답 유실 재개에는 destructive delete grant를 사용하지 않는다. R8d는 concrete Firestore current-state read로 발급하는 별도 `cleanup-absence-audit` capability를 둔다. 이 capability는 exact target·plan hash, receipt revision, fence, expected raw/manifest path와 30초 이하 expiry를 묶고 inventory read만 허용한다. Delete backend에는 전달할 수 없다.

- `verified_empty`: Raw와 manifest를 각각 `targeted=false/not_attempted`로 원장에 기록하고 fresh complete-empty audit을 수행한다.
- `delete_candidate`의 `*_dispatch_recorded`: Mutation을 반복하기 전에 같은 read-only capability로 해당 expected path를 먼저 감사한다.
- Audit result는 complete regular/soft-deleted empty만 `confirmed_absent`로 만들며 여전히 terminal authority가 아니다.
- Classification 당시 empty였다는 sealed evidence만으로 completion하지 않고 current fence 아래 새 audit을 반드시 수행한다.

따라서 R8c destructive execution grant와 R8d absence-audit grant는 서로 교환되지 않는다. Phase command authorizer는 R8c observation 또는 audit-only result를 exact persisted target·current ledger phase와 결합하지만, finalizer는 어느 in-process result도 직접 신뢰하지 않는다.

### 4. Target 생성 뒤 cleanup lease renewal은 금지한다

Target은 receipt revision, lease heartbeat와 lease expiry를 hash에 고정한다. 따라서 동일 target을 유지한 채 lease를 renew하는 계약은 허용하지 않는다.

- Target 생성 전: 별도 cleanup renewal contract가 exact started attempt와 current fence를 확인한 경우만 허용할 수 있다.
- Target 생성 후: Renewal write 0이다.
- Target create 전에는 전체 실행과 finalization을 마칠 수 있는 명시적 최소 잔여 lease budget을 검사한다.
- Budget 부족이나 transient failure를 lease expiry 전에 분류할 수 있으면 현재 attempt를 bounded retry·hold disposition으로 닫고 lease를 clear한다.
- Lease가 이미 만료됐으면 old owner는 더 쓰지 않고 아래 progress-aware takeover가 prior attempt를 disposition 없이 `failed/lease_expired`로 닫는다. 다음 claim은 token·revision을 증가시키고 새 attempt와 새 immutable target으로 audit-first 재개한다.

Generic forward `RenewLease`와 `ReleaseLease`를 `cleanup_pending`으로 넓히지 않는다. Cleanup retry와 hold는 attempt terminal state, lease clear와 다음 control cursor를 한 transaction에서 기록하는 별도 계약이다.

Target 생성 뒤 process crash로 lease가 먼저 만료된 경우에는 old owner가 expiry 뒤 disposition write를 시도하지 않는다. 다음 cleanup claim transaction이 exact prior target과 progress-bearing attempt를 함께 읽어 다음 shape만 허용한다.

- Attempt status는 `started`, owner/token/version/started time은 expired receipt lease와 exact match다.
- `decision_domain`은 비어 있고 execution field도 전부 비어 있거나, `expiry_cleanup`이고 target·plan hash와 execution revision·phase가 단조 validator를 통과한다.
- `cleanup_disposition`, terminal outcome, completion time과 non-cleanup terminal field는 비어 있다.
- Existing target은 prior attempt ID path에 있고 attempt의 target hash와 canonical hash가 일치한다.

검증에 성공하면 prior attempt를 `failed/lease_expired`로 닫되 이미 기록된 bounded cleanup progress는 보존하고, 새 token·revision·attempt를 같은 transaction에서 만든다. 이 cleanup-specific failed shape는 `decision_domain=expiry_cleanup`과 검증된 progress를 허용하지만 disposition은 허용하지 않는다. Missing/conflicting target, invalid phase·revision, terminal residue 또는 hash mismatch는 takeover와 receipt write를 모두 0으로 만든다. 새 attempt는 prior outcome을 authority로 상속하지 않고 새 classification·target에서 audit-first로 시작한다.

### 5. 성공 최종화는 durable ledger만으로 결정한다

`manifest_absence_confirmed` 뒤 finalizer는 새로운 transaction에서 receipt, exact attempt, immutable target과 두 index를 다시 읽는다. 다음을 모두 만족해야 한다.

- Receipt가 target에 고정된 revision의 `cleanup_pending`이다.
- Owner kind는 cleanup이고 attempt ID, token, lease expiry가 exact match이며 lease가 살아 있다.
- Attempt가 `started`, `decision_domain=expiry_cleanup`이고 target·plan hash가 일치한다.
- Raw와 manifest phase가 단조적으로 끝났고 두 audit outcome이 `confirmed_absent`다.
- 두 audit time이 completion evidence freshness 상한 안에 있고 finalization time과 lease expiry보다 앞선다.
- Target은 `planned`이고 `delete_candidate` 또는 `verified_empty`이며 `hold`가 아니다.
- Late generation, soft-deleted generation, incomplete inventory 또는 bounded error residue가 없다.
- Receipt와 두 index의 linkage와 purge field가 동일하고 아직 purge eligibility가 없다.

성공 transaction은 다음을 함께 commit한다.

1. Attempt `started -> completed`
2. `cleanup_phase=completed`, `cleanup_execution_revision` 정확히 1 증가
3. `decision_domain=expiry_cleanup`, `outcome=expired`, `cleanup_disposition=complete`, evidence hash와 completion time 기록
4. Receipt `cleanup_pending -> expired`
5. Cleanup lease field clear와 receipt revision 정확히 1 증가
6. Receipt와 두 uniqueness index에 같은 `purge_eligible_at` 기록

Target write는 0이다. 하나라도 어긋나면 네 control document 모두 write 0이다.

`purge_eligible_at`은 다음 pure policy로 계산한다.

```text
max(receipt_retention_floor, completed_at + CleanupCompletionAuditWindow)
```

Local 정책의 `CleanupCompletionAuditWindow`는 코드와 문서에서 명시적으로 versioning하며 staging 보존 승인 전 production 값으로 주장하지 않는다.

### 6. Response loss는 exact outcome read로 상관한다

Commit 호출이 timeout, cancellation 또는 unavailable로 끝나면 provider mutation을 다시 실행하지 않는다. Exact correlation query가 receipt, attempt, target과 두 index를 read-only transaction에서 읽어 다음 중 하나를 반환한다.

- `committed`: Exact attempt가 completed/expiry-cleanup이고 receipt가 expired이며 revision, target·plan·evidence hash와 세 purge eligibility가 모두 일치한다.
- `not_committed`: Exact original receipt revision과 started attempt가 그대로이며 current fence와 ledger phase가 finalization 전 상태다.
- `unverifiable`: 다른 winner, malformed linkage, conflicting terminal residue, missing evidence 또는 read failure다.

`committed`는 재실행 없이 성공으로 수렴한다. `not_committed`만 fresh authorization과 audit-first 재개가 가능하다. `unverifiable`에서는 delete, expired finalization과 purge가 모두 0이다.

### 7. Retry·hold와 purge 실행은 후속 하위 단계로 분리한다

구현 상태는 하위 gate로 분리한다. R8d는 success-path progress foundation을, R8e [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md)은 signed absence persistence를, R8f [ADR-0028](./ADR-0028-progress-aware-expired-cleanup-takeover.md)은 progress-aware expired takeover를 local component로 닫았다. Atomic expiry finalization, purge eligibility, outcome correlation과 다음 bounded taxonomy는 같은 attempt ledger를 사용하되 별도 구현 gate로 둔다.

- Retry 후보: timeout, cancellation, provider unavailable, quota, incomplete/truncated inventory
- Hold 후보: precondition drift, lineage mismatch, late/soft-deleted generation, permission·retention policy conflict
- Direct 404: complete-empty audit가 있으면 성공 후보, 없으면 retry

Policy가 결정한 retry·hold는 attempt를 `completed`로 닫고 `decision_domain=expiry_cleanup`, exact target·plan hash, `cleanup_disposition=retry|hold`, bounded error class와 completion time을 기록한다. Receipt는 `cleanup_pending`을 유지하되 lease clear와 `next_cleanup_at` 또는 hold/finding을 같은 transaction에 기록한다. `failed`는 crash·lease expiry처럼 disposition이 확정되지 않은 execution failure만 의미하며, cleanup-specific `failed/lease_expired`는 검증된 progress를 보존할 수 있어도 disposition을 가지지 않는다. 자동 우회는 금지한다. Purge job은 eligibility 설정과 별개다. Nested attempt, target과 finding을 bounded cursor로 먼저 제거하고 empty를 재검증한 후에만 두 index와 receipt를 마지막 transaction에서 삭제한다.

### 8. 실행·운영 활성화는 계속 닫아 둔다

R8d local implementation은 scheduler, startup, readiness와 runtime route에 연결하지 않는다. 실제 staging/production delete와 `expired` 운영 전환은 다음을 별도로 검증해야 한다.

- Bucket versioning, soft-delete, lifecycle, retention, KMS와 IAM 실제 정책
- Firestore index와 transaction latency
- Commit response-loss와 crash injection drill
- Operator hold/retry와 restore 절차
- Audit window와 purge retention 승인

## 결과와 위험

- Immutable target은 계획·계보 증거로 유지되고 attempt가 mutable execution ledger가 된다.
- Provider delete outcome과 version-aware absence fact를 혼동하지 않는다.
- Response loss 뒤 receipt를 읽지 않고 destructive call을 반복하는 경로가 차단된다.
- Progress field가 있는 cleanup attempt를 forward validator가 받아들이지 않도록 mode-specific validation이 필수다.
- Target 생성 뒤 renewal을 막으므로 실행 전 lease budget 계산과 빠른 bounded finalization이 필요하다.
- Retry·hold와 purge worker가 구현되기 전에는 success 외 실패가 자동 운영 상태로 수렴하지 않는다.

## 연결 문서

- 선행 결정: [ADR-0023](./ADR-0023-fenced-cleanup-lease-claim.md), [ADR-0024](./ADR-0024-immutable-cleanup-dry-run-target.md), [ADR-0025](./ADR-0025-generation-pinned-cleanup-delete-and-audit.md)
- 후속 결정: [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md) — paired signed read-only audit와 raw·manifest absence persistence를 구현했다. Phase executor·terminal finalizer는 계속 후속 범위다.
- 후속 결정: [ADR-0028](./ADR-0028-progress-aware-expired-cleanup-takeover.md) — historical/live authority를 분리하고 prior progress 보존 closure와 pristine new attempt의 원자 인계를 구현했다. Provider 권한 상속·runtime 연결은 없다.
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 증거: [EVD-20260722-034](../evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence) — pure ledger와 fresh non-audit Firestore progress persistence의 local/Emulator 근거. 해당 증거 시점에는 absence audit·takeover·terminal finalizer·correlation이 미구현이었다.
- 후속 증거: [EVD-20260722-035](../evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence) — signed absence persistence까지의 local/Emulator 근거. GCS sequential listing non-atomic residual과 staging IAM/write-exclusion gate를 유지
- 후속 증거: [EVD-20260722-036](../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover) — all-phase historical validation, progress-preserving closure와 pristine attempt 원자 인계의 local/Emulator 근거
- 제품 업데이트: 해당 없음 — executable·사용자·운영 경로 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
