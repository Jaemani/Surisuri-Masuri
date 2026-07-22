---
id: HR-20260722-20
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R7 bounded forward recovery worker and checkpoint
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Bounded forward recovery worker

## 한눈에 보기

- 이번 회차의 사전 목적: due `reserved` receipt를 tenant별로 bounded 조회하고, claim winner만 single-receipt reconciler에 전달하며, poison item과 중단된 scan 뒤에도 진행 가능한 outer worker를 만든다.
- 보고 기준일의 실제 상태: Candidate query, fixed-cutoff checkpoint, recovery worker와 bounded retry가 commit `9bd7787`에 구현됐고 clean CI까지 통과했다. Local Go race와 Firestore Emulator 범위의 결과는 [EVD-20260722-029](../../evidence/2026-07.md#evd-20260722-029--bounded-forward-recovery-worker와-cross-run-checkpoint)에 기록한다.
- 가장 중요한 차이 또는 위험: Worker component는 executable에 연결하지 않았다. `status` 또는 `next_recovery_at` 자체가 없는 receipt는 candidate query에 나타나지 않으므로 별도 integrity audit가 필요하다.
- 사람에게 필요한 결정·확인: Clean CI와 별도 staging에서 index READY, service account, Scheduler/Cloud Run 호출 경계와 metrics·alert를 검증하기 전에는 runtime을 열지 않는다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 공간·텔레메트리 파이프라인 중 fail-closed recovery R7
- 계획한 기술 주제: deterministic candidate pagination, tenant isolation, poison item 격리, fixed-cutoff scan epoch, CAS checkpoint, bounded retry·timeout·panic isolation, privacy-safe observation
- 예상 산출물: provider-neutral candidate/checkpoint port, Firestore adapters와 composite index, bounded outer worker, concurrency·resume·privacy 회귀 테스트
- 검토할 질문: query가 claim 권한과 분리됐는가, 동일 due time cursor가 안정적인가, 지속 유입 중에도 scan이 wrap하는가, malformed candidate가 후속 receipt를 막지 않는가, checkpoint conflict가 receipt 권한을 만들지 않는가
- 계획 완료 조건: local component 검증과 별도로 staging index·IAM·Scheduler, runtime metrics·alert와 missing-field integrity audit가 필요하다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Candidate query | `검증됨` | Tenant direct collection에서 due reserved receipt를 `(next_recovery_at, document ID)`로 deterministic page | Production index READY 미검증 | local compile + Firestore Emulator |
| Malformed item isolation | `검증됨` | Cursor material이 유효한 malformed candidate는 claim 없이 invalid로 계수하고 다음 item으로 진행 | Missing status/due는 query에서 보이지 않음 | synthetic + Firestore Emulator |
| Fixed-cutoff checkpoint | `검증됨` | Tenant별 revision CAS, 동일 cutoff resume, concurrent winner 1명과 exhaustion reset | Staging contention·내구성 미검증 | Firestore Emulator |
| Bounded outer worker | `검증됨` | Page·item·claim·receipt·run budget, retry jitter seam, panic breaker와 acquired-only handoff | 실제 Scheduler 호출과 metrics exporter 미구현 | synthetic Go race |
| Privacy surface | `검증됨` | Observer와 public result는 bounded enum·count·duration만 노출 | 실제 logging backend scan 미검증 | reflection·unit test |
| Runtime composition | `미착수` | Startup·scheduler·readiness 변경 없음 | 의도적 차단 | 기존 fail-closed executable boundary |

### 실제 결과 상세

- Commit: `9bd7787` (`feat: add bounded forward recovery worker`)
- Query 경계: `tenants/{tenantId}/ingestReceipts`에서 `status == reserved`, `next_recovery_at <= scan_cutoff`, `next_recovery_at ASC`, `__name__ ASC`를 사용한다. Page는 `limit + 1` read로 다음 cursor 존재 여부를 판단한다.
- Candidate 진실성: 저장된 `receipt_id`와 Firestore document ID가 달라지거나 tenant·reservation key·state가 malformed이면 lease claim을 호출하지 않는다. Query 결과만으로 artifact 접근이나 receipt mutation 권한을 얻지 않는다.
- Poison 진행: Query가 반환한 문서에서 cursor material은 유효하지만 나머지 field가 손상된 경우 해당 item을 invalid로 격리하고 cursor를 전진시킨다.
- Scan epoch: Checkpoint는 cursor와 `scan_cutoff`를 함께 보존한다. 재개 실행은 cutoff 뒤 새로 due가 되는 정상 유입을 현재 epoch에 계속 붙이지 않고, 현재 pagination이 exhausted되면 cursor와 cutoff를 함께 reset한다. 이는 Firestore snapshot 고정이 아니므로 page 사이 due/state 변경은 중복 또는 다음 epoch까지의 지연을 만들 수 있다.
- Checkpoint 의미: Revision CAS는 advisory progress만 보호한다. Load·persist failure 또는 CAS loss는 aggregate 관측과 중복 scan을 만들 수 있지만 claim winner나 artifact 권한을 만들지 않는다.
- 실행 budget: Page와 item 수, page attempt, page/checkpoint/claim/per-item timeout, total run, lease duration과 panic count를 설정 상한으로 제한한다. Page read retry는 상한이 있는 exponential full jitter를 사용한다.
- Handoff: Exact sweeper lease grant가 검증된 candidate만 [ADR-0020](../../decisions/ADR-0020-two-pass-forward-reconciliation.md)의 reconciler로 전달한다.
- 개인정보: Worker 결과와 observer에 tenant·receipt·attempt·cursor·artifact path, provider 원문 오류, 좌표·body·UID·App ID를 넣지 않는다.
- 데이터 유형: `synthetic`, Firebase demo Firestore Emulator; production·field data 없음
- 알려진 제한: Missing `status`·`next_recovery_at` receipt audit, runtime metrics exporter·alert, startup endpoint, Cloud Scheduler, staging index·ADC/IAM·부하·비용은 검증하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Bounded candidate scan, fixed-cutoff checkpoint와 outer worker | [EVD-20260722-029](../../evidence/2026-07.md#evd-20260722-029--bounded-forward-recovery-worker와-cross-run-checkpoint) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Single-receipt reconciler handoff | [EVD-20260722-028](../../evidence/2026-07.md#evd-20260722-028--bounded-forward-reconciler-composition) | `verified` — local/Emulator/testbench/clean CI | Codex + independent review / 2026-07-22 |
| Sweeper claim과 atomic started attempt | [EVD-20260721-018](../../evidence/2026-07.md#evd-20260721-018--recovery-leasestarted-attempt-ledger와-reserved-cleanup-진입) | `verified` — local/Emulator/clean CI | Codex / 2026-07-21 |

근거가 없는 staging·production·field 성과와 실제 사용자·기관 결과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0021](../../decisions/ADR-0021-bounded-forward-recovery-worker.md), [ADR-0020](../../decisions/ADR-0020-two-pass-forward-reconciliation.md)
- 실제 제품 업데이트: 해당 없음 — worker는 startup·scheduler·readiness 및 사용자 흐름에 연결하지 않았다.
- 인시던트: 해당 없음 — 설계 리뷰와 테스트에서 발견·정정한 사항은 local synthetic·Emulator에 한정되고 production·staging·field 영향이 없다.
- 열린 위험: Missing status/due integrity audit, production composite index·ADC/IAM, Scheduler 인증, runtime metrics·alert, 실제 backlog fairness와 비용 검증이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8 expiry·integrity cleanup과 immutable deletion target
- 실제 상태를 반영한 다음 검증: Forward candidate와 분리된 cleanup mode·lease, quiet period, exact-generation dry-run target과 late-generation hold
- 필요한 사람의 결정·지원: R9 전에 별도 staging Firebase/GCP project와 Scheduler/Cloud Run service account, index 배포와 metric sink 범위를 승인한다.

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
- [x] GitHub clean CI 결과를 EVD-20260722-029에 최종 반영했다.
- [ ] 사람이 리포트 내용을 검토했다.
