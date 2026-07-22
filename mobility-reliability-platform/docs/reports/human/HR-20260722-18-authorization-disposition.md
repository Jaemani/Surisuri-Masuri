---
id: HR-20260722-18
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R6 current authorization disposition boundary
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Current authorization disposition 경계

## 한눈에 보기

- 이번 회차의 사전 목적: current authorization denied/unavailable을 artifact classifier 결과로 위장하지 않고, 별도 capability로 안전하게 hold/release하며 receipt와 attempt에 원자 기록한다.
- 보고 기준일의 실제 상태: denied hold, readable-malformed unavailable release, transaction 시점 재평가와 decision-domain 기반 fresh outcome correlation이 `main` commit `03bd550`에 구현됐다. WSL2 Docker 전체 Go gate, 실제 Firestore Emulator 경로, 독립 재리뷰와 clean CI를 통과했다.
- 가장 중요한 차이 또는 위험: protocol component만 존재하며 reconciler execution loop·startup·scheduler에는 연결하지 않았다. Transport outage처럼 coherent current snapshot을 얻지 못하면 release를 추측하지 않고 capability 0으로 닫는다.
- 사람에게 필요한 결정·확인: readiness와 staging worker를 계속 닫아 둔다. 다음 bounded reconciler composition 전에 기존 completed attempt 원장이 배포 환경에 있는지 확인하고, 있다면 `decision_domain` rollout/migration을 별도로 결정한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 공간·텔레메트리 파이프라인 중 fail-closed recovery R6
- 계획한 기술 주제: 별도 disposition capability, current-state 재평가, bounded hold/release, atomic receipt+attempt, domain-separated response-loss correlation
- 예상 산출물: provider-neutral disposition contract, Firestore atomic adapter, denied/unavailable·TOCTOU·rollback Emulator matrix, fresh outcome proof
- 검토할 질문: caller가 action/code를 선택할 수 없는가, transport 오류를 current unavailable로 오인해 release하지 않는가, preflight 뒤 권한이 바뀌면 write 0인가, artifact action과 outcome이 교환되지 않는가
- 계획 완료 조건: disposition component뿐 아니라 bounded reconciler loop, startup composition, staging IAM·GCS race와 observability가 필요하다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Denied disposition | `검증됨` | current consent withdrawal만 고정 hold/code로 commit | 실제 사용자 철회 미검증 | local synthetic + Firestore Emulator |
| Unavailable disposition | `검증됨` | readable-malformed relation만 고정 release/backoff로 commit | 실제 backend outage는 grant 0 | local synthetic + Firestore Emulator |
| TOCTOU 재평가 | `검증됨` | denied→allowed, unavailable→allowed/denied 변화 모두 write 0 | production transaction timing 미검증 | Firestore Emulator |
| Atomic ledger | `검증됨` | receipt와 exact started attempt update 2건, missing attempt면 receipt rollback | runtime caller 미연결 | local fake + Firestore Emulator |
| Decision-domain outcome | `검증됨` | current authorization과 artifact reconciliation을 분리하고 exact commit만 채택 | 기존 원장 migration 미수행 | local + Firestore Emulator |
| Runtime composition | `미착수` | port와 adapter만 존재 | 의도적 차단 | 해당 없음 |

### 실제 결과 상세

- 권한 파생: Authorizer 입력에는 action·hold/release code·raw error·artifact path가 없다. Domain이 current snapshot에서 `denied|unavailable`을 파생하고 action/code를 고정한다.
- 원자성: denied는 `recovery_hold/current_authorization_denied`, unavailable은 `reserved/authorization_unavailable`로 receipt와 attempt를 같은 transaction에서 완료했다.
- 재평가: commit 전에 linked receipt, 모든 current 관계와 exact attempt를 다시 읽는다. 현재 상태가 달라지거나 attempt가 없으면 artifact/receipt mutation이 없다.
- 결과 상관관계: attempt와 fresh query에 `decision_domain`을 기록·봉인한다. Wrong domain·disposition은 committed가 아니며 outcome read는 receipt와 attempt를 바꾸지 않는다.
- 데이터 최소화: disposition attempt에 classifier phase/class/reason, raw/manifest lineage, provider 오류를 쓰지 않는다. 실제 update field scan에서 artifact/sample field 0을 확인했다.
- 관측 수치: 전체 Go race suite와 disposition Emulator 3개 targeted top-level entrypoint 및 subcase가 통과했다. 처리량·latency·비용은 측정하지 않았다.
- 데이터 유형: `synthetic`, Firebase demo Emulator; field·staging data 없음
- 알려진 제한: reconciler·scheduler·startup, staging ADC/IAM, actual GCS race, 실제 사용자 경로는 검증하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Authorization disposition·원자 commit·fresh outcome | [EVD-20260722-027](../../evidence/2026-07.md#evd-20260722-027--current-authorization-disposition-원자-경계) | `verified` — local/Emulator/review/clean CI | Codex + independent review / 2026-07-22 |
| Terminal action·attempt·outcome 선행 경계 | [EVD-20260722-026](../../evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계) | `verified` | WSL2 Docker + Firestore Emulator + clean CI |
| Current-state authorization 선행 경계 | [EVD-20260721-024](../../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization) | `verified` | WSL2 Docker + Firestore Emulator |

근거가 없는 field·staging·production 성과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0020](../../decisions/ADR-0020-two-pass-forward-reconciliation.md)
- 실제 제품 업데이트: 해당 없음 — startup·worker·사용자 흐름에 연결하지 않았다.
- 인시던트: 해당 없음 — local test fixture migration과 Docker mount 정정은 production·staging·field 영향이 없다.
- 열린 위험: bounded reconciler loop, pre-change attempt rollout, staging Firestore/GCS race와 runtime observability가 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: current authorize→classify/repair→action/disposition→outcome을 합성하는 bounded reconciler execution loop
- 실제 상태를 반영한 다음 검증: per-receipt deadline, renewal 시 evidence 폐기, poison receipt 격리, retry/backoff, crash/resume와 structured observability
- 필요한 사람의 결정·지원: local loop gate 이후 staging Firebase/GCP project·service account·bucket·Scheduler 실험 승인 범위를 결정한다.

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
