---
id: ADR-0028
title: Progress-aware expired cleanup takeover
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0028: 만료된 cleanup progress를 보존하고 새 fence로 인계한다

## 맥락

[ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)의 execution ledger와 [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md)의 signed absence persistence가 생기면서 cleanup attempt는 `planned` 이후의 durable progress를 가질 수 있다. 그러나 기존 `ClaimCleanupLease` takeover는 forward/pristine attempt validator를 재사용해 cleanup ledger residue가 하나라도 있으면 거부했다. 이 상태에서 process가 종료되고 lease가 만료되면 receipt는 안전하게 fail-closed하지만 어떤 새 worker도 인계할 수 없어 영구 고립된다.

Takeover 시각은 이미 old fence expiry 이후다. 이를 `ValidateCleanupExecutionLedger`의 observation time으로 넘기면 올바른 과거 progress도 모두 invalid가 된다. 반대로 expiry 검사를 완화하면 만료 뒤 생성된 가짜 progress를 승인할 수 있다. 따라서 **과거 ledger 유효성**과 **현재 takeover 가능 시각**을 분리해서 검증해야 한다.

## 결정 기준

- 역사적 무결성: 이전 progress는 해당 phase의 마지막 persisted timestamp에서 old fence 안에 있었는지 검증한다.
- Fresh fencing: 현재 receipt, 두 index, exact prior attempt와 immutable prior target을 같은 transaction에서 읽는다.
- 원자 인계: Prior closure, receipt token/revision/count 증가와 pristine next attempt 생성을 함께 commit한다.
- 권한 비상속: 이전 delete outcome·absence evidence는 새 fence의 provider 권한이나 완료 근거가 아니다.
- Fail-closed: Missing/conflicting target, partial ledger, terminal residue 또는 clock incoherence는 모든 write를 0으로 만든다.
- 불변 근거: Prior target과 이미 저장된 progress field를 수정하지 않는다.
- 운영 격리: Local transaction component만 구현하고 scheduler/startup/delete/finalizer는 연결하지 않는다.

## 검토한 선택지

### 선택지 A: Ledger residue가 있으면 operator가 수동으로 receipt를 수정

- 장점: 자동 takeover 구현이 단순하다.
- 단점: Fence·target·progress의 원자 관계를 깨고 수동 수정 자체가 새로운 authority가 된다.
- 판단: 제외한다.

### 선택지 B: 이전 progress를 새 attempt에 복사해 계속 진행

- 장점: 재분류·재감사 비용을 줄일 수 있다.
- 단점: Old fence와 target에 결합된 outcome을 새 fence의 권한으로 승격한다. 특히 sequential GCS listing residual을 새 completion 근거로 오해할 수 있다.
- 판단: 제외한다.

### 선택지 C: 이전 attempt를 progress-preserving failed로 닫고 새 attempt는 pristine으로 시작

- 장점: 과거 근거는 감사 가능하게 보존하면서 새 worker가 current state를 다시 분류하고 새 target을 만든다.
- 단점: 새 attempt가 audit-first로 재시작하므로 추가 provider read가 필요하다.
- 판단: 채택한다.

## 결정

### 1. Live authorization과 historical reconstruction을 분리한다

`BuildCleanupExecutionLedgerPlan`은 active lease 안의 current provider/persistence 경계에 계속 사용한다. 별도 `BuildExpiredCleanupExecutionLedgerPlan`은 exact expired, still-current `started` attempt와 immutable target의 구조적 binding만 재구성한다.

Historical builder는 다음을 검증하지만 provider I/O authority를 만들지 않는다.

- Receipt가 여전히 `cleanup_pending + reservation_expiry + reserved`다.
- Old owner, token, lease acquired/heartbeat/expiry, receipt revision과 target command가 exact match다.
- Exact prior attempt가 cleanup owner, worker version, started time과 fence를 공유한다.
- Target hash가 canonical이고 target/plan의 expected paths와 request binding이 current unchanged receipt에서 재구성된다.
- Check time은 old lease expiry 이상이며 receipt update time보다 이르지 않다.

같은 plan type을 반환하더라도 destructive grant는 별도 private capability이므로 이 historical reconstruction만으로 delete를 호출할 수 없다. 향후 오용 가능성이 생기면 historical 전용 타입으로 더 좁힌다.

### 2. Ledger는 마지막 durable phase timestamp로 검증한다

Takeover의 현재 시각을 old ledger validator에 전달하지 않는다. Phase별 historical validation time은 다음과 같다.

```text
planned                    -> target.created_at
raw_dispatch_recorded      -> raw.dispatch_at
raw_outcome_recorded       -> raw.outcome_recorded_at
raw_absence_confirmed      -> raw.audited_at
manifest_dispatch_recorded -> manifest.dispatch_at
manifest_outcome_recorded  -> manifest.outcome_recorded_at
manifest_absence_confirmed -> manifest.audited_at
```

이 시각으로 schema, decision domain, target/plan hash, receipt revision, old fence, phase/revision, targeted flags, timestamps와 nonterminal shape를 검증한다. `completed`, disposition/error/evidence/completed timestamp, forward field residue, partial ledger와 future/after-fence phase time은 거부한다.

### 3. Target read clock까지 포함해 expiry를 보수적으로 판정한다

Transaction은 receipt, prior attempt와 prior target read time을 모두 읽는다. Application time을 포함한 네 clock의 폭이 허용 skew를 넘으면 unavailable이다. 그중 가장 이른 시각이 old lease expiry 전이면 `held`이며 write 0이다.

따라서 receipt와 attempt read만 expiry 이후이고 target read가 아직 expiry 전인 경계에서는 closure를 시작하지 않는다.

### 4. Closure는 progress field를 보존한다

검증된 prior `started` attempt에는 다음 세 field만 update한다.

```text
status = failed
failure_code = lease_expired
failed_at = conservative effective takeover time
```

Decision domain, target/plan hash, receipt revision, execution revision·phase와 raw/manifest progress는 그대로 남는다. Immutable cleanup target과 두 uniqueness index는 write하지 않는다.

같은 transaction에서 receipt fencing token, revision과 attempt count를 각각 정확히 1 증가시키고 새 cleanup lease를 기록하며 새 `started` attempt를 create한다. Duplicate incoming attempt나 다른 transaction conflict가 있으면 prior closure와 receipt update도 함께 rollback한다.

### 5. 새 attempt는 과거 권한을 상속하지 않는다

새 attempt는 cleanup execution field가 전부 비어 있다. Prior target, delete outcome, `confirmed_absent`와 evidence를 복사하지 않는다. 새 worker는 current receipt/fence 아래 classification과 immutable target을 다시 만들고 필요한 path를 audit-first로 확인해야 한다.

Prior signed absence는 과거 관측 근거로만 남는다. [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md)의 regular/soft-deleted sequential listing non-atomic residual도 그대로 유지되며 새 fence의 terminal authority가 아니다.

### 6. Pristine crash window는 기존 규칙을 유지한다

Ledger residue가 없는 pristine started attempt는 기존 R8a validator로 takeover한다. Target이 이미 생성됐지만 ledger initialization 전 crash한 경우에도 provider mutation intent가 없으므로 target을 새 attempt에 상속하지 않고 기존 pristine closure를 유지한다. 새 attempt는 새 attempt ID에 새 target을 만든다.

## 결과와 위험

- Cleanup progress가 생긴 뒤 process가 종료돼도 lease expiry 후 receipt가 영구 고립되지 않는다.
- Old progress는 변경 없이 보존되어 crash 위치와 bounded outcome을 감사할 수 있다.
- New fence는 이전 outcome을 authority로 사용하지 않아 stale worker와 replay를 차단한다.
- Historical builder가 live plan과 같은 concrete plan type을 반환하는 것은 현재 destructive grant separation 아래 안전하지만 future misuse 감시가 필요하다.
- `verified_empty`, raw-only, manifest-only target의 full takeover matrix와 모든 phase의 Emulator serialization 확대는 후속 회귀 강화 항목이다.
- 이 구현 자체는 phase executor, retry·hold, terminal `expired` finalizer나 runtime을 제공하지 않는다. 후속 [ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md)이 local phase executor를, [ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md)이 local success-only finalizer와 read-only response-loss correlation을 구현했다. Retry·hold disposition, accepted/held/rejected cleanup, nested purge와 runtime·scheduler·staging/production 경계는 계속 닫혀 있다.

## 연결 문서

- 선행 결정: [ADR-0023](./ADR-0023-fenced-cleanup-lease-claim.md), [ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md), [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md)
- 증거: [EVD-20260722-036](../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover)
- 사람 대상 리포트: [HR-20260722-27](../reports/human/HR-20260722-27-progress-aware-cleanup-takeover.md)
- 후속 결정·증거: [ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md), [EVD-20260722-037](../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution), [ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md), [EVD-20260722-038](../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation)
- 후속 finalization 사람 대상 리포트: [HR-20260722-29](../reports/human/HR-20260722-29-atomic-cleanup-expiry-finalization.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 제품 업데이트: 해당 없음 — runtime·scheduler·사용자·staging·production 경로 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
