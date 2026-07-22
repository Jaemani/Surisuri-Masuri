---
id: HR-20260723-30
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: 2026-07-23
roadmap_month: M3
technical_gate: R8i - phase-preserving cleanup retry and hold disposition
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Cleanup retry·hold disposition

## 한눈에 보기

- 이번 회차의 사전 목적: Cleanup execution 실패를 success로 꾸미지 않고 실제 phase와 원인을 durable하게 남기며, old provider capability와 새 retry fence가 겹치지 않게 한다.
- 보고 기준일의 실제 상태: Commit `318f3b5`에서 10-class retry·hold policy, phase-preserving attempt terminal, cleanup 전용 receipt cursor, 2문서 atomic commit, read-only response-loss correlation과 exact-boundary retry claim을 local domain·Firestore adapter로 구현했다.
- 가장 중요한 차이 또는 위험: Component와 transaction 계약은 검증됐지만 phase executor의 typed error가 아직 이 disposition을 호출하지 않는다. Hold release와 production cleanup runtime도 없다.
- 사람에게 필요한 결정·확인: Operator hold 승인·해제 절차와 staging writer/IAM·bucket policy가 승인되기 전에는 actual cleanup runtime을 연결하지 않는다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8i failure-control gate
- 계획한 기술 주제: Durable error taxonomy, phase-preserving terminal ledger, retry/hold policy, old-fence-bounded scheduling, Firestore atomicity, response-loss provenance
- 예상 산출물: Pure disposition contract, receipt cursor schema, Firestore 2문서 transaction, read-only outcome authorization, retry claim validator와 unit·Emulator concurrency tests
- 검토할 질문: 실패를 success revision으로 올리는가, old capability가 살아 있는데 새 claim이 생기는가, target/index가 변하는가, hold가 자동 재개되는가, commit 응답 유실 뒤 mutation을 반복하는가
- 계획 완료 조건: Local full Go gate, Firestore Emulator atomicity·single-winner·write-zero·response-loss 검증과 clean CI를 통과하고 runtime은 계속 fail-closed로 둔다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Durable error class | `검증됨` | `unknown`과 timeout/cancel/unavailable/response-unverifiable exact class를 같은 outcome revision에 저장 | Provider 원문 오류는 저장하지 않음 | WSL2 Docker Go unit/race |
| Exhaustive failure policy | `검증됨` | 10개 class를 retry 15/30/60분 또는 24시간 review hold로 고정 | Operator가 policy를 바꾸는 UI는 없음 | Pure domain table tests |
| Phase-preserving terminal | `검증됨` | Allowed phase 2/3/5/6과 revision을 보존한 `completed/cleanup_retry|cleanup_hold` | Success revision 8과 분리 | Domain·codec tests |
| Atomic disposition | `검증됨` | Attempt+receipt만 같은 transaction에서 갱신, immutable target·두 index write 0 | Actual provider call은 없음 | Firebase demo Firestore Emulator |
| Retry·hold cursor | `검증됨` | Retry는 old fence·backoff boundary에서만 pristine attempt 생성, hold는 review due 뒤에도 auto-claim 0 | Operator hold release 미구현 | Unit/race + Emulator |
| Response-loss correlation | `검증됨` | 최초 query 보존, fresh read로 `committed|not_committed|unverifiable` 판별 | Query는 mutation 권한이 아님 | Unit/race + Emulator actual commit-response loss |
| Runtime·제품 | `미연결` | Phase executor·startup·scheduler·readiness·HTTP·사용자 경로 변화 없음 | 의도된 격리 | Source composition + clean CI |

### 실제 결과 상세

- Implementation commit: `318f3b5` (`feat: persist cleanup retry hold disposition`)
- Error-class durability: Ambiguous provider result만 exact bounded class를 가질 수 있다. Known result의 residue와 same outcome/different class replay는 거부한다.
- Policy: Timeout, cancellation, unavailable, response-unverifiable은 15분, incomplete inventory는 30분, quota는 60분 retry다. Permission, precondition, generation, lineage 문제는 24시간 이내 사람 검토 hold다.
- Attempt terminal: `status=completed`, `decision_domain=expiry_cleanup`, `outcome=cleanup_retry|cleanup_hold`, 실제 cleanup phase/revision, disposition, class, evidence hash와 trusted completion time을 기록한다.
- Receipt cursor: Exact terminal attempt ID, disposition, class와 retry 또는 hold timestamp 하나만 기록하고 active lease를 제거한다. Baseline·active receipt에서 cursor residue는 invalid다.
- No overlap: Retry 시각은 old fence expiry와 class backoff 중 늦은 값이다. Claim transaction이 old attempt·target·evidence·fence를 다시 확인한 뒤에만 cursor를 지우고 새 attempt를 생성한다.
- No authority inheritance: 새 attempt는 old target, progress, delete outcome, absence evidence와 terminal disposition을 상속하지 않는다.
- Hold stop: Review due가 지나도 자동 claim하지 않는다. 이번 구현에는 operator release command가 없다.
- Response-loss: Transaction callback retry가 drift하거나 two-document commit 직후 응답만 유실돼도 첫 exact query가 남는다. Fresh correlation은 실제 committed state를 읽기만 하고 write나 provider mutation을 만들지 않는다.
- Semantic separation: Readable fence/evidence mismatch는 terminal evidence를 노출하지 않는 `unverifiable`, missing target·attempt·linkage와 malformed decode는 unavailable이다.
- Validation data: Synthetic control documents와 Firebase demo Firestore Emulator만 사용했다. 실제 GPS, 사용자·기관 데이터, staging·production Firebase/GCS와 actual object delete는 사용하거나 변경하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| R8i retry·hold disposition 전체 | [EVD-20260723-039](../../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition) | `verified` — local full gate, Emulator, clean CI | Codex + independent review / 2026-07-23 |
| Success finalizer 선행 경계 | [EVD-20260722-038](../../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Durable phase execution 선행 경계 | [EVD-20260722-037](../../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution) | `verified` — local/Emulator/testbench/clean CI | Codex + independent review / 2026-07-22 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0031](../../decisions/ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)
- 실제 제품 업데이트: [UPD-20260723-05](../../product-updates/UPD-20260723-05-cleanup-retry-hold-control.md) — local control-plane component이며 배포·사용자 화면 변화 없음
- 인시던트: 해당 없음 — synthetic local/Emulator 검증이며 production·staging·field 영향이 없다.
- 열린 위험: Phase executor composition, operator hold release, accepted·held·rejected cleanup, nested purge, scheduler/startup, staging IAM·bucket lifecycle·writer exclusion이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8 runtime composition 이전의 typed error routing·operator hold workflow 또는 bounded purge contract
- 실제 상태를 반영한 다음 검증: Phase executor가 정확한 stored class만 disposition으로 넘기며 response-loss query를 운영자가 재조회할 수 있는지, hold release가 old fence와 evidence를 다시 검증하는지 확인한다.
- 필요한 사람의 결정·지원: 실제 mutation 전에 staging service account, 모든 bucket writer, versioning·soft-delete·lifecycle·retention과 hold 승인자·감사 로그를 확정해야 한다.

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
- [x] Retry·hold terminal을 success 또는 actual deletion으로 표현하지 않았다.
- [x] Correlation query를 mutation·provider 권한으로 표현하지 않았다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 제품 업데이트·인시던트·회의 상태를 각각 명시했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 리포트 내용을 검토했다.
