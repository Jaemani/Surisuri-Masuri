---
id: HR-20260722-25
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8d - fenced cleanup execution ledger persistence foundation
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Fenced cleanup execution ledger persistence foundation

## 한눈에 보기

- 이번 회차의 사전 목적: Immutable cleanup target 이후의 delete dispatch·RPC outcome·absence audit을 exact cleanup attempt의 단조 원장으로 보존하고 crash·replay가 임의의 완료 증거를 만들지 못하게 한다.
- 보고 기준일의 실제 상태: Commit `83c2415`에서 pure ledger를, `005091e`에서 Firestore codec·transaction persistence를 구현했다. Test clock fixture를 `c00ad64`와 `e757526`에서 정정한 뒤 local Go race/vet, Firebase demo Firestore Emulator와 clean CI `29918384281`을 통과했으며 결과는 [EVD-20260722-034](../../evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence)에 기록한다.
- 가장 중요한 차이 또는 위험: Generic progress API는 안전상 absence-confirmed 단계를 저장하지 않는다. 별도 read-only absence-audit capability, phase executor와 terminal finalizer가 없으므로 아직 receipt를 `expired`로 만들 수 없다.
- 사람에게 필요한 결정·확인: Staging GCS 정책과 destructive runtime 승인 전까지 scheduler/startup 연결과 실제 object 삭제를 계속 금지한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8d cleanup execution ledger
- 계획한 기술 주제: Event-sourced progress, exact fence binding, crash-safe replay, tamper-resistant Firestore codec, response-loss-safe completion foundation
- 예상 산출물: Pure ledger·validator, Firestore DTO codec, planned initialization과 progress mutation transaction, unit·race·Emulator gate
- 검토할 질문: Caller가 phase를 건너뛸 수 없는가, absence를 command만으로 위조할 수 없는가, response replay가 중복 write를 만들지 않는가, partial residue가 fail-closed하는가
- 계획 완료 조건: Persistence foundation을 local/Emulator에서 검증하고 provider execution·absence audit·terminal finalizer는 각각 별도 capability로 남긴다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Pure execution ledger | `검증됨` | Canonical plan, monotonic phase/revision, raw-before-manifest와 7일 audit window 검증 | Retry·hold terminal policy 미구현 | WSL2 Docker Go unit/race |
| Firestore codec | `검증됨` | Bool presence, outcome timestamp와 partial/tampered residue fail-closed | 기존 문서 backfill 정책 미정 | local unit/race |
| Firestore persistence | `검증됨` | Fresh preflight+mutation revalidation, planned/raw-dispatch replay write 0 | Lease expiry·delete outcome은 unit/fake transaction, staging ADC/IAM은 미검증 | local unit/race + Firebase demo Firestore Emulator |
| Absence evidence | `의도적 차단` | Generic API가 두 absence-confirmed phase를 항상 거부 | 별도 read-only capability 필요 | local unit/race |
| Completion/runtime | `미착수` | Receipt·index·attempt terminal mutation과 worker 연결 없음 | 후속 R8d 범위 | executable 미연결 |

### 실제 결과 상세

- Implementation commits: `83c2415` (`feat: define fenced cleanup execution ledger`), `005091e` (`feat: persist fenced cleanup progress`)
- Validation fixes: `c00ad64` (`test: align cleanup expiry clock fixture`), `e757526` (`test: remove cleanup classifier wall-clock expiry`)
- Canonical binding: Authoritative receipt·exact started cleanup attempt·immutable target에서 request, target hash, plan hash, fence, receipt revision과 expected paths를 결박한다.
- State machine: `planned`에서 raw dispatch/outcome/absence, manifest dispatch/outcome/absence, `completed` 순서만 허용하고 phase별 expected revision을 고정한다.
- Outcome separation: Delete RPC 결과와 complete-empty audit 결과는 다른 enum·timestamp로 저장한다. `unknown`, `not_found_observed`와 `confirmed_absent`를 교환하지 않는다.
- Verified-empty: Target이 path를 대상으로 하지 않더라도 `targeted=false`를 명시하고, fresh audit가 승인한 경우만 `not_attempted + confirmed_absent`로 수렴하도록 domain 표현을 고정했다.
- Durable schema: Cleanup attempt DTO에 target/plan hash, execution revision/phase, raw·manifest target 여부, dispatch·outcome·audit와 각 timestamp, disposition/error/evidence field를 추가했다.
- Residue isolation: Forward started/failed/outcome validator가 cleanup ledger 잔여 field를 거부하며, cleanup codec도 일부 field 누락·foreign query·terminal residue를 거부한다.
- Transaction boundary: Read-only preflight로 current deadline을 구한 뒤 deadline-bound context를 만들고, 실제 transaction에서 receipt·두 uniqueness index·attempt·target을 다시 읽어 current fence와 immutable binding을 검사한다.
- Replay: Exact expected revision과 semantic command가 이미 적용된 경우 write 없이 저장 상태를 반환한다. 그보다 앞뒤 phase를 replay라고 추정하지 않는다.
- Lease/error: Exclusive lease expiry는 unauthorized/write 0, caller cancellation은 그대로 보존하고 Firestore 내부 timeout은 unavailable로 닫는다.
- Security gate: Generic `RecordCleanupExecutionProgress`는 `raw_absence_confirmed`와 `manifest_absence_confirmed`를 command shape만으로 받지 않는다. 이로 인해 current foundation만으로 manifest dispatch나 terminal success를 조작할 수 없다.
- Validation correction: 첫 CI의 exact-expiry fixture는 application/read clock을 서로 다르게 고정해 expected unauthorized보다 앞선 coherence gate에서 unavailable을 반환했다. Coherent expiry와 incoherent clock을 분리했고, wall-clock을 지난 classifier I/O fixture도 current-time case로 격리했다. 실패 실행은 성공 증거로 사용하지 않았다.
- Data 유형: Synthetic receipt·attempt·target과 Firebase demo project만 사용했다. 실제 GPS, 사용자, 기관, staging/production Firebase·GCS 데이터는 사용하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Pure ledger와 Firestore progress persistence | [EVD-20260722-034](../../evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Generation-pinned delete와 audit 선행 경계 | [EVD-20260722-033](../../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit) | `verified` — local/Emulator/pinned testbench/clean CI | Codex + independent final review / 2026-07-22 |

근거가 없는 staging·production·field 성과와 실제 사용자·기관 결과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0026](../../decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)
- 실제 제품 업데이트: 해당 없음 — persistence component가 executable·scheduler·사용자·운영 경로에 연결되지 않았다.
- 인시던트: 해당 없음 — 검증 중 수정은 local uncommitted 범위였고 production·staging·field 영향이 없다.
- 열린 위험: Absence-audit capability, phase-specific GCS executor, progress-bearing takeover, atomic expired finalizer, three-state response-loss correlation, retry·hold persistence, scheduler/startup/runtime 연결이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: Fresh read-only absence-audit capability와 phase-specific cleanup execution
- 실제 상태를 반영한 다음 검증: Current receipt·attempt·target·expected path에 결합된 짧은 read grant가 fresh regular/soft-deleted complete-empty inventory만 durable absence evidence로 승인하는지 확인
- 필요한 사람의 결정·지원: Staging GCS versioning·soft-delete·lifecycle·retention·IAM과 삭제 복구 창을 승인하기 전 actual runtime delete를 활성화하지 않는다.

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
- [x] GitHub clean CI 결과를 EVD-20260722-034에 최종 반영했다.
- [ ] 사람이 리포트 내용을 검토했다.
