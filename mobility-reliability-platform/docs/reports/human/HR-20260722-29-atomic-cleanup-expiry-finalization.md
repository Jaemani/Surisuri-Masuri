---
id: HR-20260722-29
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8h - atomic cleanup expiry finalization
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Atomic cleanup expiry finalization

## 한눈에 보기

- 이번 회차의 사전 목적: 두 artifact의 signed absence가 모두 남은 cleanup만 attempt·receipt·두 uniqueness index에 원자적으로 완료하고, commit 응답 유실을 추가 mutation 없이 판별한다.
- 보고 기준일의 실제 상태: Commit `f7e0a44`에서 success-only 4문서 finalizer와 `committed|not_committed|unverifiable` read-only correlation을 local domain·Firestore adapter로 구현했다. 근거와 검증 범위는 [EVD-20260722-038](../../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation)에 기록한다.
- 가장 중요한 차이 또는 위험: Terminal completion 자체는 구현됐지만 executable·phase executor·scheduler·staging·production에는 연결하지 않았다. Retry·hold disposition과 nested purge도 남아 있다.
- 사람에게 필요한 결정·확인: Staging writer exclusion, bucket 정책, retry·hold taxonomy와 purge 보존정책이 확정되기 전에는 actual cleanup runtime을 활성화하지 않는다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8h terminal finalization gate
- 계획한 기술 주제: Atomic multi-document state transition, immutable fence deadline, commit response-loss correlation, historical plan reconstruction, fail-closed terminal validation
- 예상 산출물: Finalization contract, Firestore 4문서 transaction, bounded outcome query, post-lease historical validator와 unit·Emulator concurrency tests
- 검토할 질문: Partial terminal state가 가능한가, target이 불변인가, 세 purge timestamp가 같은가, Firestore callback retry 뒤 첫 query가 보존되는가, lease 삭제 뒤 terminal evidence를 재검증할 수 있는가
- 계획 완료 조건: Local race·vet·build, Firestore Emulator single-winner/write-zero, workspace check·build·test와 clean CI를 통과하고 runtime은 계속 fail-closed로 둔다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Finalization pre-state | `검증됨` | `manifest_absence_confirmed/revision 7`, 두 `confirmed_absent`, unknown 0과 exact target·plan·fence만 허용 | Retry·hold terminal shape는 미구현 | WSL2 Docker Go unit/race |
| Atomic terminal commit | `검증됨` | Attempt `completed/expired`, receipt `expired`, 두 index의 purge eligibility를 4문서 transaction으로 기록하고 target write 0 | Nested target·attempt purge는 미실행 | Firebase demo Firestore Emulator |
| Response-loss correlation | `검증됨` | 첫 valid pre-state query를 error 뒤에도 보존하고 fresh read-only query가 세 상태로만 수렴 | Query는 mutation·재시도 권한이 아님 | unit/race + Firestore Emulator |
| Historical reconstruction | `검증됨` | Terminal receipt의 삭제된 lease를 되살리지 않고 target에 봉인된 original fence·revision으로 plan/evidence를 재계산 | Live execution capability와 연결하지 않음 | domain·adapter tests |
| Malformed terminal state | `검증됨` | Evidence·purge·linkage 손상은 성공 보정 없이 `unverifiable` 또는 unavailable로 fail-closed | 자동 repair는 미구현 | unit/race + Emulator |
| Runtime·제품 | `미연결` | Startup·scheduler·readiness·HTTP·사용자 경로 변화 없음 | 의도된 격리 | source composition과 fail-closed CI smoke |

### 실제 결과 상세

- Implementation commit: `f7e0a44` (`feat: finalize expired cleanup atomically`)
- Exact write set: Cleanup attempt, receipt, idempotency index와 client-batch index 네 문서만 같은 transaction에서 변경한다. Immutable cleanup target은 읽기만 한다.
- Terminal shape: Attempt는 `status=completed`, `outcome=expired`, cleanup revision 8과 evidence를 기록한다. Receipt는 `cleanup_pending -> expired`, revision `+1`, lease 제거와 completion/purge timestamp를 기록한다.
- Purge boundary: `purge_eligible_at=max(receipt retention floor, completed_at)`을 계산하고 receipt와 두 index에 같은 값을 요구한다. 이 값은 삭제 실행이 아니라 향후 purge 가능 시각이다.
- Response-loss safety: Outcome query는 transaction의 pre-state와 예상 revision을 봉인하며 `completed_at`을 미리 고정하지 않는다. Write 오류, callback retry drift와 commit response loss에도 첫 non-zero query를 caller가 보존한다.
- Correlation: Exact terminal state면 `committed`, 원 pre-state가 그대로면 `not_committed`, 다른 winner·partial state·evidence/purge 손상이면 `unverifiable`이다. 어떤 결과도 provider delete나 finalization 재실행 권한을 주지 않는다.
- Post-lease check: Receipt가 terminal이 되어 lease field가 제거된 뒤에도 immutable target의 original binding으로 terminal plan과 evidence를 read-only 재검증한다.
- Validation data: Synthetic receipt·attempt·target·index와 Firebase demo Firestore Emulator만 사용했다. 실제 GPS, 사용자·기관 데이터, staging·production Firestore/GCS와 actual object delete는 사용하거나 변경하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Atomic terminal commit과 response-loss correlation | [EVD-20260722-038](../../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Durable artifact phase 선행 경계 | [EVD-20260722-037](../../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution) | `verified` — local/Emulator/testbench/clean CI | Codex + independent review / 2026-07-22 |
| Progress-aware takeover 선행 경계 | [EVD-20260722-036](../../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0030](../../decisions/ADR-0030-atomic-cleanup-expiry-finalization.md)
- 실제 제품 업데이트: 해당 없음 — local component가 executable·scheduler·readiness·사용자·staging·production 경로에 연결되지 않았다.
- 인시던트: 해당 없음 — synthetic local/Emulator 검증이며 production·staging·field 영향이 없다.
- 열린 위험: Retry·hold disposition과 error-class persistence, accepted·held·rejected cleanup, nested metadata purge, staging IAM/write exclusion, bucket lifecycle·retention·soft-delete, runtime composition이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: Cleanup retry·hold disposition 또는 bounded purge/runtime 이전 정책
- 실제 상태를 반영한 다음 검증: Ambiguous/error outcome을 자동 delete 재시도와 분리해 durable하게 분류하고, 보존기간·legal hold·partial linkage에서 purge가 fail-closed하는지 확인한다.
- 필요한 사람의 결정·지원: Actual staging mutation 전에 Firebase/GCS writer inventory, least-privilege IAM, retention·restore drill과 승인 절차가 필요하다.

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] 실제 주장마다 EVD를 연결했다.
- [x] Purge eligibility를 actual deletion 또는 purge 완료로 표현하지 않았다.
- [x] Correlation query를 mutation·provider 권한으로 표현하지 않았다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 제품 업데이트·인시던트·회의가 없음을 각각 명시했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 리포트 내용을 검토했다.
