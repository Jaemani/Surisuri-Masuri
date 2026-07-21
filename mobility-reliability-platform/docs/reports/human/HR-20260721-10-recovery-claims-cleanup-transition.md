---
id: HR-20260721-10
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Recovery Claims and Reserved Cleanup Transition
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: Recovery claim·renew·reserved cleanup transition

## 한눈에 보기

- 이번 회차의 사전 목적: request와 recovery worker의 lease 인수·연장을 같은 fencing 규칙으로 직렬화하고, reservation deadline 뒤에는 forward 처리가 아닌 reserved-origin cleanup 대기 상태만 시작하게 한다.
- 보고 기준일의 실제 상태: `RenewLease`, sweeper 전용 `ClaimRecoveryLease`, HTTP/sweeper takeover의 atomic `started` recovery attempt와 count, reserved-origin `BeginCleanupTransition`이 local worktree에 구현됐다. server/app clock 방향과 주요 경쟁은 unit·Firestore Emulator test로 고정하는 중이다.
- 가장 중요한 차이 또는 위험: 이 구현은 recovery 실행기가 아니며 lease claim은 artifact 접근 권한이 아니다. classifier·forward reconciler·current system authorization runtime이 없으므로 sweeper claim 뒤 artifact inspect/write를 실행해서는 안 된다. cleanup object를 claim·삭제하는 코드와 다른 origin cleanup, runtime/staging 연결도 없다.
- 사람에게 필요한 결정·확인: EVD-018의 최신 전체 local gate와 clean CI를 확인한 뒤 generation-pinned read-only classifier를 다음 독립 gate로 시작할지 검토해야 한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한다.

- 로드맵상 위치: M3 / fenced forward admission 이후 claim·heartbeat·deadline transition 경쟁 닫기
- 계획한 기술 주제: explicit recovery claim, bounded lease renewal, atomic recovery-attempt start, monotonic attempt count, reserved-origin cleanup transition, clock-direction guard
- 예상 산출물: provider-neutral control port, Firestore transaction 구현, renew/takeover·claim/claim·cleanup/finalizer 경쟁 test, generated local evidence
- 검토할 질문: transaction winner만 owner가 되는가, started attempt와 count가 lease takeover와 함께 commit되는가, deadline 뒤 forward mutation이 0인가, 조기 cleanup이 가능한가
- 전체 계획 완료 조건: artifact classifier·forward reconciler·sweeper·origin별 cleanup 실행·bounded purge와 staging/runtime gate까지 끝나야 하며 이번 회차는 그 완료를 의미하지 않는다.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| `RenewLease` | `local 구현` | current owner/token/expiry와 3-way linkage를 확인하고 남은 시간이 renewal window 안일 때만 deadline 이내로 연장 | runtime heartbeat 호출자는 없음 | unit + Firestore Emulator |
| `ClaimRecoveryLease` | `local 구현` | sweeper owner kind만 받아 due·expired reserved receipt를 claim하고 held/not-due/deadline/not-eligible를 분리 | 실제 sweeper query·worker 없음 | unit + Firestore Emulator |
| HTTP takeover attempt | `부분 검증` | expired request replay가 lease takeover, token·revision·attempt count 증가와 `started` attempt 생성을 한 transaction에 기록 | attempt completion update 없음 | Firestore transaction/Emulator |
| sweeper claim attempt | `부분 검증` | explicit claim도 winning lease와 `started` attempt를 atomic commit하고 동시 loser는 held로 수렴 | scheduler·bounded page 없음 | Firestore Emulator |
| reserved cleanup entry | `부분 검증` | deadline 경과·reserved-origin·live lease 없음일 때 token·revision을 증가시키고 `cleanup_pending/reservation_expiry/reserved`와 quiet period를 기록 | cleanup target·lease·삭제 없음 | unit + Firestore Emulator |
| clock 방향 | `부분 검증` | forward acceptance는 `max(app, server read)`, 조기 cleanup 방지는 `min(app, server read)`, 허용 skew 초과는 fail-closed | staging clock fault injection 없음 | fake clock + Emulator |
| replay authorization time | `부분 검증` | authorization/receipt read time coherence를 확인하고 더 늦은 receipt time으로 같은 snapshot을 다시 평가해 consent·trip expiry를 닫음 | 실제 철회 transaction 경쟁·production 미검증 | unit + Firestore transaction seam |
| overflow·state invariant | `부분 검증` | forward revision overflow와 극단 clock skew는 update 0, reserved cleanup은 artifact-empty, pending/expired 시간 순서를 분리 | cleanup 실행·purge 없음 | unit test |
| worker provenance | `local 구현` | `worker_version`은 server allowlist의 `telemetry-recovery.v1`만 허용하고 자유 문자열·식별자 형태를 거부 | version rollout registry는 없음 | unit test |
| 경쟁 조건 | `부분 검증` | renew 대 takeover, 동시 sweeper claim, cleanup 대 claim/stale stored/stale rejected에서 commit된 owner 또는 cleanup 하나만 상태를 변경 | production contention·network 미검증 | Firestore Emulator |
| runtime·staging | `미착수` | gateway startup과 worker를 연결하지 않음 | readiness·ingest는 계속 `503` | 미검증 |

### 실제 결과 상세

- `RenewLease`는 fencing token을 바꾸지 않고 heartbeat·lease expiry·next recovery와 receipt revision만 갱신한다. lease가 충분히 남았거나 이미 만료됐거나 reservation deadline을 넘는 연장은 거부한다.
- `ClaimRecoveryLease`는 current receipt와 두 uniqueness index의 linkage를 transaction에서 다시 읽는다. active lease, 아직 due가 아닌 released receipt, deadline 경과와 non-reserved 상태를 서로 다른 read-only status로 반환한다.
- `ClaimRecoveryLease`는 control-plane owner를 정할 뿐 current tenant·trip·assignment·precise-location consent를 대신 확인하지 않는다. 실제 recovery runtime은 claim 뒤와 artifact 접근 전에 current system authorization을 별도로 재평가해야 하며, 그 경계가 구현되기 전에는 startup·scheduler에 연결하지 않는다.
- 최초 request는 recovery attempt가 아니다. 만료 lease를 인수하는 HTTP replay와 explicit sweeper claim만 `recoveryAttempts/{attemptId}`의 `started` 문서와 `recovery_attempt_count + 1`을 lease mutation과 함께 commit한다.
- HTTP replay는 authorization 문서 묶음의 read time coherence와 authorization/receipt read time 차이를 제한한다. receipt read가 더 늦으면 그 시각으로 같은 authorization snapshot을 다시 평가해 그 사이 만료된 consent·trip이 takeover를 얻지 못하게 한다.
- `BeginCleanupTransition`은 expired reservation의 **진입 상태만** 만든다. token·revision을 증가시키고 forward lease를 제거하지만 object generation을 선택하거나 cleanup target/lease를 만들거나 artifact를 삭제하지 않는다. reserved-origin `cleanup_pending`과 이후 `expired`는 stored artifact field·sample·rejection code가 없어야 하며, pending은 reservation deadline 이후, expired는 quiet period 이후 시각만 허용한다.
- forward·consent·deadline acceptance는 더 늦은 시각을 사용해 이미 만료됐을 가능성을 우선하고, cleanup readiness는 더 이른 시각을 사용해 아직 만료 전일 가능성을 우선한다. app/server 차이가 허용 범위를 넘거나 극단 시각이면 어느 방향도 진행하지 않는다. forward revision이 정수 상한에 도달한 경우에도 wrap하지 않고 mutation 0으로 닫는다.
- recovery attempt의 `worker_version`은 현재 `telemetry-recovery.v1` exact allowlist만 허용한다. 이메일·UID·App ID 또는 임의 버전을 provenance label로 넣지 못한다.
- cleanup late-write grace의 현재 6분 값은 local race·invariant 검증을 위한 provisional 기본값이다. staging latency와 lifecycle 증거, 개인정보 보존 승인 없이 운영 삭제 대기시간·SLO로 확정하지 않는다.
- 데이터 유형: `synthetic | test`; 실제 GPS, UID/App ID, 이용자·복지관 데이터 없음

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| claim·renew·origin별 cleanup의 전체 계약이 결정됨 | [ADR-0017](../../decisions/ADR-0017-fenced-ingest-recovery.md) | `accepted` decision; 구현 증거 아님 | 문서 검토 필요 |
| renew·explicit claim·atomic started attempt·reserved cleanup transition과 Emulator races가 local worktree에서 관찰됨 | [EVD-20260721-018](../../evidence/2026-07.md) | `generated` — 최신 전체 local gate와 clean CI 확인 전 | 사람 검토 필요 |
| cleanup 대상이 exact generation으로 삭제되고 purge됨 | 확인 필요 — 현재 구현하지 않음 | `미검증` | 해당 없음 |
| sweeper runtime과 production telemetry recovery가 활성화됨 | 확인 필요 — 현재 활성화하지 않음 | `미검증` | 해당 없음 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0017](../../decisions/ADR-0017-fenced-ingest-recovery.md), [ADR-0016](../../decisions/ADR-0016-immutable-telemetry-artifact-lineage.md), [ADR-0015](../../decisions/ADR-0015-atomic-telemetry-admission.md)
- 실행계획: [Telemetry Reservation Recovery 실행계획](../../plans/TELEMETRY_RECOVERY_PLAN.md)
- 실제 제품 업데이트: 해당 없음 — runtime·사용자·운영 경로에는 연결하지 않음
- 인시던트: 해당 없음 — production·field 배포와 사용자 영향 없음
- 열린 위험: system recovery authorization 부재, attempt completion/failure 유실, artifact 분류 부재, cleanup handoff·delete 부재, scheduler/query 부재, provisional grace 미조정, staging clock·contention·IAM 미검증

## 명시적 미구현 범위

- `BeginHeldCleanup`, `BeginAcceptedDeletion`, `BeginRejectedArtifactCleanup`
- cleanup target, `ClaimCleanupLease`, generation-pinned cleanup executor와 bounded purge
- generation-pinned classifier, forward reconciler와 bounded sweeper runtime
- current tenant·trip·assignment·consent를 재검사하는 system recovery authorization과 artifact permission 경계
- recovery attempt의 artifact classification·action·completed/failed update
- gateway startup wiring, Cloud Scheduler/Run IAM, staging·production 배포

## 다음 회차

- 8개월 계획상 다음 주제: R5 generation-pinned read-only artifact classifier
- 실제 상태를 반영한 다음 검증: manifest/raw generation inventory, exact compressed read, multiple-generation ambiguity, provider unavailable과 missing 분리
- 필요한 사람의 결정·지원: local EVD·clean CI 검토, staging bucket inventory·IAM·lifecycle 검증 일정

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] claim·transition 구현과 실제 worker·cleanup 실행을 분리했다.
- [x] local synthetic·Emulator와 staging/production을 구분했다.
- [x] reserved-origin transition을 다른 origin cleanup 완료로 확대하지 않았다.
- [x] runtime 미연결 변경을 제품 업데이트로 기록하지 않았다.
- [x] local test 실패를 사용자 영향 인시던트로 기록하지 않았다.
- [x] 참석자·사진·지출을 생성하거나 추정하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [ ] EVD-20260721-018의 최신 전체 local gate와 clean CI 결과를 확인했다.
- [ ] reviewer와 발행일을 사람이 확정했다.
