---
id: HR-20260722-27
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8f - progress-aware expired cleanup takeover
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Progress-aware expired cleanup takeover

## 한눈에 보기

- 이번 회차의 사전 목적: Durable cleanup progress가 있는 attempt의 lease가 만료돼도 과거 근거를 변조하지 않고 새 fence로 원자 인계한다.
- 보고 기준일의 실제 상태: Commit `2b281a9`에서 historical plan reconstruction, phase-time ledger validation과 Firestore progress-aware takeover를 구현했다. Local full race/vet, 20회 targeted race, Firebase demo Firestore Emulator, workspace check와 container build 결과는 [EVD-20260722-036](../../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover)에 기록한다.
- 가장 중요한 차이 또는 위험: 새 attempt는 과거 progress를 상속하지 않는다. Signed absence를 포함한 old outcome은 감사 증거일 뿐 새 fence의 delete·completion 권한이 아니다.
- 사람에게 필요한 결정·확인: Phase executor를 구현하기 전에도 runtime/scheduler/delete 연결 금지를 유지한다. Staging IAM/write exclusion과 terminal finalizer 승인은 별도 gate다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8f crash-safe cleanup handoff
- 계획한 기술 주제: Historical validation, fencing handoff, transaction atomicity, progress-preserving failure ledger, non-inheritance
- 예상 산출물: Expired plan reconstruction, historical phase-time decoder, progress-aware claim transaction, all-phase unit/race와 Emulator rollback tests
- 검토할 질문: Takeover 시각을 old authorization으로 오용하지 않는가, target·plan·revision·fence가 exact한가, progress가 보존되는가, new attempt가 pristine인가, duplicate create 시 전체 rollback되는가
- 계획 완료 조건: 모든 nonterminal phase를 local race에서 인계하고 대표 signed phase의 Emulator atomic commit과 duplicate rollback을 확인한다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Historical plan | `검증됨` | Expired current receipt·attempt·target에서 canonical target/plan/fence binding 재구성, live lease와 drift 거부 | Historical 전용 result type은 후속 강화 가능 | WSL2 Docker Go unit/race |
| Phase-time validation | `검증됨` | 7개 nonterminal phase를 각 마지막 persisted time에서 old fence 안으로 검증 | Completed/terminal residue는 의도적 거부 | local unit/race |
| Transactional takeover | `검증됨` | Prior progress 보존 failure closure + receipt token/revision/count +1 + pristine attempt create | Phase executor는 미구현 | local unit/race + Firebase demo Firestore Emulator |
| Rollback·clock | `검증됨` | Duplicate attempt 전체 rollback, target read pre-expiry held, incoherent target clock unavailable/write 0, immutable target·두 uniqueness index 불변 | 모든 phase Emulator matrix는 후속 강화 | local unit/race + Emulator |
| Runtime·provider | `미연결` | Target write 0, 실제 GCS/Firebase production mutation과 scheduler/startup 연결 없음 | R8g/R8h 후속 | executable/runtime 사용 안 함 |

### 실제 결과 상세

- Implementation commit: `2b281a9` (`feat: recover expired cleanup progress`)
- Live/historical split: Active plan builder는 expiry 전만 허용하고 expired builder는 current unchanged binding의 역사적 재구성만 제공한다.
- Four-clock gate: Application, receipt, attempt와 target read clock의 coherence를 검사하고 가장 이른 시각이 expiry 전이면 takeover write 0이다.
- Historical time: Planned는 target create time, 이후 phase는 해당 dispatch/outcome/audit timestamp를 사용해 ledger를 검증한다.
- Exact binding: Target canonical hash, plan hash, receipt revision, old owner/token/lease, worker, attempt ID와 started time이 모두 일치해야 한다.
- Monotonic shape: Planned부터 manifest absence까지 7개 nonterminal phase를 허용하며 partial revision, forward residue, terminal disposition/evidence/completion은 거부한다.
- Preserved closure: Prior attempt에는 `status`, `failure_code`, `failed_at`만 update하고 cleanup progress와 immutable target은 바꾸지 않는다.
- No inheritance: New attempt는 새 token으로 시작하며 cleanup ledger field가 모두 비어 있다.
- Atomic rollback: Duplicate new attempt가 이미 있으면 prior closure와 receipt fence/revision/count update도 rollback된다.
- Validation: New takeover/expired-plan targeted race tests를 20회 반복했고 전체 Go race/vet, Firestore Emulator, workspace checks와 container build를 통과했다.
- Independent review: Blocking finding 없음. Nonblocking 강화 항목은 target shape matrix, additional all-phase Emulator coverage와 historical result type narrowing이다.
- Data 유형: Synthetic receipt·attempt·target과 Firebase demo project만 사용했다. 실제 GPS, 사용자, 기관, staging/production data와 GCS object는 사용하거나 변경하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Progress-aware takeover와 atomic rollback | [EVD-20260722-036](../../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Signed absence persistence 선행 경계 | [EVD-20260722-035](../../evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0028](../../decisions/ADR-0028-progress-aware-expired-cleanup-takeover.md)
- 실제 제품 업데이트: 해당 없음 — local control-plane component가 executable·scheduler·사용자·운영 경로에 연결되지 않았다.
- 인시던트: 해당 없음 — production·staging·field 영향 없음.
- 열린 위험: Phase-bound delete authorization, dispatch/outcome/signed-audit executor, retry·hold, terminal finalizer/correlation, sequential GCS listing residual, staging IAM과 runtime composition이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: Artifact 하나씩만 처리하는 phase-bound cleanup executor
- 실제 상태를 반영한 다음 검증: Dispatch applied winner만 delete하고 replayed caller는 provider call 0, timeout/unavailable은 `unknown`을 보존한 뒤 audit/manifest 진행 0으로 닫는다.
- 필요한 사람의 결정·지원: Staging GCS writer identity·lifecycle·soft-delete 정책 전에는 actual delete/runtime을 활성화하지 않는다.

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
- [x] Historical validation을 live provider authority로 표현하지 않았다.
- [x] Old progress가 새 fence에 상속되지 않음을 명시했다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보, object path와 GPS 좌표가 없다.
- [ ] 사람이 리포트 내용을 검토했다.
