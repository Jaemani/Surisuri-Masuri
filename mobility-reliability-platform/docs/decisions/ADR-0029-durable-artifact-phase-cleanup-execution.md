---
id: ADR-0029
title: Durable artifact-phase cleanup execution
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0029: Cleanup delete를 artifact별 durable phase로 실행한다

## 맥락

[ADR-0025](./ADR-0025-generation-pinned-cleanup-delete-and-audit.md)의 초기 executor는 raw delete, raw absence audit, manifest delete와 manifest audit을 한 호출에서 수행했다. [ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)의 durable ledger와 [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md)의 signed absence persistence가 생긴 뒤에도 이 whole-plan 호출을 그대로 사용하면 다음 문제가 남는다.

- 기존 delete grant는 exact ledger phase·revision과 artifact 하나에 결합되지 않는다.
- Dispatch를 durable하게 기록하기 전에 provider mutation이 시작될 수 있다.
- Concurrent 또는 replay caller가 같은 artifact mutation을 다시 호출할 수 있다.
- Delete RPC timeout·cancel·unavailable처럼 mutation 여부가 모호한 결과가 상위 zero result로 유실될 수 있다.
- Raw outcome이 모호한데도 같은 호출이 manifest delete 또는 absence audit로 전진할 surface를 가진다.
- Mutation deadline에서 provider가 반환되면 결과 persistence도 같은 deadline에 막혀 가장 중요한 `unknown`을 기록하지 못한다.

따라서 cleanup 실행은 한 번의 delete plan이 아니라 **한 artifact의 dispatch, mutation outcome, signed absence를 각각 durable phase로 연결하는 protocol**이어야 한다.

## 결정 기준

- Durable-before-destructive: Provider mutation 전에 exact dispatch phase가 Firestore에 commit돼야 한다.
- Single winner: 같은 phase 경쟁자 중 transaction `applied` winner 한 명만 provider capability를 받는다.
- One artifact surface: 한 capability와 executor는 raw 또는 manifest 하나만 다룬다.
- Ambiguity preservation: Mutation 여부가 모호하면 bounded `unknown`을 durable하게 남긴다.
- Raw-first barrier: Durable raw absence 전에는 manifest dispatch가 0이어야 한다.
- Evidence separation: Delete response를 absence evidence로 해석하지 않고 기존 signed auditor만 사용한다.
- Time integrity: Mutation 시작과 provider 완료 시각을 실제 trusted clock에서 보존하며 backdating하지 않는다.
- Runtime isolation: Terminal finalizer와 response-loss correlation 전에는 startup·scheduler·readiness에 연결하지 않는다.

## 검토한 선택지

### 선택지 A: 기존 whole-plan executor 결과를 마지막에 한 번 저장

- 장점: 호출 수와 orchestration 코드가 적다.
- 단점: 중간 crash와 response loss에서 어느 artifact가 전송됐는지 구분할 수 없고 raw ambiguity 뒤 manifest mutation을 구조적으로 차단하기 어렵다.
- 판단: 제외한다.

### 선택지 B: Ledger에 lock flag만 기록하고 기존 grant를 재사용

- 장점: 기존 executor를 거의 유지할 수 있다.
- 단점: Grant가 target·plan·receipt revision·fence·artifact·phase revision을 exact하게 묶지 않아 replay와 stale caller의 provider surface가 남는다.
- 판단: 제외한다.

### 선택지 C: Artifact별 dispatch grant, single-artifact executor와 durable outcome을 순차 합성

- 장점: Firestore transaction winner만 destructive surface를 받고 `unknown`이 다음 phase를 막는다. Signed audit와 mutation도 concrete type으로 분리된다.
- 단점: Phase 수와 transaction/read 호출이 늘고 terminal finalizer 전에는 중간 durable 상태가 더 많이 보인다.
- 판단: 채택한다.

## 결정

### 1. Request는 exact pre-dispatch ledger revision에 결합한다

`CleanupArtifactExecutionRequest`는 다음 값을 canonical request hash에 포함한다.

- Tenant, reservation key와 cleanup attempt ID
- Immutable target hash와 cleanup plan hash
- Receipt revision과 current cleanup owner/token/lease fence
- Expected pre-dispatch ledger revision
- Dispatch revision·phase와 outcome phase
- `raw|manifest` artifact 하나
- Exact expected path, targeted 여부와 generation·metageneration lineage

Raw는 `planned/revision 1 -> raw_dispatch_recorded/revision 2`, manifest는 durable `raw_absence_confirmed/revision 4 -> manifest_dispatch_recorded/revision 5`에서만 request를 만들 수 있다. Raw delete outcome이 `unknown`이면 manifest request도 만들 수 없다.

### 2. Dispatch commit과 winner grant 발급을 하나의 transaction 결과로 묶는다

`BeginCleanupArtifactExecution`은 authoritative receipt, exact started attempt, immutable target과 current ledger를 다시 읽는다. Expected revision이면 dispatch phase를 attempt에 기록하고 `applied`를 반환한다. 이미 exact dispatch가 있으면 write 0의 `replayed`를 반환한다.

- `applied`: Exact artifact capability가 non-zero다.
- `replayed`: Capability는 zero이며 provider call이 0이다.
- Conflict·stale fence·malformed ledger·insufficient lease window: Dispatch write와 provider call이 모두 0이다.

Grant를 caller가 받기 전 process가 종료돼 durable dispatch에 머무를 수 있다. 같은 fence에서 mutation을 추측해 반복하지 않고 [ADR-0028](./ADR-0028-progress-aware-expired-cleanup-takeover.md)의 expiry takeover가 progress를 보존한 채 새 pristine attempt로 복구한다.

### 3. Mutation deadline과 outcome persistence deadline을 분리한다

Capability에는 두 exclusive deadline을 둔다.

```text
checked_at
  < mutation_expires_at
  < outcome_expires_at
  <= lease_expires_at

outcome_expires_at - mutation_expires_at = 5 seconds
```

Mutation window의 기본 상한은 30초다. Lease가 더 이르면 5초 outcome grace를 먼저 확보하고 그 앞까지 mutation window를 줄인다. Positive mutation window와 전체 grace를 확보할 수 없으면 dispatch를 시작하지 않는다.

Delete를 실제 호출하기 직전에 trusted `MutationStartedAt`을 캡처하고 mutation deadline 전인지 검증한다. Provider 반환 직후 `ObservedAt`을 정확히 한 번 캡처해 boundary 판정과 result에 같은 값을 사용한다. 따라서 deadline 직전 확정 응답이 시각 재조회 때문에 zero result로 유실되지 않는다.

Outcome authorization은 다음을 따로 확인한다.

- Mutation을 시도한 result는 `MutationStartedAt < mutation_expires_at`이다.
- `ObservedAt < outcome_expires_at`이다.
- `ObservedAt >= MutationStartedAt`이다.
- 두 시각은 모두 exact cleanup lease fence 안이다.
- Firestore application·transaction effective time도 `outcome_expires_at` 전이다.

Caller result 시각만 grace 안이라고 해서 늦은 replay를 저장하지 않는다. Exact outcome expiry에는 write 0이다.

### 4. GCS executor는 artifact 하나만 mutation한다

`CleanupSingleArtifactExecutor`는 request가 지정한 path 하나에만 다음 순서로 접근한다.

1. Exact path regular/soft-deleted inventory를 bounded complete coverage로 읽는다.
2. Target generation이 live한 경우 exact generation을 inspect하고 lineage를 비교한다.
3. Exact generation+metageneration conditional delete를 한 번 호출한다.

Counterpart artifact와 absence auditor port를 받지 않는다. Targeted artifact가 없거나 generation이 이미 absent면 `not_attempted`, delete success는 `observed`, direct generation 404는 `not_found_observed`다. 이 세 결과 어느 것도 자체 absence proof가 아니다.

Delete 호출 뒤 timeout, cancellation, unavailable, authorization deadline crossing과 `ErrArtifactResponseUnverifiable`은 zero result가 아니라 다음 bounded result와 원 오류를 함께 반환한다.

```text
delete_outcome = unknown
error_class = provider_timeout | provider_cancelled |
              provider_unavailable | response_unverifiable
```

Provider 원문 메시지, object path, credential, UID/App ID, payload와 좌표는 result에 넣지 않는다.

### 5. Outcome은 exact dispatch 다음 durable revision에 저장한다

`RecordCleanupArtifactExecutionOutcome`은 result authorization과 current Firestore effective time을 outcome window에서 다시 확인한다. Current target·plan·receipt revision·fence와 exact dispatch phase가 일치할 때만 outcome revision을 기록한다.

Semantic exact replay는 write 0이다. 다른 outcome, later phase, stale fence·revision 또는 grace expiry는 write 0이다. 현재 bounded `ErrorClass`는 process result와 human/evidence report에만 남고 ledger는 `unknown` outcome으로 안전 정지한다. Restart 뒤 상세 원인 보존은 terminal disposition 설계에서 별도로 결정한다.

### 6. Known outcome만 signed audit로 전진한다

Phase executor 순서는 고정한다.

```text
initialize ledger
  -> begin raw dispatch
  -> execute raw artifact
  -> persist raw outcome
  -> signed raw absence audit
  -> begin manifest dispatch
  -> execute manifest artifact
  -> persist manifest outcome
  -> signed manifest absence audit
  -> ready_for_finalization
```

`unknown` outcome이면 signed audit와 counterpart artifact 호출은 모두 0이다. Existing durable dispatch를 본 replay caller도 mutation을 추측하지 않고 `dispatch_pending`으로 정지한다. Raw·manifest absence는 [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md)의 paired Ed25519 evidence와 dedicated Firestore path만 저장한다.

### 7. Terminal authority와 runtime은 계속 분리한다

Phase executor의 성공 종점은 `manifest_absence_confirmed/revision 7`과 `ready_for_finalization`이다. 다음은 이 결정의 구현 범위가 아니다.

- Attempt `completed`, receipt `expired`와 세 control document `purge_eligible_at`의 atomic finalizer
- Finalizer commit response-loss `committed|not_committed|unverifiable` correlation
- `unknown`, quota, drift와 policy 오류의 retry·hold disposition persistence
- Nested attempt·target·finding과 receipt/index purge
- Scheduler, startup, readiness, HTTP route와 staging/production GCS delete

## 결과와 위험

- Provider mutation 전에 exact dispatch가 durable해지고 replay caller는 zero grant를 받는다.
- Raw `unknown`과 response-unverifiable이 durable barrier가 되어 audit·manifest로 전진하지 않는다.
- Mutation deadline을 넘긴 ambiguous response도 5초 outcome grace 안에서 보존할 수 있다.
- Confirmed provider 응답과 result가 같은 completion timestamp를 사용해 deadline-edge 유실을 막는다.
- Read-only `not_attempted` 경계는 provider mutation이 없지만 deadline 직후 dispatch-pending liveness가 생길 수 있다. Expiry takeover가 복구하며 후속 최적화 대상으로 둔다.
- Begin은 full outcome grace와 positive mutation window를 요구하지만 post-commit 최소 provider 실행 잔여시간까지 보장하지 않는다. Staging latency 측정 전 runtime 활성화를 금지한다.
- Durable ledger는 현재 bounded error class를 저장하지 않으므로 restart 뒤 `unknown` 원인의 세부 분류가 사라진다. Safety barrier는 유지되며 retry·hold ADR에서 저장 여부를 확정한다.
- Regular/soft-deleted listing은 여전히 sequential observation이다. Staging IAM writer exclusion 전에는 atomic absence proof가 아니다.

## 연결 문서

- 선행 결정: [ADR-0025](./ADR-0025-generation-pinned-cleanup-delete-and-audit.md), [ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md), [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md), [ADR-0028](./ADR-0028-progress-aware-expired-cleanup-takeover.md)
- 증거: [EVD-20260722-037](../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution)
- 사람 대상 리포트: [HR-20260722-28](../reports/human/HR-20260722-28-durable-cleanup-phase-execution.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 제품 업데이트: 해당 없음 — executable·scheduler·readiness·사용자·staging·production 경로 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
