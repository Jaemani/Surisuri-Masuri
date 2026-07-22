---
id: ADR-0022
title: Atomic forward-attempt closure at reserved expiry cleanup transition
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0022: Reserved expiry cleanup 전이에서 forward attempt를 원자 종료한다

## 맥락

`BeginCleanupTransition`은 reservation deadline이 지난 `reserved` receipt를 `cleanup_pending`으로 바꾸면서 fencing token과 revision을 증가시키고 forward lease를 제거한다. 기존 구현은 receipt만 바꿨다. 만료된 forward lease에 대응하는 `recoveryAttempts/{lease_owner_id}`가 아직 `started`이면 이후 어떤 owner도 그 attempt를 종료할 수 없다.

- 이전 forward owner는 receipt가 더 이상 `reserved`가 아니고 fence도 증가했으므로 실패·완료 기록 권한을 잃는다.
- cleanup 경로는 제거된 lease의 owner·token·시작시각을 복원할 수 없다.
- receipt mutation 안전성은 fence가 지키지만 감사 원장, terminal purge 사전조건과 재개 판단은 영구 `started` 기록 때문에 모순된다.

이 문제는 runtime에 연결되기 전 local 설계 검토에서 발견됐다. Production·staging·field 데이터 영향은 없지만, R8 cleanup lease와 purge를 구현하기 전에 원장 불변조건을 닫아야 한다.

## 결정 기준

- 원자성: receipt가 cleanup으로 전환되면 이전 forward attempt도 같은 transaction에서 terminal이어야 한다.
- fail-closed: attempt linkage를 증명하지 못하면 lease 증거를 지우지 않는다.
- 시간 안전성: application, receipt snapshot과 attempt snapshot 중 어느 clock이라도 deadline 전이면 cleanup을 시작하지 않는다.
- 기존 의미 보존: initial request lease처럼 recovery attempt가 없는 정상 `count=0` 경로를 유지한다.
- 범위 제한: cleanup lease, target, Storage delete, `expired` 완료와 scheduler를 이 수정에 섞지 않는다.

## 검토한 선택지

### 선택지 A: Receipt만 전환하고 고아 attempt를 나중에 audit로 보정

- 장점: 기존 transaction read/write 수가 유지된다.
- 단점: cleanup과 purge가 불완전한 원장을 정상으로 받아야 하고, 보정 전에 owner·token 근거가 receipt에서 사라진다.
- 판단: 원자적 진실성을 회복할 수 없어 제외한다.

### 선택지 B: 전환 뒤 별도 transaction으로 attempt를 실패 처리

- 장점: `BeginCleanupTransition` 변경이 작다.
- 단점: receipt commit 뒤 attempt commit 전 crash 또는 권한 오류가 같은 고아 상태를 다시 만든다.
- 판단: partial failure window를 남기므로 제외한다.

### 선택지 C: 같은 transaction에서 exact prior attempt를 검증·종료

- 장점: transition과 ledger terminality가 함께 commit 또는 rollback된다. 누락·변조된 attempt에서 receipt lease 증거를 보존한다.
- 단점: cleanup transition에 nested attempt read와 조건부 update가 추가된다.
- 판단: 원장과 receipt의 불변조건을 동시에 보존하므로 채택한다.

## 결정

### 1. Recovery attempt가 있는 만료 lease는 exact linkage를 읽는다

`recovery_attempt_count > 0`이고 receipt에 만료된 request 또는 sweeper lease가 있으면 다음 문서를 같은 Firestore transaction에서 읽는다.

```text
tenants/{tenantId}/ingestReceipts/{receiptId}/recoveryAttempts/{leaseOwnerId}
```

Attempt는 receipt와 exact tenant, receipt ID, owner kind, owner/attempt ID, fencing token, worker version, `started_at == lease_acquired_at`으로 연결돼야 한다. Decision, action, artifact lineage, terminal residue가 섞인 `started` attempt는 유효하지 않다.

- Exact `started`: 같은 transaction에서 `failed`, `failure_code=lease_expired`, `failed_at=cleanupAt`으로 닫는다.
- Exact terminal `failed`: 기존 failure가 owner/fence/time과 일치하면 다시 쓰지 않고 receipt 전이만 계속한다.
- Missing, foreign token·owner, malformed 또는 `completed`: admission unavailable로 fail-closed하고 attempt와 receipt write를 모두 0으로 유지한다.
- `recovery_attempt_count == 0`: 최초 request owner lease만 attempt 없이 전환할 수 있다. Sweeper owner인데 count가 0이면 손상 상태로 거부한다.

### 2. Cleanup clock은 모든 참여 clock의 최솟값이다

조기 cleanup을 막는 effective time은 다음 세 값의 UTC 최솟값이다.

```text
min(application requested_at, receipt snapshot read_time, attempt snapshot read_time)
```

세 값의 최댓값과 최솟값 차이가 admission clock-skew 상한 5초를 넘으면 fail-closed한다. Attempt read 뒤 다시 계산한 최솟값이 reservation deadline 또는 lease expiry 전이면 `transition_not_ready`로 mutation 없이 반환한다.

Pairwise clock 계산을 연쇄하지 않는다. Pairwise 차이는 각각 허용 범위여도 전체 earliest/latest 폭이 상한을 넘을 수 있기 때문이다.

### 3. Attempt update가 receipt transition보다 먼저 같은 transaction에 기록된다

모든 read와 validation이 끝난 뒤 transaction write 순서는 다음과 같다.

1. 필요한 경우 exact prior attempt를 `failed/lease_expired`로 update
2. receipt의 status, fencing token, lease 제거, quiet-period, mode/origin, revision과 updated time update

Firestore transaction 전체가 commit되지 않으면 둘 다 적용되지 않는다. Recovery attempt count는 누적 감사 count이므로 감소시키지 않는다.

### 4. R8 실행 권한과 삭제는 계속 차단한다

이 결정은 cleanup ownership을 발급하지 않는다.

- `ClaimCleanupLease`, cleanup attempt와 renewal 없음
- immutable cleanup target 없음
- GCS delete scope·adapter와 exact-generation delete 없음
- `expired`, purge eligibility와 bounded purge 없음
- startup, scheduler, readiness 변경 없음

## 결과와 위험

- Receipt가 `cleanup_pending`이면 전환 시점에 존재했던 forward attempt가 더 이상 `started`로 남지 않는다.
- Corrupt attempt를 자동 치유하지 않고 transition 자체를 거부하므로 운영자가 원본 lease 증거를 조사할 수 있다.
- Nested read가 추가돼 transaction 비용과 실패 표면이 조금 늘어난다. 이는 cleanup entry의 빈도와 원장 진실성을 고려해 수용한다.
- 이미 과거 버전에서 만들어진 `cleanup_pending + orphan started` 상태를 이 명령이 소급 치유하지는 않는다. Runtime rollout 전 bounded integrity audit에서 별도로 탐지해야 한다.
- 다음 R8a는 immutable `cleanup_transitioned_at`, cleanup 전용 owner/version/attempt와 quiet-period claim 계약을 먼저 고정한 뒤 구현한다.

## 연결 문서

- 선행 결정: [ADR-0017](./ADR-0017-fenced-ingest-recovery.md), [ADR-0020](./ADR-0020-two-pass-forward-reconciliation.md)
- 증거: [EVD-20260722-030](../evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료)
- 사람 대상 리포트: [HR-20260722-21](../reports/human/HR-20260722-21-cleanup-transition-attempt-closure.md)
- 제품 업데이트: 해당 없음 — cleanup runtime·사용자 경로에 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없는 local 설계 결함 정정
