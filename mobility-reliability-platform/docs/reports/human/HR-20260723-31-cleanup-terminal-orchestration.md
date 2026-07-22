---
id: HR-20260723-31
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: 2026-07-23
roadmap_month: M3
technical_gate: R8j - bounded cleanup terminal orchestration
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Cleanup terminal orchestration

## 한눈에 보기

- 이번 회차의 사전 목적: Durable phase execution의 성공과 실패를 각각 올바른 terminal transaction에 한 번만 전달하고, persistence·commit 응답 유실을 mutation 반복 없이 처리한다.
- 보고 기준일의 실제 상태: Commit `a340e8b`에서 sealed terminal intent, exhaustive failure routing, invocation당 single terminal mutation, durable unknown restart, cancellation-isolated read-only correlation과 bounded result를 local component로 구현했다.
- 가장 중요한 차이 또는 위험: Orchestrator unit/race와 Firestore terminal-store 경쟁은 검증됐지만 concrete PhaseExecutor→Firestore/GCS→terminal end-to-end vertical slice와 runtime wiring은 아직 없다.
- 사람에게 필요한 결정·확인: Operator hold 승인·해제 절차와 staging IAM·bucket policy가 승인되고 full vertical slice가 통과하기 전에는 actual cleanup runtime을 연결하지 않는다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8j terminal composition gate
- 계획한 기술 주제: Capability-oriented orchestration, sealed state transition intent, exhaustive error taxonomy, exactly-once-per-invocation mutation surface, response-loss provenance와 bounded disclosure
- 예상 산출물: Package-private terminal intent, terminal orchestrator, exact failure mapper, detached read-only correlation, bounded result와 unit/race·Emulator competition tests
- 검토할 질문: Public result나 generic error가 mutation 권한이 되는가, success와 failure terminal이 모두 호출될 수 있는가, persistence ambiguity에서 terminal이 생성되는가, parent cancellation 뒤 mutation이 반복되는가, full receipt·path·identity가 결과에 노출되는가
- 계획 완료 조건: Local Go full gate와 Firestore terminal-store 경쟁·workspace clean CI를 통과하고 executable·production runtime은 계속 fail-closed로 둔다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Sealed terminal authority | `검증됨` | Package-private intent와 seal만 finalization/disposition 권한이며 public result literal은 mutation 0 | Public `Execute` signature 유지 | WSL2 Docker Go unit/race |
| Single terminal routing | `검증됨` | Success finalizer와 failure disposition 호출 합계 invocation당 최대 1회 | Mutation error를 반대 terminal로 fallback하지 않음 | Go unit/race |
| Failure taxonomy | `검증됨` | 10개 exact typed class, mixed/generic/internal error와 sentinel 사칭 거부 | Malformed inventory는 incomplete로 축소하지 않음 | Phase/GCS adapter tests |
| Durable restart | `검증됨` | Phase 3/6 `unknown`의 stored class와 exact ledger binding을 복원 | In-memory error가 없는 restart도 지원 | Phase executor tests |
| Ambiguity barrier | `검증됨` | Outcome·audit persistence failure와 unknown mutation status에서 terminal intent·mutation 0 | 다음 invocation의 durable-state 재시작에 위임 | Go unit/race |
| Response-loss correlation | `검증됨` | Valid query만 최대 5초 detached read-only correlation, mutation 재호출 0 | Fake resolver 기반 orchestrator test | Go unit/race |
| Terminal store atomicity | `검증됨` | Phase 7 finalizer와 stale phase 6 disposition 경쟁에서 finalizer lineage만 원자 commit | Orchestrator 전체 vertical slice는 아님 | Firebase demo Firestore Emulator |
| Result privacy | `검증됨` | Bounded `TerminalResult`에 full receipt·command·query·path·user/device/trip/person identity·좌표 type 없음 | Control correlation용 attempt ID는 허용 | Reflection/unit tests |
| Runtime·제품 | `미연결` | `cmd/server`·scheduler·startup·readiness·HTTP·사용자 경로 변화 없음 | 의도된 격리 | Source composition + clean CI |

### 실제 결과 상세

- Implementation commit: `a340e8b` (`feat: orchestrate cleanup terminal outcomes`)
- Public boundary: `PhaseExecutor.Execute(context.Context, CleanupExecutionQuery) (ExecutionResult, error)`는 바꾸지 않았다. Terminal intent discriminant, source, exact query·command·fence binding과 seal은 package-private다.
- Success authority: `manifest_absence_confirmed/revision 7`, 두 signed confirmed-absent evidence, non-unknown outcomes와 exact immutable binding만 finalization intent가 된다.
- Failure authority: Origin이 확인된 provider/auditor typed failure 또는 durable raw/manifest `unknown`의 stored class만 disposition intent가 된다.
- Exhaustive mapper: 10개 recognized class 중 정확히 하나일 때만 routing한다. 서로 다른 class의 `errors.Join`, recognized residue가 없는 error, custom `Is` sentinel 사칭과 arbitrary internal error는 fail-closed한다.
- Inventory boundary: 정상 구조의 incomplete/truncated/missing-pass는 `ErrCleanupExecutionInventoryIncomplete`지만 duplicate, identity mismatch와 malformed inventory는 unavailable/invalid로 남는다.
- Persistence barrier: Provider outcome이나 signed audit persistence가 성공했는지 알 수 없으면 terminal을 추측하지 않고 same-invocation mutation을 0으로 유지한다.
- Same-store production composition: Concrete constructor는 phase runner의 control store와 terminal mutator가 동일 `FirestoreAdmissionStore`인 경우에만 생성된다.
- Mutation budget: Finalizer 또는 disposition 하나를 original parent context로 정확히 한 번 호출한다. 오류, conflict, timeout과 correlation 결과는 mutation replay나 반대 terminal 선택 사유가 아니다.
- Correlation: Error와 valid pre-state query가 함께 있을 때만 parent value를 보존하고 cancellation/deadline을 제거한 최대 5초 context로 read-only resolver를 호출한다.
- Bounded result: Attempt ID, phase/revision, terminal kind/status, bounded class, evidence hash와 cursor timestamp만 반환하고 raw provider error·path·Firebase UID·user/device/trip/person identity record·payload·좌표는 제외한다.
- Emulator race: Success finalizer가 stale disposition과 경쟁해 attempt·receipt·두 uniqueness index만 동일 commit lineage로 갱신했다. Immutable target은 유지되고 retry·hold residue는 없었다.
- Validation data: Synthetic control documents와 Firebase demo Firestore Emulator만 사용했다. 실제 GPS, 사용자·기관 데이터, staging·production Firebase/GCS와 actual object delete는 사용하거나 변경하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| R8j sealed terminal orchestration 전체 | [EVD-20260723-040](../../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration) | `verified` — local unit/race, terminal-store Emulator, clean CI | Codex + independent review / 2026-07-23 |
| Failure disposition 선행 경계 | [EVD-20260723-039](../../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition) | `verified` — local full gate, Emulator, clean CI | Codex + independent review / 2026-07-23 |
| Success finalizer 선행 경계 | [EVD-20260722-038](../../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Durable phase execution 선행 경계 | [EVD-20260722-037](../../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution) | `verified` — local/Emulator/testbench/clean CI | Codex + independent review / 2026-07-22 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0032](../../decisions/ADR-0032-bounded-cleanup-terminal-orchestration.md)
- 실제 제품 업데이트: [UPD-20260723-06](../../product-updates/UPD-20260723-06-cleanup-terminal-orchestration.md) — local control-plane component이며 배포·사용자 화면 변화 없음
- 인시던트: 해당 없음 — synthetic local/Emulator 검증이며 production·staging·field 영향이 없다.
- 열린 위험: Concrete Firestore/GCS vertical slice, operator hold release, held/accepted/rejected cleanup, nested purge, scheduler/startup/readiness, metrics, staging IAM·bucket lifecycle·writer exclusion이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8 runtime 연결 전 operator hold release·bounded purge contract 또는 concrete terminal vertical slice
- 실제 상태를 반영한 다음 검증: 동일 Firestore control store와 fake/pinned provider를 연결해 success·retry·hold·response-loss를 `TerminalOrchestrator.Run`에서 end-to-end 재현하고 write set·provider call count를 확인한다.
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
- [x] Local orchestration을 runtime·actual deletion 또는 현장 성과로 표현하지 않았다.
- [x] Terminal-store Emulator 경쟁과 full orchestrator vertical slice를 구분했다.
- [x] Correlation query를 mutation·provider 권한으로 표현하지 않았다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 제품 업데이트·인시던트·회의 상태를 각각 명시했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 리포트 내용을 검토했다.
