---
id: HR-20260722-21
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8 precondition - atomic cleanup transition ledger closure
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Cleanup transition attempt-ledger 정합성

## 한눈에 보기

- 이번 회차의 사전 목적: R8 cleanup lease·target·delete를 시작하기 전에 reserved expiry transition이 기존 forward attempt를 고아로 만들지 않는지 검토한다.
- 보고 기준일의 실제 상태: 원장 결함을 재현했고 commit `21e2697`에서 `BeginCleanupTransition`이 exact expired attempt를 같은 transaction으로 종료하도록 수정했다. Local Go race와 Firestore Emulator 결과는 [EVD-20260722-030](../../evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료)에 기록한다.
- 가장 중요한 차이 또는 위험: Receipt mutation 자체의 stale fence 안전성은 기존에도 유지됐지만, 이전 `started` attempt가 영구 고아가 되어 감사·purge 진실성을 깨뜨릴 수 있었다. 이번 수정은 이를 cleanup 진입 전에 fail-closed한다.
- 사람에게 필요한 결정·확인: 다음 R8a에서는 cleanup claim만 먼저 구현하고 Storage delete·scheduler는 계속 분리한다. `cleanup_transitioned_at`과 late-write grace의 시간 상한을 별도 결정해야 한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8 expiry cleanup 선행 불변조건
- 계획한 기술 주제: recovery attempt terminality, Firestore multi-document atomicity, fencing linkage, conservative multi-clock cleanup boundary
- 예상 산출물: attempt+receipt atomic transition, missing/malformed attempt write-zero, clock boundary와 Emulator regression
- 검토할 질문: cleanup 전환이 이전 owner의 원장을 닫는가, 증명 불가능한 attempt에서 receipt lease를 지우지 않는가, attempt snapshot 시각이 조기 전이를 만들지 않는가
- 계획 완료 조건: local·clean CI 뒤에도 cleanup lease·target·delete·purge와 runtime wiring은 별도 gate로 남는다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| 결함 재현 | `검증됨` | Deadline cleanup이 lease를 지운 뒤 prior `started` attempt를 영구 고아로 만들 수 있음을 코드 경로로 확인 | Production 영향 없음 | local static review + synthetic fixture |
| Atomic closure | `검증됨` | Exact expired attempt를 `failed/lease_expired`로 닫고 receipt를 `cleanup_pending`으로 같은 transaction에서 전환 | Storage cleanup은 미구현 | local Go race + Firestore Emulator |
| Fail-closed linkage | `검증됨` | Missing, foreign-token, completed attempt는 receipt와 attempt write 0으로 거절 | 기존 손상 데이터 자동 치유 없음 | unit + Firestore Emulator |
| Time coherence | `검증됨` | App·receipt·attempt read time 전체 폭을 검사하고 최솟값이 deadline 전이면 `not_ready` | Staging clock-offset metric 미구현 | synthetic clock tests |
| Runtime composition | `미착수` | Cleanup lease·delete·scheduler·readiness 변경 없음 | 의도적 차단 | 기존 fail-closed executable boundary |

### 실제 결과 상세

- Commit: `21e2697` (`fix: close expired recovery attempts during cleanup`)
- 정상 started 경로: receipt의 tenant, receipt ID, lease owner/kind, fencing token, worker version과 `started_at`이 exact attempt에 일치할 때만 attempt를 `failed/lease_expired`로 닫는다.
- 이미 닫힌 경로: exact `failed` attempt는 재작성하지 않고 receipt transition만 수행한다.
- 초기 request 경로: `recovery_attempt_count=0`인 최초 request lease는 기존처럼 nested attempt 없이 transition할 수 있다. Sweeper lease인데 count가 0이면 거부한다.
- 손상 경로: Attempt 누락, foreign fencing token·owner, malformed terminal residue 또는 `completed` 상태는 cleanup transition 전체를 거부한다.
- 시간 경계: `min(requested_at, receipt read_time, attempt read_time)`을 사용하고 전체 earliest/latest 차이가 5초를 넘으면 unavailable로 닫는다. Attempt read 뒤 최솟값이 deadline 또는 lease expiry 전이면 write 없이 `transition_not_ready`다.
- 원자성: 필요한 attempt update와 receipt transition은 같은 Firestore transaction에 기록되므로 한쪽만 commit될 수 없다.
- 데이터 유형: `synthetic`, Firebase demo Firestore Emulator; 실제 GPS·사용자·복지관 데이터 없음
- 알려진 제한: 과거 orphan audit, cleanup 전용 claim/renewal, immutable target, exact-generation GCS delete, `expired`, purge와 runtime wiring은 구현하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Cleanup transition의 expired attempt 원자 종료·rollback | [EVD-20260722-030](../../evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료) | `verified` — local/Emulator/clean CI | Codex + delegated read-only review / 2026-07-22 |
| 기존 claim·cleanup transition 기반 | [EVD-20260721-018](../../evidence/2026-07.md#evd-20260721-018--recovery-leasestarted-attempt-ledger와-reserved-cleanup-진입) | `verified` — local/Emulator/clean CI | Codex / 2026-07-21 |
| Forward attempt failure·expired takeover closure | [EVD-20260722-026](../../evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계) | `verified` — local/Emulator/clean CI | Codex / 2026-07-22 |

근거가 없는 staging·production·field 성과와 실제 사용자·기관 결과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0022](../../decisions/ADR-0022-atomic-cleanup-transition-attempt-closure.md), [ADR-0017](../../decisions/ADR-0017-fenced-ingest-recovery.md)
- 실제 제품 업데이트: 해당 없음 — cleanup runtime과 사용자·운영 경로에 연결하지 않았다.
- 인시던트: 해당 없음 — local 설계 검토와 synthetic/Emulator 테스트에서 발견·정정했고 production·staging·field 영향이 없다.
- 열린 위험: 기존 orphan integrity audit, cleanup 전용 timestamp·lease·attempt, Storage timeout 상한과 late-write grace, immutable target·generation-pinned delete가 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8a cleanup-specific lease claim과 immutable transition timestamp
- 실제 상태를 반영한 다음 검증: `cleanup_pending + reservation_expiry + reserved origin`만 quiet-period 이후 cleanup owner가 claim하고 concurrent winner가 1명인지 확인
- 필요한 사람의 결정·지원: 실제 Storage delete와 staging lifecycle/IAM 검증 전까지 dry-run·control-plane 범위만 허용한다.

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] 실제 주장마다 근거가 있거나 제한을 표시했다.
- [x] synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·EVD를 원문으로 링크했다.
- [x] GitHub clean CI 결과를 EVD-20260722-030에 최종 반영했다.
- [ ] 사람이 리포트 내용을 검토했다.
