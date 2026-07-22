---
id: HR-20260722-19
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R6 bounded single-receipt forward reconciler
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Bounded forward reconciler

## 한눈에 보기

- 이번 회차의 사전 목적: 이미 구현된 current authorization, generation-pinned classifier, manifest-only writer, atomic action/disposition과 outcome component를 한 receipt의 bounded two-pass 실행기로 합성한다.
- 보고 기준일의 실제 상태: `ForwardRecoveryReconciler`와 12개 orchestration 회귀 시나리오가 commit `d8aacdb`에 구현됐다. Local Go·Firestore Emulator·pinned Storage·workspace·container gate, 독립 재리뷰와 clean CI run `29891888924`를 통과했다.
- 가장 중요한 차이 또는 위험: single-receipt protocol은 구현했지만 candidate discovery·claim·pagination·poison isolation을 담당할 R7 outer worker와 startup wiring은 없다. Container readiness와 ingest는 계속 `503`이다.
- 사람에게 필요한 결정·확인: local CI가 끝난 뒤에도 staging Firebase/GCP project, service account, actual bucket과 Scheduler 권한을 검증하기 전에는 runtime에 연결하지 않는다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 공간·텔레메트리 파이프라인 중 fail-closed recovery R6
- 계획한 기술 주제: provider-neutral composition, 2-pass confirmation, manifest-only repair, renewal epoch barrier, response-loss correlation, caller cancellation finalizer
- 예상 산출물: production constructor, bounded state machine, late-commit barrier, privacy-safe execution result, renewal/cancellation/TOCTOU tests
- 검토할 질문: raw mutation port가 없는가, old evidence가 renewal 뒤 폐기되는가, pending transaction을 섣불리 not-committed로 단정하지 않는가, cancellation 뒤 허용되는 detached work가 control plane으로 한정되는가
- 계획 완료 조건: R6 local component completion 뒤 R7 outer worker, staging ADC/IAM·actual GCS race, metrics·alert와 startup gate가 별도로 필요하다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Production composition | `검증됨` | private classifier·authorizer 합성, manifest-only Storage port, Firestore control conformance | executable 미연결 | local compile/race |
| Complete/raw-only flow | `검증됨` | complete 2-pass, raw-only manifest 1회+post-confirm stored | actual GCS end-to-end는 component test 분리 | synthetic orchestration + pinned testbench |
| Renewal epoch | `검증됨` | renewal 뒤 old initial evidence가 terminal action에 전달되지 않음 | real network renewal loss 미검증 | scripted local |
| Commit response loss | `검증됨` | exact outcome, failure barrier, old-query late commit 재조회, action replay 0 | actual Firestore network loss 미검증 | synthetic + existing Emulator transaction |
| Cancellation finalizer | `검증됨` | bounded attempt failure/disposition만 허용, ambiguity를 caller error와 함께 보존 | process crash는 미검증 | synthetic local |
| Runtime composition | `미착수` | route·scheduler·readiness 변화 없음 | 의도적 차단 | fail-closed container smoke |

### 실제 결과 상세

- 실행 budget: total 2분 중 5초를 outcome/finalizer tail로 예약하고 evidence epoch 2, operational step 24, detached finalizer step 12로 제한했다.
- 타입 경계: reconciler가 받는 Storage mutation은 `CreateManifest` 하나뿐이다. Raw body/create/rewrite/delete surface가 없으며 execution result도 full receipt/path 대신 state·revision만 노출한다.
- 정상 흐름: complete는 두 번의 fresh classification 후 action 1회, raw-only는 manifest-only create 1회와 full post-confirmation 뒤 action 1회다.
- 권한 흐름: denied/unavailable은 classifier·writer·normal action 0으로 별도 disposition을 사용한다.
- Renewal: 성공 시 request/grant/result/plan을 폐기하고 initial authorization부터 다시 시작한다. Renewal response가 불명확하면 old fence로 진행하지 않는다.
- Response loss: committed는 fresh exact outcome으로 채택한다. Not-committed 첫 snapshot 뒤에는 attempt-failure transaction을 conflict barrier로 사용하고, barrier 실패 시 old query를 다시 읽어 late commit을 회수한다.
- Cancellation: artifact I/O나 normal action을 detached로 계속하지 않는다. Bounded attempt failure와 정책상 필요한 disposition만 tail에서 허용한다.
- 원장 보강: failed prior attempt에 decision-domain 또는 authorization-disposition residue가 있으면 takeover가 거부된다.
- 관측 수치: reconciler top-level test 12개, mobile 65개, Rules 24개, contract fixture 6개, Firestore Emulator recovery/admission top-level 18개, pinned Storage entrypoint 3개가 통과했다.
- 데이터 유형: `synthetic`, Firebase demo Emulator, official local Storage testbench; field·staging data 없음
- 알려진 제한: outer worker, actual ADC/IAM, staging bucket semantics, metrics·alert, 실제 GPS·사용자·복지관 경로는 검증하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Bounded single-receipt composition과 late-commit/cancellation protocol | [EVD-20260722-028](../../evidence/2026-07.md#evd-20260722-028--bounded-forward-reconciler-composition) | `verified` — local/Emulator/testbench/review/clean CI | Codex + independent review / 2026-07-22 |
| Current authorization disposition | [EVD-20260722-027](../../evidence/2026-07.md#evd-20260722-027--current-authorization-disposition-원자-경계) | `verified` | WSL2 Docker + Firestore Emulator + clean CI |
| Atomic action·attempt·outcome | [EVD-20260722-026](../../evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계) | `verified` | WSL2 Docker + Firestore Emulator + clean CI |
| Manifest-only planner·writer | [EVD-20260722-025](../../evidence/2026-07.md#evd-20260722-025--two-pass-forward-recovery-planner와-manifest-only-repair-boundary) | `verified` | WSL2 Docker + pinned Storage + clean CI |

근거가 없는 field·staging·production 성과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0020](../../decisions/ADR-0020-two-pass-forward-reconciliation.md) section 10
- 실제 제품 업데이트: 해당 없음 — startup·worker·scheduler·사용자 흐름에 연결하지 않았다.
- 인시던트: 해당 없음 — 발견·정정 사항은 local test/review/build orchestration에 한정되고 production·staging·field 영향이 없다.
- 열린 위험: multi-receipt outer loop, operational observability, staging IAM·Storage semantics, `decision_domain` 이전 원장이 존재할 경우 migration 결정이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R7 bounded candidate worker와 recovery attempt 운영 관측
- 실제 상태를 반영한 다음 검증: deterministic page/cursor, poison receipt isolation, total-run deadline, backoff/jitter seam, structured metric·privacy scan
- 필요한 사람의 결정·지원: R7 local gate 뒤 별도 staging project와 Scheduler/Cloud Run service account를 만들 범위를 승인한다.

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
- [x] 관측 수치에 환경·모수를 표시했다.
- [x] synthetic·Emulator와 field 데이터를 구분했다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·EVD를 원문으로 링크했다.
- [x] GitHub clean CI가 완료됐다.
- [ ] 사람이 리포트 내용을 검토했다.
