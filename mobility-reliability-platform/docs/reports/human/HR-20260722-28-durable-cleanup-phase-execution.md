---
id: HR-20260722-28
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8g - durable artifact-phase cleanup execution
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Durable artifact-phase cleanup execution

## 한눈에 보기

- 이번 회차의 사전 목적: Cleanup delete를 raw·manifest artifact별 durable phase로 나누고, 실제 mutation winner 한 명만 provider를 호출하며 모호한 결과가 다음 단계로 전파되지 않게 한다.
- 보고 기준일의 실제 상태: Commit `760d045`에서 dispatch transaction, single-artifact GCS executor, durable outcome과 signed audit를 연결하는 phase executor를 구현했다. Local full gate와 clean CI `29933231628`이 통과했고 근거는 [EVD-20260722-037](../../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution)에 기록한다.
- 가장 중요한 차이 또는 위험: Delete timeout뿐 아니라 검증 불가능한 provider 응답도 `unknown`으로 보존한다. `unknown` 뒤에는 absence audit와 counterpart manifest 호출이 모두 0이다.
- 사람에게 필요한 결정·확인: Terminal finalizer·response-loss correlation·retry/hold가 구현되기 전에는 runtime과 actual delete를 활성화하지 않는다. Staging IAM과 bucket 정책 승인도 별도 gate다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8g phase-bound cleanup execution
- 계획한 기술 주제: Durable dispatch, single-winner capability, one-artifact mutation surface, ambiguous outcome preservation, signed absence sequencing, deadline split
- 예상 산출물: Artifact execution request/grant, Firestore dispatch/outcome transaction, raw/manifest single executor, phase orchestrator와 race/Emulator tests
- 검토할 질문: Provider mutation 전에 dispatch가 durable한가, replay caller가 zero grant인가, raw unknown 뒤 audit·manifest가 0인가, delete deadline을 넘긴 unknown을 저장할 수 있는가, response-unverifiable이 유실되지 않는가
- 계획 완료 조건: Local race 20회, full Go race/vet, Firestore Emulator concurrency/write-zero, official Storage testbench, workspace check/build/test, container fail-closed smoke와 clean CI를 통과한다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Durable dispatch | `검증됨` | Exact target/plan/receipt/fence/phase revision에 결합된 dispatch를 transaction commit하고 applied winner만 non-zero grant 수신 | Dispatch response-loss correlation은 R8h | WSL2 Docker Go + Firebase demo Firestore Emulator |
| Artifact executor | `검증됨` | Raw 또는 manifest 하나의 inventory·inspect·conditional delete만 수행, counterpart·audit surface 없음 | Actual staging GCS delete는 미실행 | scripted unit/race + official Storage testbench backend |
| Unknown preservation | `검증됨` | Timeout/cancel/unavailable/deadline crossing/response-unverifiable을 bounded `unknown`으로 반환·저장 | Durable ledger에는 ErrorClass 세부값을 아직 저장하지 않음 | unit/race + Firestore Emulator |
| Phase sequencing | `검증됨` | Raw dispatch→outcome→signed audit 뒤에만 manifest dispatch, unknown 또는 replay dispatch에서 안전 정지 | Terminal finalizer는 미구현 | cleanupflow orchestration tests |
| Deadline boundary | `검증됨` | Mutation 30초 상한과 5초 outcome grace 분리, trusted mutation start/completion과 Firestore effective-time gate, exact expiry write 0 | 최소 post-commit provider 잔여시간은 후속 측정 | deterministic clock tests + unit/race |
| Runtime·제품 | `미연결` | `/healthz=200`, `/readyz=503`, ingest `503`; startup·scheduler·사용자 경로 변화 없음 | 의도된 fail-closed | local container + clean CI |

### 실제 결과 상세

- Implementation commit: `760d045` (`feat: execute cleanup artifacts by durable phase`)
- Clean CI: [29933231628](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29933231628), 6분 17초, 전체 job 성공
- Request binding: Target/plan hash, receipt revision, current fence, pre-dispatch revision, artifact, path와 exact lineage를 canonical hash로 묶었다.
- Winner rule: Firestore transaction `applied` caller만 capability를 받고 exact replay는 update time 불변·zero grant·provider call 0이다.
- One-artifact port: GCS executor는 artifact 하나만 처리하고 absence auditor나 counterpart path를 호출할 수 없다.
- Time split: Mutation deadline 뒤 5초 outcome persistence grace를 별도 봉인했다. Mutation start는 deadline 전, completion과 trusted Firestore persistence time은 outcome deadline 전이어야 한다.
- Single completion clock: Provider 반환 직후 completion time을 한 번만 읽어 boundary 판정과 result에 같이 사용한다.
- Ambiguous result: `ErrArtifactResponseUnverifiable`을 `response_unverifiable` error class의 `unknown`으로 보존했다.
- Safety stop: Durable raw unknown 뒤 signed audit와 manifest dispatch/delete가 0이고, durable manifest unknown 뒤 finalization readiness로 전진하지 않는다.
- Final boundary: 성공해도 `manifest_absence_confirmed`에서 `ready_for_finalization`만 반환한다. Receipt `expired`, attempt completion과 purge eligibility를 쓰지 않는다.
- Validation: Related packages race 20회, 전체 Go module/race/vet/build, Firestore Emulator admission suite, Firebase Rules 24개, mobile 65개, contracts 6 fixture, Android/iOS Expo export, official Storage testbench 4개 integration과 container smoke가 통과했다.
- Container: Local image `sha256:c7fd8e5339d33380a93eb7f32c46e94db986a759d6f4417f7a818124097c6603`, health `200`, readiness/ingest `503`.
- Independent review: 최초 review의 mutation/outcome deadline 충돌과 response-unverifiable 유실을 수정했다. 재리뷰가 찾은 known delete completion clock race도 단일 timestamp와 회귀 테스트로 수정한 뒤 blocking finding 0을 확인했다.
- Data 유형: Synthetic receipt·attempt·target·inventory와 Firebase demo project, official local Storage testbench만 사용했다. 실제 GPS, 사용자, 기관, staging/production data와 object는 읽거나 변경하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Durable artifact phase와 unknown barrier | [EVD-20260722-037](../../evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution) | `verified` — local/Emulator/testbench/clean CI | Codex + independent review / 2026-07-22 |
| Progress-aware expiry takeover 선행 경계 | [EVD-20260722-036](../../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Signed absence persistence 선행 경계 | [EVD-20260722-035](../../evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0029](../../decisions/ADR-0029-durable-artifact-phase-cleanup-execution.md)
- 실제 제품 업데이트: 해당 없음 — local control-plane component가 executable·scheduler·readiness·사용자·운영 경로에 연결되지 않았다.
- 인시던트: 해당 없음 — production·staging·field 영향 없음.
- 열린 위험: Terminal finalizer/correlation, retry·hold persistence, outcome ErrorClass durable retention, 최소 provider 실행 잔여시간, sequential GCS listing residual, staging IAM·lifecycle·soft-delete와 runtime composition이 남아 있다.

## 검증 과정에서 확인한 실패와 정정

- 첫 race 반복 명령은 gateway 하위 폴더만 Docker mount해 상위 contract fixture를 찾지 못했다. 제품 코드를 바꾸지 않고 platform root를 CI와 같은 경로로 mount해 다시 통과했다.
- 고정 과거 fixture 시각과 WSL 실제 wall clock의 context deadline이 섞여 unit test domain error가 authorization expiry로 가려졌다. Production deadline은 유지하고 test 전용 context seam을 추가해 deterministic하게 분리했다.
- 최초 grant는 mutation deadline을 outcome 저장에도 재사용해 deadline-crossing `unknown`을 거부했다. Mutation과 5초 persistence grace를 분리하고 Firestore trusted-time gate를 추가했다.
- `ErrArtifactResponseUnverifiable`이 zero result로 유실됐다. Bounded `response_unverifiable + unknown`으로 보존하고 audit·manifest zero-call test를 추가했다.
- Provider completion 직후 시각을 반복 조회해 확정된 known delete가 exact deadline 경계에서 유실될 수 있었다. Completion time을 한 번만 캡처해 판정과 result가 공유하도록 수정했다.
- 위 항목은 local 구현·검증 과정이며 production·staging·field runtime, 실제 사용자와 기관 운영에 영향이 없어 Incident 기준에 해당하지 않는다.

## 다음 회차

- 8개월 계획상 다음 주제: Terminal expiry finalizer와 commit response-loss correlation
- 실제 상태를 반영한 다음 검증: `manifest_absence_confirmed`에서만 attempt completed·receipt expired·purge eligibility를 원자 commit하고, 응답 유실 뒤 read-only query가 `committed|not_committed|unverifiable`로 수렴하는지 확인한다.
- 필요한 사람의 결정·지원: Staging GCS writer identity·lifecycle·soft-delete·retention과 restore drill 전에는 actual cleanup runtime을 활성화하지 않는다.

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
- [x] Delete response를 absence proof나 terminal completion으로 표현하지 않았다.
- [x] Unknown 뒤 audit·manifest 호출 0과 runtime 미연결을 명시했다.
- [x] Synthetic·Emulator·testbench와 staging·production·field를 구분했다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보, object path와 GPS 좌표가 없다.
- [ ] 사람이 리포트 내용을 검토했다.
