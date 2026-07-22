---
id: ADR-0023
title: Fenced cleanup lease claim after immutable quiescence
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0023: 불변 quiet-period 뒤 cleanup 전용 lease를 발급한다

## 맥락

[ADR-0022](./ADR-0022-atomic-cleanup-transition-attempt-closure.md)는 reservation deadline cleanup이 만료된 forward attempt를 고아로 만들지 않도록 전이와 원장 종료를 한 transaction으로 묶었다. 다음 단계는 `cleanup_pending` receipt를 실제 cleanup 실행 주체가 가져갈 수 있는 소유권 계약이다.

Forward claim을 재사용하면 다음 의미가 섞인다.

- `request|sweeper`는 artifact를 복구해 `stored|rejected|recovery_hold`로 전진시키는 owner다.
- `cleanup`은 quiet-period 뒤 partial artifact를 조사하고, 이후 별도 target 권한이 있을 때만 삭제하는 owner다.
- Cleanup이 forward due query의 `next_recovery_at`을 유지하면 서로 다른 worker가 같은 receipt를 후보로 볼 수 있다.
- 전이 시각과 grace 계산 근거가 receipt에서 복원되지 않으면 claim 시점에 정책을 바꿔 쓰거나 late write보다 먼저 cleanup을 시작할 수 있다.

또한 기존 6분 grace는 provisional 값이었다. 실제 안전 근거로 쓰려면 최대 forward lease와 artifact mutation의 실행 상한이 코드에서 함께 강제돼야 한다.

## 결정 기준

- 목적 분리: cleanup owner와 forward owner를 교환하지 않는다.
- 시간 안전성: late artifact mutation이 끝날 수 있는 상한보다 strict하게 늦게 claim한다.
- 불변성: transition time, quiescence, mode, origin과 policy version을 claim/takeover가 다시 쓰지 않는다.
- 원장 정합성: claim과 `started` cleanup attempt를 같은 transaction으로 만든다.
- 경쟁 안전성: concurrent first claim과 expired takeover는 정확히 한 owner만 획득한다.
- 범위 제한: target, classifier, GCS delete, `expired`, renewal과 runtime activation은 별도 gate로 남긴다.

## 검토한 선택지

### 선택지 A: 기존 sweeper claim과 worker version을 재사용

- 장점: 새 domain port와 Firestore transaction이 필요 없다.
- 단점: forward finalizer와 cleanup action의 허용 상태·결과가 섞이고 잘못된 mutation port를 호출할 수 있다.
- 판단: 최소권한과 감사 의미를 깨므로 제외한다.

### 선택지 B: quiet-period 뒤 lease 없이 cleanup target을 바로 생성

- 장점: claim 단계가 줄어든다.
- 단점: 동시 worker의 target 경쟁, stale 실행과 attempt 원장을 통제할 fence가 없다.
- 판단: 실행 소유권 없이 삭제 권한으로 건너뛰므로 제외한다.

### 선택지 C: 불변 transition metadata와 cleanup 전용 fenced claim

- 장점: cleanup 소유권, quiet-period와 attempt ledger를 transaction 하나에서 검증한다. Forward mutation port를 거부할 수 있다.
- 단점: 별도 owner/version/grant와 takeover 검증이 필요하다.
- 판단: 목적·시간·원장 경계를 함께 보존하므로 채택한다.

## 결정

### 1. Cleanup transition metadata를 불변으로 고정한다

`BeginCleanupTransition`은 receipt에 다음을 같은 transaction으로 기록한다.

```text
cleanup_transitioned_at
cleanup_quiescence_until
cleanup_mode=reservation_expiry
cleanup_origin_status=reserved
cleanup_policy_version=telemetry-cleanup-transition.v1
```

현재 정책은 다음 strict inequality를 만족한다.

```text
DefaultCleanupLateWriteGrace = 11m
MaxLeaseDuration             = 5m
MaxArtifactOperationTimeout  = 5m

11m > 5m + 5m
```

`StoreBatch` 전체는 raw와 manifest를 합쳐 최대 5분인 단일 context boundary를 사용한다. Raw와 manifest에 각각 5분을 주지 않는다. GCS adapter는 취소를 provider call에 전달하고, context 완료 뒤 다음 create를 시작하지 않으며, late success가 반환돼도 trusted lineage로 승인하지 않는다. 원격 conditional create는 취소와 commit이 경쟁할 수 있으므로 quiet-period와 immutable replay 검증은 계속 필요하다.

### 2. Claim은 cleanup 전용 owner와 attempt만 허용한다

`ClaimCleanupLease`는 다음 조건을 모두 만족해야 한다.

- Receipt state가 `cleanup_pending`
- `cleanup_mode=reservation_expiry`
- `cleanup_origin_status=reserved`
- 알려진 transition policy와 exact `transition + 11m` quiescence
- Application, receipt snapshot, 필요한 prior attempt snapshot의 가장 이른 시각이 quiet boundary 이상
- Owner kind가 `cleanup`
- Owner ID와 cleanup attempt ID가 같고 UUID
- Worker version이 `telemetry-cleanup.v1`
- Lease duration이 30초 이상 5분 이하

First claim은 token, revision과 attempt count를 각각 정확히 1 증가시키고 같은 transaction에서 `started` attempt를 생성한다. Attempt에는 좌표, 경로, body, UID/App ID 또는 provider 원문 오류를 저장하지 않는다.

### 3. Forward due-query와 mutation에서 분리한다

Cleanup claim은 receipt의 `next_recovery_at`과 `last_recovery_code`를 삭제한다. Forward candidate query가 cleanup receipt를 재처리할 일정 의미를 만들지 않는다.

기존 `RenewLease`, `ReleaseLease`, `MarkStored`, `MarkRejected`는 cleanup owner receipt를 수정할 수 없다. Cleanup 전용 renewal·action port가 구현되기 전에는 lease가 만료되면 새 cleanup claim만 takeover할 수 있다.

### 4. Expired cleanup takeover는 prior attempt를 먼저 닫는다

Expired cleanup lease가 있으면 transaction은 exact prior attempt를 읽는다.

- Exact `started`: `failed/lease_expired`로 닫고 새 lease·attempt를 생성한다.
- Exact already `failed/lease_expired`: 재작성하지 않고 새 lease·attempt를 생성한다.
- Missing, foreign tenant/receipt/owner/token/version/started time, completed, 다른 failure code 또는 terminal residue: receipt와 attempt write 0으로 fail-closed한다.

Application·receipt·attempt clock의 전체 폭이 5초를 넘으면 거부한다. Attempt snapshot이 lease expiry보다 1ns라도 이르면 active lease로 보고 `lease_held`를 반환한다.

### 5. 이 claim은 artifact read/delete 권한이 아니다

`CleanupLeaseGrant`는 control-plane 처리 소유권만 증명한다. 다음 항목은 아직 권한이 없다.

- Artifact inventory/classification
- Immutable cleanup target 생성
- Raw 또는 manifest generation 삭제
- Receipt `expired` 완료
- Purge eligibility와 nested purge
- Scheduler, startup, readiness 또는 production deployment

R8b는 first-pass read-only classification을 immutable dry-run target으로 고정하는 별도 capability를 정의한다. Actual delete는 staging lifecycle/IAM과 exact-generation semantics 검증 전까지 금지한다.

## 결과와 위험

- Cleanup과 forward owner가 receipt에서 동시에 유효한 상태를 만들지 않는다.
- Transition policy를 claim 시점에 다시 계산하거나 grace를 줄일 수 없다.
- Concurrent claim과 expired takeover는 Firestore transaction에서 한 winner로 수렴한다.
- Context cancellation은 원격 GCS commit 자체를 증명 가능한 abort로 만들지 않는다. 11분 grace, immutable create와 replay 검증이 그 불확실성을 흡수한다.
- Cleanup lease가 만료되면 현재는 takeover만 가능하다. Renewal과 실패/완료 protocol은 cleanup executor 설계 전까지 열지 않는다.
- Runtime에 연결하지 않았으므로 운영·사용자 기능이나 삭제 완료를 주장하지 않는다.

## 연결 문서

- 선행 결정: [ADR-0017](./ADR-0017-fenced-ingest-recovery.md), [ADR-0022](./ADR-0022-atomic-cleanup-transition-attempt-closure.md)
- 증거: [EVD-20260722-031](../evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim)
- 사람 대상 리포트: [HR-20260722-22](../reports/human/HR-20260722-22-fenced-cleanup-lease-claim.md)
- 제품 업데이트: 해당 없음 — executable·사용자·운영 경로 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
