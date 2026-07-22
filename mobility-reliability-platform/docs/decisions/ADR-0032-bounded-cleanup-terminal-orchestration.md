---
id: ADR-0032
title: Bounded cleanup terminal orchestration
status: accepted
decided_at: 2026-07-23
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0032: Cleanup phase 실행과 terminal commit은 sealed intent 기반 오케스트레이터로 합성한다

## 맥락

[ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md)의 `PhaseExecutor`는 artifact별 dispatch를 provider 호출보다 먼저 기록하고, 각 delete outcome과 signed absence를 durable phase로 전진시킨다. 성공 종점은 `manifest_absence_confirmed/revision 7`의 `ready_for_finalization`이지만 실제 terminal commit은 호출하지 않는다.

[ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md)은 위 성공 상태를 attempt·receipt·두 uniqueness index의 4문서 transaction으로 닫고, [ADR-0031](./ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)은 bounded execution 실패를 attempt·receipt 2문서 transaction의 retry 또는 hold로 닫는다. 두 terminal store와 각각의 read-only response-loss correlation은 구현됐지만 phase 실행 결과를 어느 store에 한 번만 전달할지 결정하는 composition은 아직 없다.

단순히 `PhaseExecutor.Execute`가 반환한 Go `error`를 switch해 terminal mutation을 선택하면 다음 문제가 생긴다.

- 같은 generic unavailable이 incomplete inventory, control-store read 실패, malformed state와 provider unavailable을 구분하지 못한다.
- Provider outcome persistence 응답이 유실된 경우 durable ledger가 전진했는지 모르므로 같은 invocation에서 disposition을 만들 수 없다.
- Commit 응답 유실을 일반 재시도로 처리하면 finalization 또는 disposition mutation을 반복할 수 있다.
- Parent context cancellation이 commit 응답 유실의 원인이면 같은 context로 correlation을 시도하는 순간 read-only 확인도 취소된다.
- Success finalizer와 failure disposition을 각각 독립 호출하면 동일 invocation에서 두 terminal mutation surface가 열릴 수 있다.

따라서 phase 실행, terminal 선택, mutation, response-loss correlation을 합성하되 각 기존 컴포넌트의 권한 경계를 넓히지 않는 별도 오케스트레이터가 필요하다.

## 결정 기준

- Single terminal mutation: Invocation 하나에서 finalization과 disposition을 합쳐 최대 한 번만 호출한다.
- Durable authority: Terminal 선택은 durable phase와 exact binding에서 만들어진 sealed intent만 따른다.
- Honest failure: Generic error, error 문자열과 provider 원문으로 retry·hold를 추측하지 않는다.
- Ambiguity barrier: Outcome persistence 결과가 불명확하면 terminal intent와 terminal mutation은 모두 0이다.
- Read-only recovery: Terminal commit 응답 유실 뒤에는 mutation을 반복하지 않고 exact query로만 상관한다.
- Cancellation isolation: Correlation은 parent의 value는 보존하되 cancellation과 deadline은 분리한 짧은 read-only budget에서만 수행한다.
- Bounded disclosure: 결과·오류·관측에는 path, UID, payload, 좌표와 provider 원문을 넣지 않는다.
- Runtime isolation: Scheduler, startup, readiness와 staging·production provider에는 별도 승인 전까지 연결하지 않는다.

## 검토한 선택지

### 선택지 A: `PhaseExecutor` 안에서 terminal store까지 직접 호출

- 장점: 호출자가 하나의 메서드만 사용한다.
- 단점: Provider phase state machine이 Firestore terminal commit과 response-loss correlation까지 소유해 테스트와 권한 범위가 커진다. Phase 재시작 의미와 terminal 재확인 의미도 한 루프에 섞인다.
- 판단: 제외한다.

### 선택지 B: 호출자가 반환된 `error`를 분류해 finalizer 또는 disposition 호출

- 장점: 기존 `PhaseExecutor` 변경이 작다.
- 단점: Error는 exact durable ledger binding을 포함하지 않으며, `errors.Join`이나 generic unavailable을 잘못 축소하면 허위 disposition이 생긴다. 호출자마다 mutation 재시도 정책도 달라질 수 있다.
- 판단: 제외한다.

### 선택지 C: Phase executor 뒤에 sealed intent 기반 `TerminalOrchestrator` 추가

- 장점: Phase 실행은 provider와 durable progress까지만, orchestrator는 상호 배타 terminal 선택과 read-only correlation만 담당한다. Exact command를 phase 결과에서 봉인하고 terminal mutation을 invocation당 한 번으로 제한할 수 있다.
- 단점: Result contract, bounded mapper, resolver와 composition test가 추가된다.
- 판단: 채택한다.

## 결정

### 1. `PhaseExecutor`의 public 실행 경계는 유지하고 terminal intent를 추가한다

`Execute(context.Context, CleanupExecutionQuery) (ExecutionResult, error)` signature는 유지한다. `ExecutionResult`에는 다음 두 상태만 terminal 권한으로 인정한다.

```text
ready_for_finalization
  query = exact validated CleanupExecutionQuery
  durable ledger = manifest_absence_confirmed/revision 7
  error = nil

ready_for_disposition
  disposition_command = exact CleanupExecutionDispositionCommand
  durable ledger = allowed phase/revision
  error = diagnostic typed failure 또는 ErrCleanupOutcomeUnknown
```

`ErrorClass != ""`, 특정 `Phase`, Go `error`만으로는 terminal 권한이 아니다. Exported status와 command 조합도 그 자체로 sealed intent가 아니다. 실제 intent discriminant, original query, disposition command, durable fence binding과 integrity seal은 package-private field로 두고 `PhaseExecutor` 전용 pure constructor만 생성한다. Constructor는 최소한 query attempt와 fence owner의 일치, target·plan digest, receipt revision, allowed phase·revision, error class와 terminal residue 부재를 함께 검증한다. Orchestrator는 private seal을 재검증하지 못하면 mutation 0이다.

`ready_for_disposition`은 non-zero command가 `ValidateCleanupExecutionDispositionCommand`를 통과하고 private fence binding과 일치할 때만 유효하다. 다른 status에 command residue가 있거나 finalization status에 error가 있으면 전체 결과를 invalid로 거부한다. External caller는 exported `ExecutionResult` literal만으로 terminal intent를 만들 수 없다.

Phase executor 내부 실행은 마지막으로 확정된 durable ledger를 함께 추적한다. Restart에서 이미 저장된 raw 또는 manifest `unknown`을 만나면 in-process error를 재분류하지 않고 ledger에 저장된 exact `ErrorClass`를 우선한다. Stored class와 ledger phase·revision·target hash·plan hash·receipt revision이 함께 검증된 경우에만 disposition command를 만든다.

### 2. 실패 분류는 origin이 확인된 typed failure만 exhaustive하게 허용한다

Disposition intent는 다음 두 source에서만 만들 수 있다.

1. Durable `unknown` ledger에 이미 함께 저장된 네 ambiguous error class
2. Artifact provider 또는 absence auditor 호출에서 직접 반환된 아래 exact typed sentinel

허용 mapping은 ADR-0031의 policy와 동일하다.

| Typed source | Error class |
| --- | --- |
| provider timeout 또는 origin이 확인된 deadline | `provider_timeout` |
| provider cancellation 또는 origin이 확인된 cancellation | `provider_cancelled` |
| provider unavailable | `provider_unavailable` |
| provider response unverifiable | `response_unverifiable` |
| quota limited | `quota_limited` |
| exact complete-inventory 검증 실패 sentinel | `inventory_incomplete` |
| permission denied | `permission_denied` |
| precondition drift | `precondition_drift` |
| generation drift | `generation_drift` |
| lineage mismatch | `lineage_mismatch` |

Incomplete inventory에는 generic `ErrCleanupExecutionUnavailable`을 재사용하지 않고 별도 exact sentinel을 추가한다. 이 sentinel은 requested pass가 수행되지 않았거나 coverage가 incomplete·truncated·limit-exceeded인 bounded completeness 실패에만 사용한다. Duplicate, identity mismatch와 구조적으로 malformed한 inventory는 `inventory_incomplete`로 축소하지 않고 unavailable 또는 별도 exact invalid 상태로 남긴다. Generic unavailable, invalid, unauthorized, conflict, malformed control state, arbitrary internal error, error 문자열과 provider 원문은 어떤 bounded class로도 매핑하지 않는다.

Mapper는 `errors.Is` first-match switch를 사용하지 않는다. Recognized bounded class를 모두 수집해 정확히 한 class로 수렴할 때만 성공한다. 서로 다른 두 class가 `errors.Join`에 함께 있거나 recognized class가 없으면 intent를 만들지 않는다. Durable unknown은 stored class가 authoritative이며 diagnostic error에는 같은 class의 typed sentinel과 `ErrCleanupOutcomeUnknown`만 허용한다. Restart처럼 typed sentinel이 사라진 경우에는 `ErrCleanupOutcomeUnknown` 단독만 허용하고, stored class와 다른 recognized class가 섞이면 intent 0이다.

Context error도 출처를 잃은 채 phase executor 바깥에서 재분류하지 않는다. Provider·auditor 호출 경계에서 반환된 typed error만 mapper에 전달하며 initialization, dispatch persistence, audit authorization, audit persistence 같은 control-store error에는 disposition intent를 만들지 않는다.

### 3. Outcome persistence ambiguity는 terminal barrier다

Artifact provider가 non-zero result를 반환해도 `RecordCleanupArtifactExecutionOutcome`이 실패하면 durable outcome commit 여부를 현재 invocation이 알 수 없다. 이 경우:

- `ready_for_disposition`을 만들지 않는다.
- Finalization과 disposition mutation을 모두 호출하지 않는다.
- Provider나 outcome persistence를 같은 invocation에서 반복하지 않는다.
- 기존 durable-state 재시작 경로가 다음 invocation에서 exact ledger를 다시 읽도록 한다.

Audit evidence persistence와 terminal intent 생성 사이에도 동일 원칙을 적용한다. Control-store mutation이 성공했다고 확인되지 않으면 in-process evidence나 error만으로 terminal 상태를 추측하지 않는다.

### 4. 별도 `TerminalOrchestrator`가 상호 배타 terminal 선택을 소유한다

오케스트레이터는 세 경계만 의존한다.

```go
type phaseRunner interface {
    Execute(context.Context, ingest.CleanupExecutionQuery) (ExecutionResult, error)
}

type terminalMutator interface {
    ingest.CleanupExpiryFinalizationStore
    ingest.CleanupExecutionDispositionStore
}

type terminalOutcomeResolver interface {
    ResolveExpiryFinalization(
        context.Context,
        ingest.CleanupExpiryFinalizationOutcomeQuery,
    ) (ingest.CleanupExpiryFinalizationOutcome, error)

    ResolveExecutionDisposition(
        context.Context,
        ingest.CleanupExecutionDispositionOutcomeQuery,
    ) (ingest.CleanupExecutionDispositionOutcome, error)
}
```

Production constructor는 concrete `PhaseExecutor`와 `FirestoreAdmissionStore`를 받고 두 system outcome authorizer와 Firestore read store를 조합한 resolver를 내부 생성한다. Authorizer의 `checkedAt`과 outcome store의 `observedAt`은 같은 trusted UTC clock에서 얻는다. Fake resolver 주입 constructor는 package-private test seam으로만 둔다.

선택 규칙은 다음과 같다.

```text
valid ready_for_finalization + nil error
  -> FinalizeExpiredCleanup exactly once
  -> DisposeCleanupExecution zero

valid ready_for_disposition + exact private seal + non-nil allowed diagnostic error
  -> DisposeCleanupExecution exactly once
  -> FinalizeExpiredCleanup zero

all other phase result/error combinations
  -> terminal mutation zero
```

Orchestrator는 한 invocation에서 선택된 mutation을 original parent context로 정확히 한 번 호출한 뒤 해당 switch branch를 닫는다. Terminal-store error나 correlation 결과를 반대 terminal 종류로 fallback·reclassification하지 않는다. `not_committed`, conflict, unavailable과 context failure를 같은 invocation의 mutation 재호출 사유로도 사용하지 않는다.

### 5. Error와 valid outcome query가 함께 있을 때만 detached correlation을 수행한다

Finalizer 또는 disposition store가 성공 result를 반환하면 bounded terminal DTO로 투영해 닫는다. Store의 full `Receipt`, command와 query는 외부 orchestration result나 log에 그대로 반환하지 않는다. Store가 error를 반환한 경우에는 result에 해당 mutation 전 exact pre-state를 봉인한 valid `OutcomeQuery`가 함께 있을 때만 response-loss 가능성이 있다고 본다.

Zero·invalid query, query와 선택된 terminal 종류 불일치, mutation 호출 전 error에는 correlation을 수행하지 않는다. Valid query가 있으면 다음 read-only context를 사용한다.

```go
base := context.WithoutCancel(parent)
ctx, cancel := context.WithTimeout(base, boundedCorrelationTimeout)
defer cancel()
```

`boundedCorrelationTimeout`은 양수이고 5초 이하여야 하며 각 outcome grant TTL보다 짧아야 한다. `WithoutCancel`은 parent value만 보존하고 cancellation과 deadline을 제거한다. Original parent context는 최초 terminal mutation에 그대로 사용한다. Detached context는 outcome authorization과 read-only outcome 조회에만 사용하며 provider call, new claim 또는 terminal mutation에는 절대 전달하지 않는다.

Correlation 결과는 다음처럼 닫는다.

- `committed`: Bounded stored terminal 결과를 semantic success로 반환한다. 최초 mutation 오류는 raw error가 아니라 bounded diagnostic category로만 결과에 보존하고 재호출하지 않는다.
- `not_committed`: Mutation을 재호출하지 않고 explicit not-committed 결과·오류로 반환한다.
- `unverifiable`: 다른 winner 또는 semantic drift로 보고 fail-closed한다.
- Authorization/read unavailable, invalid, expired 또는 correlation timeout: Fail-closed한다.

어떤 correlation 결과도 provider mutation, finalization, disposition, lease claim 권한을 새로 만들지 않는다.

Resolver 반환은 `error == nil`일 때만 해석한다. Non-zero outcome과 non-nil error가 함께 오거나 commit-status enum이 알려지지 않았으면 unavailable로 fail-closed한다. `not_committed`, `unverifiable`과 unavailable에는 각각 전용 bounded orchestration error를 사용해 바깥 호출자가 raw transport error를 보고 즉시 mutation 재시도를 결정하지 못하게 한다.

### 6. 결과 surface와 관측값을 bounded하게 유지한다

Terminal orchestration 결과에는 다음 control 정보만 허용한다.

- Phase execution status, phase/revision과 artifact enum
- 선택된 terminal kind와 commit status
- Attempt ID, receipt revision, disposition/error class
- Evidence hash와 terminal timestamp
- Retry 또는 hold cursor timestamp
- Step 수와 bounded error category

Raw·manifest path, receipt document path, UID, device·trip·person ID, telemetry payload, 좌표, provider response와 credential은 결과·error·metric label에 넣지 않는다. Query와 command도 로그 전체 직렬화 대상이 아니다.

### 7. 첫 구현은 local component와 Emulator composition까지만 연다

이번 gate의 구현·검증 범위는 다음이다.

- `PhaseExecutor`의 exact durable intent 생성
- Exact inventory-incomplete sentinel과 bounded mapper
- Terminal outcome resolver와 `TerminalOrchestrator`
- Orchestrator unit/race test와 terminal-store Firestore Emulator 경쟁 검증. Concrete `TerminalOrchestrator.Run`의 Firestore/GCS end-to-end vertical slice는 후속 gate
- Success 4문서 transaction, retry·hold 2문서 transaction과 immutable target/index write 0 확인
- Finalizer와 disposition 경쟁에서 single valid lineage 확인

Scheduler, startup, readiness, metrics exporter, operator hold release, accepted·held·rejected origin cleanup, nested purge, staging·production Firebase/GCS와 실제 object delete는 계속 미연결이다.

## 결과와 위험

- Phase execution success와 bounded failure가 기존 두 terminal contract에 상호 배타적으로 연결된다.
- Restart된 durable unknown도 in-memory error가 아니라 저장된 class와 exact binding으로 동일 disposition을 만든다.
- Outcome persistence와 terminal commit response loss를 mutation 반복 없이 분리해 처리한다.
- Parent cancellation 뒤에도 제한된 read-only correlation은 가능하지만, detached context를 다른 I/O에 재사용하면 권한 범위가 넓어질 위험이 있다.
- Typed provider error가 generic control error로 조기에 축소되면 정당한 disposition을 만들 수 없으므로 provider·auditor adapter의 exact sentinel 보존 test가 필요하다.
- `committed` correlation은 terminal 완료를 확인하지만 원래 mutation call의 transport error를 지우지 않는다. 운영 관측에는 terminal 결과와 최초 오류 category를 분리해 남겨야 한다.
- Scheduler와 runtime은 이 ADR을 accepted로 바꾸고 local gate를 통과해도 자동 활성화되지 않는다.

## 연결 문서

- 선행 결정: [ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md), [ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md), [ADR-0031](./ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 구현 증거: [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration)
- 제품 업데이트: [UPD-20260723-06](../product-updates/UPD-20260723-06-cleanup-terminal-orchestration.md)
- 사람 대상 리포트: [HR-20260723-31](../reports/human/HR-20260723-31-cleanup-terminal-orchestration.md)
- 인시던트: 해당 없음 — 설계 변경이며 production·staging·field 영향 없음
