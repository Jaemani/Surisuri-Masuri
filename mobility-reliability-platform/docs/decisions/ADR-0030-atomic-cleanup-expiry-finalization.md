---
id: ADR-0030
title: Atomic cleanup expiry finalization and read-only response-loss correlation
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0030: Cleanup 성공은 4문서 원자 finalization과 read-only correlation으로만 확정한다

## 맥락

[ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md)의 artifact phase executor는 raw와 manifest의 dispatch, delete outcome, signed absence를 순서대로 durable하게 남기지만 성공해도 `manifest_absence_confirmed/revision 7`과 `ready_for_finalization`에서 멈춘다. 이 상태는 두 expected path의 부재 근거가 있다는 뜻이지 receipt가 terminal이라는 뜻은 아니다.

Attempt `completed`, receipt `expired`와 purge eligibility를 서로 다른 write로 저장하면 중간 crash에서 다음 모순이 생길 수 있다.

- Attempt만 completed이지만 receipt가 계속 `cleanup_pending`이다.
- Receipt만 expired인데 attempt evidence가 terminal이 아니다.
- Receipt와 두 uniqueness index의 `purge_eligible_at`이 서로 다르다.
- Commit 응답이 유실된 caller가 같은 finalization mutation을 반복한다.
- Terminal receipt의 lease field가 사라진 뒤 live-plan builder를 재사용해 과거 권한을 현재 권한처럼 해석한다.
- Finalization이 cleanup lease 만료 직전에 시작해 만료 뒤 commit된다.

따라서 success finalization은 exact terminal pre-state 하나만 받고, attempt·receipt·두 index를 한 transaction으로 닫으며, 응답 유실은 mutation replay가 아니라 별도의 read-only protocol로 판별해야 한다.

## 결정 기준

- Terminal evidence: 두 artifact 모두 durable signed absence이고 ambiguous `unknown`이 없어야 한다.
- Atomic linkage: Attempt, receipt와 두 uniqueness index가 함께 commit 또는 rollback되어야 한다.
- Immutable target: Cleanup target은 finalization에서도 write하지 않는다.
- Fence deadline: Transaction commit까지 immutable cleanup lease의 exclusive expiry 안이어야 한다.
- Response-loss safety: Commit 결과가 불명확해도 같은 mutation을 반복하지 않는다.
- Historical reconstruction: Terminal receipt의 lease를 되살리지 않고도 원 target·plan binding을 재검증할 수 있어야 한다.
- Bounded disclosure: Correlation 응답은 경로·PII·provider 원문 없이 최소 상태만 노출한다.
- Runtime isolation: Retry·hold·purge와 staging 정책이 닫히기 전 scheduler·startup에 연결하지 않는다.

## 검토한 선택지

### 선택지 A: Attempt, receipt와 index를 순차 update

- 장점: 구현이 단순하고 작은 write를 재사용할 수 있다.
- 단점: 중간 crash와 부분 성공에서 terminal ledger와 purge linkage가 모순된다.
- 판단: 제외한다.

### 선택지 B: Commit 오류 시 finalizer를 그대로 재호출

- 장점: 별도 outcome 계약이 필요 없다.
- 단점: 첫 commit이 성공했는지 모르는 상태에서 mutation을 반복하며, 다른 winner나 손상된 terminal state를 정상 replay로 오인할 수 있다.
- 판단: 제외한다.

### 선택지 C: Exact pre-state를 4문서 transaction으로 닫고 read-only outcome query로 상관

- 장점: Partial terminal state를 만들지 않고 commit response loss를 `committed|not_committed|unverifiable`로 제한한다.
- 단점: Historical plan reconstruction과 별도 query capability, Emulator concurrency 검증이 필요하다.
- 판단: 채택한다.

## 결정

### 1. Finalizer 입력은 `manifest_absence_confirmed/revision 7` 하나다

Success finalizer는 current Firestore transaction에서 receipt, exact cleanup attempt, immutable target과 두 uniqueness index를 다시 읽는다. 다음 조건을 모두 만족할 때만 terminal candidate를 만든다.

- Receipt는 `cleanup_pending`이고 target에 봉인된 receipt revision·cleanup transition과 일치한다.
- Receipt·attempt의 owner kind, attempt ID, fencing token과 worker version이 exact cleanup fence와 일치한다.
- Ledger는 `manifest_absence_confirmed/revision 7`이다.
- Raw와 manifest의 audit outcome이 모두 `confirmed_absent`다.
- Delete outcome 어느 쪽에도 `unknown`이 없다.
- 두 audit timestamp는 completion 시각보다 미래가 아니며 최대 한 cleanup lease보다 오래되지 않았다.
- Target hash, plan hash, receipt revision과 fence가 ledger와 일치한다.

Generic forward outcome validator에는 cleanup-only `expired`를 추가하지 않는다. Cleanup success는 이 finalizer의 exact 계약에서만 허용한다.

### 2. Completion transaction은 immutable fence deadline으로 제한한다

Public finalizer는 먼저 authoritative target에서 immutable `lease_expires_at`을 읽고 application clock이 그보다 앞인지 확인한다. 실제 Firestore transaction에는 해당 시각을 exclusive `context.WithDeadline`으로 적용하고, callback 안에서도 current state와 conservative effective time을 다시 검증한다.

따라서 preflight 뒤 state가 바뀌거나 commit이 fence 만료를 넘으면 success authority로 확정하지 않는다. Parent cancellation은 그대로 보존하고 operation deadline은 cleanup authorization 상실로 처리한다.

### 3. Exactly four mutable documents를 한 transaction으로 닫는다

Finalization transaction은 다음 네 문서만 갱신한다.

1. Exact cleanup attempt
   - `status=completed`
   - `outcome=expired`
   - `cleanup_phase=completed`
   - `cleanup_execution_revision=8`
   - completion evidence hash와 `completed_at`
2. Receipt
   - `cleanup_pending -> expired`
   - revision `+1`
   - lease·next-recovery field 제거
   - `updated_at=completed_at`
   - `purge_eligible_at`
3. Idempotency index
   - 같은 `purge_eligible_at`
4. Client-batch index
   - 같은 `purge_eligible_at`

`purge_eligible_at`은 `max(receipt_retention_floor, completed_at + CleanupCompletionAuditWindow)`으로 계산한다. 현재 local policy의 audit window는 7일이며 staging 보존 승인 전 production 값으로 해석하지 않는다. Receipt와 두 index의 값은 exact equality여야 한다. Immutable cleanup target은 읽기만 하며 update·create가 0이다.

### 4. Response-loss query에는 transaction pre-state만 봉인한다

Firestore는 transaction callback을 재시도할 수 있으므로 query에 `completed_at`을 미리 넣지 않는다. Query는 다음 pre-state와 expected revision만 봉인한다.

- Tenant, reservation key와 attempt ID
- Target hash와 plan hash
- Original lease fence
- Pre/final receipt revision
- Pre/final ledger revision

Query는 모든 authoritative read와 validation 뒤, 첫 write 전에 만든다. 1~4번째 write 오류, transaction callback retry 뒤 drift와 commit 응답 유실에서도 non-zero query를 보존한다. Query revision은 signed overflow가 불가능해야 한다.

### 5. Correlation은 terminal receipt에서 historical plan을 재구성한다

Terminal receipt는 lease field가 제거됐으므로 live execution plan builder의 입력이 아니다. `BuildCompletedCleanupExecutionLedgerPlan`은 exact expired shape를 먼저 검증한 뒤 target에 봉인된 original receipt revision·fence·transition time만 historical view에 복원해 원 plan hash를 재계산한다.

이 reconstruction은 provider capability나 current cleanup authority를 만들지 않는다. Stored `completed_at`으로 terminal evidence hash와 purge eligibility를 다시 계산하기 위한 read-only 검증 도구다.

### 6. Correlation 결과는 세 상태뿐이다

Exact query에 대한 fresh read-only capability는 30초 이하이며 query binding 전체에 봉인된다. Outcome read는 receipt, attempt, target과 두 index를 읽되 write하지 않고 다음만 반환한다.

- `committed`: Exact terminal attempt·expired receipt·revision·evidence hash와 세 purge eligibility가 모두 일치한다.
- `not_committed`: Original `cleanup_pending` receipt와 started attempt, pre-finalization ledger·fence가 그대로다.
- `unverifiable`: 필요한 문서가 모두 구조적으로 읽힌 뒤에도 다른 winner, partial terminal residue, 잘못된 evidence 또는 purge 때문에 exact 두 상태로 증명할 수 없다.

Attempt·target·linkage 문서 누락, 구조 해석 실패 또는 authorization/read failure는 상태 판정 입력 자체가 성립하지 않으므로 `unavailable` 오류로 닫는다. 이 오류를 `not_committed`나 `unverifiable` outcome으로 축소하지 않는다.

`committed`만 재실행 없이 success로 수렴한다. `not_committed`는 후속 orchestrator가 fresh authority를 다시 얻을 수 있는 사실일 뿐 이 query가 mutation 권한을 주지는 않는다. `unverifiable`에서는 finalization, delete와 purge를 모두 중지한다.

### 7. 구현과 runtime 활성화를 분리한다

이 결정은 local domain·Firestore adapter와 Emulator test를 구현하지만 다음을 연결하지 않는다.

- Cleanup phase executor의 terminal call wiring
- Retry·hold disposition과 error-class persistence
- Nested attempt·target·finding purge worker
- Scheduler, startup, readiness와 HTTP route
- Staging·production Firebase/GCS 또는 actual object delete

## 결과와 위험

- Terminal 성공이 attempt·receipt·두 index에 partial하게 보일 수 없다.
- Cleanup target은 계획·계보 증거로 계속 불변이다.
- Commit response loss 뒤 destructive 또는 terminal mutation을 추측해 반복하지 않는다.
- Terminal state가 손상됐을 때 성공으로 보정하지 않고 `unverifiable`로 닫힌다.
- Correlation은 lease expiry 뒤에도 historical binding을 검증할 수 있지만 live authority는 만들지 않는다.
- Retry·hold와 nested purge가 없으므로 success 외 상태의 자동 운영 수렴과 실제 metadata 삭제는 아직 없다.
- GCS regular/soft-deleted inventory는 순차 관측이다. Staging IAM writer exclusion 전에는 이 local finalizer를 production absence authority로 활성화하지 않는다.

## 연결 문서

- 선행 결정: [ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md), [ADR-0027](./ADR-0027-paired-read-only-cleanup-absence-attestation.md), [ADR-0028](./ADR-0028-progress-aware-expired-cleanup-takeover.md), [ADR-0029](./ADR-0029-durable-artifact-phase-cleanup-execution.md)
- 증거: [EVD-20260722-038](../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation)
- 사람 대상 리포트: [HR-20260722-29](../reports/human/HR-20260722-29-atomic-cleanup-expiry-finalization.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 제품 업데이트: 해당 없음 — executable·scheduler·readiness·사용자·staging·production 경로 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
