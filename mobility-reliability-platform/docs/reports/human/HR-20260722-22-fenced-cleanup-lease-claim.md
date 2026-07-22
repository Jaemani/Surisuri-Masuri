---
id: HR-20260722-22
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8a - immutable quiescence and fenced cleanup lease claim
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Fenced cleanup lease claim

## 한눈에 보기

- 이번 회차의 사전 목적: Reserved expiry transition 뒤 late artifact write보다 먼저 cleanup이 시작되지 않도록 불변 시간 정책과 cleanup 전용 소유권을 고정한다.
- 보고 기준일의 실제 상태: Commit `66733b2`에서 11분 quiet-period, 전체 `StoreBatch` 5분 boundary, cleanup 전용 claim·attempt와 expired takeover를 구현했다. Local Go race, Firebase demo Firestore Emulator, pinned official Storage testbench와 clean CI 결과는 [EVD-20260722-031](../../evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim)에 기록한다.
- 가장 중요한 차이 또는 위험: Cleanup 처리 소유권은 생겼지만 artifact read/delete 권한과 immutable target은 없다. 따라서 실제 삭제, `expired`, scheduler와 운영 성과는 아직 주장할 수 없다.
- 사람에게 필요한 결정·확인: R8b는 dry-run target까지만 진행하고 actual delete는 staging bucket lifecycle·retention·IAM과 generation 조건을 확인한 뒤 별도로 승인한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8 expiry cleanup
- 계획한 기술 주제: immutable transition metadata, strict late-write grace, purpose-specific lease, fencing takeover, context-bounded Storage mutation
- 예상 산출물: cleanup claim domain contract, Firestore transaction adapter, cleanup attempt ledger, concurrent winner·rollback·clock-boundary test
- 검토할 질문: Cleanup이 forward worker와 분리되는가, quiet-period 근거가 runtime에서 강제되는가, stale cleanup owner와 malformed attempt가 write를 만들지 않는가
- 계획 완료 조건: Local·clean CI 뒤에도 target·delete·`expired`·purge와 runtime wiring은 별도 gate로 남는다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Transition metadata | `검증됨` | transition time, exact quiescence, mode, origin과 policy version을 claim이 다시 쓰지 못함 | `reservation_expiry + reserved` origin만 구현 | local Go race + Firestore Emulator |
| Cleanup claim | `검증됨` | Cleanup owner/version만 quiet boundary 뒤 lease와 `started` attempt를 한 transaction에서 획득 | Renewal·executor 미구현 | local + Firestore Emulator |
| 경쟁·takeover | `검증됨` | Concurrent first claim과 expired takeover가 각각 1 acquired/1 held로 수렴 | Production transaction timing 미검증 | Firebase demo Firestore Emulator |
| Artifact time boundary | `검증됨` | Raw+manifest 전체 `StoreBatch`가 5분 이하이며 context 완료 뒤 추가 create와 trusted late-success를 차단 | 원격 commit abort를 보장하지 않음 | local race + pinned Storage testbench |
| Runtime composition | `미착수` | Scheduler·startup·readiness와 delete port 변경 없음 | 의도적 차단 | fail-closed executable boundary |

### 실제 결과 상세

- Commit: `66733b2` (`feat: add fenced cleanup lease claims`)
- 시간 정책: `11m > MaxLeaseDuration 5m + MaxArtifactOperationTimeout 5m`을 strict validator와 회귀 test로 고정했다.
- Storage 경계: `StoreBatch` 하나에만 최대 5분 context를 적용한다. Raw와 manifest별 timeout을 합쳐 10분으로 늘리지 않는다.
- GCS 취소 경계: provider call 전·후 context를 확인하고 raw 뒤 취소됐으면 manifest create를 시작하지 않는다. Manifest create가 remote commit 뒤 취소와 경쟁하면 trusted result를 반환하지 않는다.
- Claim 계약: `cleanup_pending + reservation_expiry + reserved origin + telemetry-cleanup-transition.v1`과 exact quiescence를 요구한다.
- 원장: Owner ID와 attempt ID는 같고 worker version은 `telemetry-cleanup.v1`이다. Claim transaction은 token·revision·attempt count를 각각 1 증가시키고 좌표·식별정보 없는 `started` attempt를 생성한다.
- Query 분리: Claim 뒤 raw Firestore document에서 `next_recovery_at` field가 실제 삭제된 것을 Emulator로 확인했다.
- Takeover: Exact prior `started`는 `failed/lease_expired`로 닫는다. 이미 실패한 prior attempt는 이 단계에서 `lease_expired`만 허용하며 다른 forward failure code는 거절한다.
- Fail-closed: Missing, foreign tenant/receipt/owner/token/version/started time, completed와 terminal residue는 mutation 0이다. Incoming attempt ID가 이미 존재하면 prior closure와 receipt update도 함께 rollback된다.
- 시간 coherence: Application·receipt·attempt clock 전체 폭 5초 초과는 unavailable이고 attempt read time이 expiry보다 1ns 이르면 held다.
- Data 유형: Synthetic control documents와 artifact fixture만 사용했다. 실제 GPS·사용자·복지관 데이터는 사용하지 않았다.
- 알려진 제한: Cleanup target, classifier, delete, renewal, attempt completion, `expired`, purge, staging IAM·lifecycle과 runtime은 구현하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Immutable quiescence와 cleanup claim/takeover | [EVD-20260722-031](../../evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim) | `verified` — local/Emulator/testbench/clean CI | Codex + delegated read-only reviews / 2026-07-22 |
| Cleanup transition attempt 원장 선행조건 | [EVD-20260722-030](../../evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료) | `verified` — local/Emulator/clean CI | Codex / 2026-07-22 |

근거가 없는 staging·production·field 성과와 실제 사용자·기관 결과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0023](../../decisions/ADR-0023-fenced-cleanup-lease-claim.md), [ADR-0022](../../decisions/ADR-0022-atomic-cleanup-transition-attempt-closure.md)
- 실제 제품 업데이트: 해당 없음 — cleanup adapter가 executable·사용자·운영 경로에 연결되지 않았다.
- 인시던트: 해당 없음 — local review와 synthetic/Emulator 검증에서 경계를 보강했고 production·staging·field 영향이 없다.
- 열린 위험: Remote conditional create의 cancellation/commit ambiguity, immutable target 부재, actual delete IAM·lifecycle, cleanup renewal·completion과 과거 orphan audit가 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8b immutable dry-run cleanup target과 read-only artifact inventory binding
- 실제 상태를 반영한 다음 검증: Cleanup lease owner가 exact receipt revision·fence와 classifier result를 target에 한 번만 고정하고 path/generation/hash를 바꾸지 못하는지 확인
- 필요한 사람의 결정·지원: Actual delete는 staging GCS 정책·권한·복구 창 검증 전까지 승인하지 않는다.

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
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·EVD를 원문으로 링크했다.
- [x] GitHub clean CI 결과를 EVD-20260722-031에 최종 반영했다.
- [ ] 사람이 리포트 내용을 검토했다.
