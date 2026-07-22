---
id: ADR-0031
title: Phase-preserving cleanup retry and hold disposition
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0031: Cleanup 실패는 phase를 보존한 retry·hold 원장과 전용 cursor로 닫는다

## 맥락

[ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md)은 provider mutation 전 dispatch를 기록하고 ambiguous delete를 `unknown`으로 보존하지만, `CleanupArtifactExecutionResult.ErrorClass`는 아직 in-process 결과에만 남는다. Process가 끝나면 같은 `unknown`이 timeout인지 response-unverifiable인지 durable state만으로 구분할 수 없다.

[ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md)은 두 artifact의 signed absence가 모두 있는 success만 원자적으로 `expired`로 닫는다. Timeout, quota, incomplete inventory, permission, generation drift와 lineage mismatch는 같은 success finalizer에 넣을 수 없다.

기존 ledger에는 `retry|hold` enum과 bounded error class가 선언돼 있지만 단순히 phase를 `completed/revision 8`로 바꾸면 실제로 수행하지 않은 audit 단계를 건너뛴 것처럼 기록된다. 또한 lease를 조기 제거한 직후 새 cleanup claim을 허용하면, 이미 발급된 old provider capability가 immutable old fence 만료까지 살아 있는 동안 새 fence가 겹칠 수 있다.

따라서 execution-stage 실패는 실제 마지막 phase와 revision을 보존한 terminal disposition으로 닫고, receipt에는 forward recovery와 분리된 cleanup 전용 retry·hold cursor를 기록해야 한다.

## 결정 기준

- Durable cause: `unknown`과 그 bounded error class를 같은 durable outcome revision에 보존한다.
- Honest history: Retry·hold는 실제 마지막 phase와 revision을 success phase로 꾸미지 않는다.
- No capability overlap: 새 claim은 old immutable fence가 만료되기 전에는 절대 시작하지 않는다.
- Explicit ownership: Lease를 지운 뒤에도 어떤 terminal attempt가 현재 retry·hold cursor를 만들었는지 receipt에서 추적한다.
- Forward isolation: `next_recovery_at`, `hold_reason` 같은 forward-recovery cursor를 cleanup 의미로 재사용하지 않는다.
- Atomic control: Attempt terminalization과 receipt cursor는 함께 commit 또는 rollback한다.
- Response-loss safety: Commit 결과가 불명확하면 mutation을 반복하지 않고 exact read-only correlation으로 판별한다.
- Immutable evidence: Cleanup target과 두 uniqueness index는 retry·hold commit에서 write 0이다.
- Bounded disclosure: Provider 원문, path, UID, credential, payload와 위치는 disposition surface에 넣지 않는다.
- Runtime isolation: Phase executor composition, scheduler, startup과 staging·production mutation은 별도 gate 전까지 닫는다.

## 검토한 선택지

### 선택지 A: Forward recovery의 `next_recovery_at`과 `hold_reason` 재사용

- 장점: Firestore field를 추가하지 않아도 된다.
- 단점: Forward `reserved|recovery_hold`와 cleanup `cleanup_pending`의 validator·claim 의미가 섞이고, 어떤 cleanup attempt가 cursor를 만들었는지 보존하지 못한다.
- 판단: 제외한다.

### 선택지 B: Retry·hold도 `completed/revision 8`로 기록

- 장점: Terminal phase가 하나라 조회가 단순하다.
- 단점: Raw outcome 단계의 timeout도 manifest absence까지 완료한 것처럼 보이며 success evidence와 실패 evidence를 구분하기 어렵다.
- 판단: 제외한다.

### 선택지 C: Phase-preserving attempt terminal과 cleanup 전용 receipt cursor

- 장점: 실제 progress를 보존하고 old capability overlap을 cursor로 차단하며 새 attempt가 prior target·outcome을 권한으로 상속하지 않게 할 수 있다.
- 단점: Receipt schema, claim validator, outcome correlation과 Emulator race test가 함께 필요하다.
- 판단: 채택한다.

## 결정

### 1. ErrorClass는 artifact outcome 전용 경로에서 durable하게 저장한다

`DeleteOutcome=unknown`이면 해당 raw 또는 manifest outcome revision에 bounded `cleanup_error_class`가 반드시 있어야 한다. 허용 조합은 mutation 결과가 ambiguous한 다음 class로 제한한다.

- `provider_timeout`
- `provider_cancelled`
- `provider_unavailable`
- `response_unverifiable`

Known `not_attempted|deleted_observed|not_found_observed`에는 error class residue가 없어야 한다. Exact replay는 outcome과 error class가 모두 같을 때만 write 0 성공이고, outcome은 같지만 class가 다르면 conflict다. Generic progress command에는 임의 provider error 입력을 열지 않고 artifact outcome grant에 결합된 persistence path만 이 field를 쓴다.

### 2. Bounded failure policy를 exhaustively 고정한다

Execution-stage error class는 다음 disposition으로만 매핑한다.

| Error class | Disposition | 기본 cursor |
| --- | --- | --- |
| provider_timeout | retry | 15분 backoff |
| provider_cancelled | retry | 15분 backoff |
| provider_unavailable | retry | 15분 backoff |
| response_unverifiable | retry | 15분 backoff |
| quota_limited | retry | 60분 backoff |
| inventory_incomplete | retry | 30분 backoff |
| permission_denied | hold | 24시간 내 사람 검토 |
| precondition_drift | hold | 24시간 내 사람 검토 |
| generation_drift | hold | 24시간 내 사람 검토 |
| lineage_mismatch | hold | 24시간 내 사람 검토 |

Unknown enum, unbounded Go error와 내부 invariant failure는 disposition으로 축소하지 않고 unavailable/fail-closed한다. Direct 404는 signed complete-empty audit 없이 success나 retry·hold로 자동 해석하지 않는다.

### 3. Retry·hold는 실제 phase와 revision을 보존한다

Attempt는 다음 shape로 terminal이 된다.

```text
status=completed
decision_domain=expiry_cleanup
outcome=cleanup_retry | cleanup_hold
cleanup_phase=<실제 마지막 phase>
cleanup_execution_revision=<해당 phase revision>
cleanup_disposition=retry | hold
cleanup_error_class=<bounded exact class>
cleanup_evidence_hash=<target+plan+fence+phase+progress+disposition+error+completed_at binding>
completed_at=<trusted transaction time>
```

`completed/revision 8 + disposition=complete`는 두 signed absence가 모두 있는 success 전용으로 유지한다. `failed/lease_expired`는 policy disposition이 정해지지 않은 crash·lease expiry 전용이며 retry·hold field를 갖지 않는다.

### 4. Receipt에는 cleanup 전용 control cursor를 둔다

`cleanup_pending` receipt에 다음 server-only optional field를 추가한다.

```text
cleanup_disposition_attempt_id?
cleanup_control_disposition?(retry|hold)
last_cleanup_error_class?
next_cleanup_at?                 # retry only
cleanup_hold_review_due_at?      # hold only
```

- Retry는 exact terminal attempt ID, `retry`, error class와 `next_cleanup_at`을 갖고 hold due는 비어 있다.
- Hold는 exact terminal attempt ID, `hold`, error class와 `cleanup_hold_review_due_at`을 갖고 next cleanup은 비어 있다.
- Baseline transition 또는 active cleanup lease에는 이 다섯 field가 모두 비어 있어야 한다.
- Receipt state는 계속 `cleanup_pending`이다. 두 uniqueness index와 purge field는 바꾸지 않는다.

### 5. Retry cursor는 old provider capability 만료를 포함한다

Retry 시각은 다음보다 이를 수 없다.

```text
next_cleanup_at = max(old_fence.expires_at, completed_at + error_class_backoff)
```

따라서 lease field를 commit에서 제거해도 old capability와 new fence가 겹치지 않는다. Hold release도 old fence expiry 전에는 새 claim을 만들 수 없으며, 별도 operator release 계약이 생기기 전에는 review due가 지났다는 이유만으로 자동 claim하지 않는다.

### 6. Disposition commit은 attempt와 receipt만 원자적으로 갱신한다

Fresh transaction은 두 uniqueness index, receipt, exact started attempt와 immutable target을 모두 읽고 target·plan·receipt revision·fence·ledger phase/revision을 다시 검증한다. 성공 write set은 두 문서뿐이다.

1. Exact cleanup attempt를 위 phase-preserving terminal shape로 갱신
2. Receipt revision `+1`, lease clear와 exact cleanup cursor 기록

Target, 두 uniqueness index와 purge field는 write 0이다. Disposition은 lease의 exclusive expiry 전에만 commit할 수 있다. Stale outcome/audit, takeover 또는 success finalizer와 경쟁하면 exact pre-state를 먼저 commit한 한 transaction만 이긴다.

### 7. Response loss는 별도 read-only correlation으로 닫는다

Correlation query는 completed time을 선결정하지 않고 exact pre-state를 봉인한다.

- Tenant, reservation key와 attempt ID
- Target hash, plan hash와 receipt revision
- Original fence, ledger phase와 revision
- Expected disposition과 error class
- Expected receipt revision `+1`

Fresh read는 receipt, attempt, target과 두 index를 읽고 다음만 반환한다.

- `committed`: Terminal attempt evidence와 receipt cursor가 stored `completed_at`에서 재계산한 policy와 exact match다.
- `not_committed`: Original active receipt·started attempt·ledger가 그대로다.
- `unverifiable`: 모든 문서는 구조적으로 읽혔지만 다른 winner 또는 partial/semantic mismatch다.

Missing attempt·target·linkage, 구조 해석 실패와 authorization/read failure는 `unavailable` 오류다. 어떤 outcome도 provider mutation이나 새 claim 권한을 주지 않는다.

### 8. Retry claim은 terminal attempt를 다시 읽는다

`next_cleanup_at` 도달 뒤 claim transaction은 receipt에 연결된 `cleanup_disposition_attempt_id` 문서를 읽고 다음을 확인한다.

- Attempt가 exact `completed/cleanup_retry`이고 receipt의 disposition·error class와 일치
- Attempt ledger와 immutable old target의 target·plan·fence·phase/revision·evidence가 유효
- Effective claim time이 `next_cleanup_at`과 old fence expiry 이상
- Hold cursor, purge eligibility와 active lease가 없음

검증 후에만 receipt token·revision·attempt count를 증가시키고 cleanup cursor를 clear하며 pristine new attempt를 원자 생성한다. Old target, delete outcome과 absence evidence는 새 provider 권한으로 상속하지 않는다. Hold는 review due가 지나도 자동 claim 0이다.

### 9. 첫 구현 범위를 execution-stage planned target으로 제한한다

이번 gate는 immutable `planned` target과 execution ledger가 이미 있는 reservation-expiry cleanup만 다룬다. `CleanupTargetStatusHold` 또는 classification unavailable은 execution plan이 없으므로 별도 targetless/classification-stage disposition 계약으로 후속 분리한다.

Phase executor가 typed error를 이 계약에 연결하는 composition, operator hold release, accepted·rejected-origin cleanup, nested purge, scheduler·startup·readiness, staging·production Firebase/GCS와 actual object delete는 이 결정만으로 활성화하지 않는다.

## 결과와 위험

- Process restart 뒤에도 ambiguous mutation의 bounded 원인을 구분할 수 있다.
- Retry·hold가 success phase를 가장하지 않고 마지막 durable progress를 보존한다.
- Lease 조기 clear와 새 claim 사이에서 old/new provider capability overlap을 막는다.
- Receipt cursor가 terminal attempt를 직접 가리켜 claim 시 원장을 재검증할 수 있다.
- Hold는 자동 재실행되지 않으며 별도 사람 승인 계약 전까지 정지한다.
- Typed error를 disposition class로 바꾸는 orchestration이 잘못되면 잘못된 retry·hold가 될 수 있으므로 exhaustive mapping과 unknown-error fail-closed test가 필요하다.
- Firestore transaction read 수가 늘지만 실제 runtime 활성화 전에 Emulator contention·비용을 별도 검증한다.

## 연결 문서

- 선행 결정: [ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md), [ADR-0028](./ADR-0028-progress-aware-expired-cleanup-takeover.md), [ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md), [ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 구현 증거: [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition)
- 제품 업데이트: [UPD-20260723-05](../product-updates/UPD-20260723-05-cleanup-retry-hold-control.md)
- 사람 대상 리포트: [HR-20260723-30](../reports/human/HR-20260723-30-cleanup-retry-hold-disposition.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
