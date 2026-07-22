---
id: HR-20260722-17
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R6 terminal action outcome and attempt failure boundary
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Forward recovery outcome·attempt failure 경계

## 한눈에 보기

- 이번 회차의 사전 목적: two-pass forward recovery의 마지막 Firestore action을 receipt와 attempt에 원자적으로 기록하고, 실패 attempt와 commit 응답 유실을 안전하게 판별할 수 있는 capability 경계를 구현·검증한다.
- 보고 기준일의 실제 상태: stored·rejected·hold·release action transaction, attempt-only failure, expired prior attempt closure와 fresh outcome correlation이 `main` commit `07b25e5`에 구현됐다. WSL2 Docker 전체 Go gate, Firestore Emulator race, 독립 재리뷰와 clean CI를 통과했다.
- 가장 중요한 차이 또는 위험: protocol component는 구현됐지만 worker execution loop와 startup·scheduler에는 연결하지 않았다. current authorization denied/unavailable을 각각 bounded hold/release로 변환하는 별도 disposition capability도 남아 있다.
- 사람에게 필요한 결정·확인: 현재 readiness를 열거나 staging worker를 배포하지 않는다. 다음 disposition 경계와 bounded reconciler composition을 구현한 뒤 staging Firestore/GCS IAM 실험 범위를 검토한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 공간·텔레메트리 파이프라인 중 fail-closed recovery R6
- 계획한 기술 주제: opaque mutation/read capability, fencing과 revision correlation, atomic receipt+attempt commit, response-loss recovery, bounded failure ledger, read-time coherence
- 예상 산출물: action·attempt·outcome domain contract, Firestore adapters, 4-action Emulator matrix, response-loss read-only proof와 failure/rollback tests
- 검토할 질문: stale worker가 terminal receipt를 바꿀 수 없는가, commit 응답이 사라져도 mutation을 replay하지 않고 결과를 확인할 수 있는가, failure가 receipt를 오염시키지 않는가
- 계획 완료 조건: protocol component뿐 아니라 authorization disposition, bounded worker, startup composition과 staging IAM·transaction 증거가 필요하다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Terminal action transaction | `검증됨` | stored·rejected·hold·release가 receipt와 exact started attempt를 같은 transaction에서 갱신 | runtime reconciler 미연결 | local synthetic + Firestore Emulator |
| Attempt-only failure | `검증됨` | bounded live failure는 receipt 불변, exact attempt만 failed로 갱신 | worker error mapping 미연결 | local synthetic + Firestore Emulator |
| Expired prior closure | `검증됨` | takeover가 old started attempt를 lease_expired로 닫고 새 fence·attempt를 생성 | production contention 미검증 | local synthetic + Firestore Emulator |
| Fresh outcome correlation | `검증됨` | committed/not_committed/unverifiable을 exact fence·revision·hash·summary로 구분하고 read는 mutation 0 | 외부 API surface 미정 | local synthetic + Firestore Emulator |
| Privacy·bounded output | `검증됨` | outcome projection에 path·UID·App ID·body·좌표가 없고 codes·CRC range를 검증 | runtime log scan 미구현 | reflection/value/unit tests |
| Runtime composition | `미착수` | protocol port와 adapter만 존재 | 의도적 차단 | 해당 없음 |

### 실제 결과 상세

- 결과: terminal action 성공, 실패, 응답 유실과 다음 takeover 사이의 control-plane 상태를 receipt–attempt 원장으로 상관 분석할 수 있게 됐다.
- 원자성: action 4종에서 receipt revision/state와 attempt outcome/action hash가 함께 commit됐다. missing attempt는 receipt mutation까지 rollback됐고 current consent withdrawal은 write 0이었다.
- response-loss: fresh read grant는 prior fence와 expected action hash·revision을 봉인한다. exact commit에서만 persisted hash를 반환하며 mismatch에는 caller expected hash를 evidence로 노출하지 않는다.
- 실패 원장: live worker가 임의 provider error 문자열이나 `lease_expired`를 기록할 수 없고, 다음 claim만 exact expired fence를 근거로 old attempt를 닫는다.
- 관측 수치: final local Firestore Emulator에서 15개 top-level integration entrypoint와 action subcase가 race detector로 통과했다. GitHub CI 한 job은 5분 52초에 성공했다. latency·throughput·비용 수치는 측정하지 않았다.
- 데이터 유형: `synthetic`, Firebase demo Emulator; field·staging data 없음
- 알려진 제한: reconciler·scheduler·startup, staging ADC/IAM, actual GCS version/soft-delete 의미, 실제 사용자 경로는 검증하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Action outcome·attempt failure·prior closure와 전체 gate | [EVD-20260722-026](../../evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Planner·manifest-only repair 선행 경계 | [EVD-20260722-025](../../evidence/2026-07.md#evd-20260722-025--two-pass-forward-recovery-planner와-manifest-only-repair-boundary) | `verified` | WSL2 Docker + official testbench |
| Current-state authorization 선행 경계 | [EVD-20260721-024](../../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization) | `verified` | WSL2 Docker + Firestore Emulator |

근거가 없는 field·staging·production 성과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0020](../../decisions/ADR-0020-two-pass-forward-reconciliation.md)
- 실제 제품 업데이트: 해당 없음 — startup·worker·사용자 흐름에 연결하지 않았다.
- 인시던트: 해당 없음 — 첫 Emulator 실행의 기존 takeover 경쟁 1회 실패는 단독 3회와 전체 재실행에서 재현되지 않았으며 production·staging·field 영향이 없다.
- 열린 위험: authorization disposition capability, bounded reconciler loop, staging Firestore/GCS race와 runtime observability가 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: current authorization denied/unavailable의 server-derived disposition과 bounded worker composition
- 실제 상태를 반영한 다음 검증: denied→`current_authorization_denied` hold, unavailable→`authorization_unavailable` release, exact receipt/fence/attempt transaction, stored/rejected/artifact mutation 0
- 필요한 사람의 결정·지원: disposition 경계까지 local/Emulator로 닫은 뒤 staging Firebase/GCP project·service account·bucket 실험 승인 범위를 결정한다.

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
- [ ] 사람이 리포트 내용을 검토했다.
