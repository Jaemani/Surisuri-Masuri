---
id: HR-20260722-26
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8e - signed read-only cleanup absence audit
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Signed cleanup absence audit boundary

## 한눈에 보기

- 이번 회차의 사전 목적: Generic progress command로 위조할 수 없는 fresh read-only absence evidence를 만들고 exact cleanup attempt에 단조적으로 저장한다.
- 보고 기준일의 실제 상태: Commit `f46f905`에서 Firestore current-state authorization, paired GCS auditor의 Ed25519 evidence, Firestore evidence verification과 raw·manifest absence phase persistence를 구현했다. Wall-clock 의존 cleanup executor fixture는 `022d3ef`에서 정정했다. Local race/vet, Firebase demo Firestore Emulator와 clean CI 결과는 [EVD-20260722-035](../../evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence)에 기록한다.
- 가장 중요한 한계: GCS regular generation과 soft-deleted generation listing은 원자적 snapshot이 아니다. 이 결과는 quiescence, application writer fencing과 IAM write exclusion을 전제로 한 bounded sequential observation이며, staging에서 해당 전제를 검증하기 전에는 production readiness가 아니다.
- 사람에게 필요한 결정·확인: Staging bucket IAM과 모든 writer identity를 확인하고 audit 대상 prefix의 out-of-band write exclusion을 증명할 때까지 runtime, 실제 delete와 terminal `expired` finalizer를 연결하지 않는다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8e read-only absence attestation
- 계획한 기술 주제: Fresh Firestore capability, exact-path GCS inventory, opaque signed evidence, grant-bound replay protection, deadline-bound transactional persistence
- 예상 산출물: Domain request/observation contract, paired auditor/verifier, Firestore authorizer와 persistence method, unit·race·Emulator gate
- 검토할 질문: Caller가 provider read 없이 evidence를 만들 수 없는가, destructive grant와 read grant가 분리됐는가, stale fence·revision·key·binding이 write 0으로 닫히는가, 비원자적 inventory 한계가 운영 gate에 반영됐는가
- 계획 완료 조건: Raw와 manifest absence phase를 local/Emulator에서 각각 저장하고 exact replay·drift·deadline 실패를 검증하되 delete/runtime/terminal completion은 활성화하지 않는다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Current-state authorization | `검증됨` | Receipt·두 index·exact attempt·target을 fresh read하고 request hash, revision, fence, artifact와 next phase를 묶은 30초 이하 read grant 발급 | Staging ADC/IAM 미검증 | WSL2 Docker Go unit/race + Firebase demo Firestore Emulator |
| Paired provider evidence | `검증됨` | Auditor 내부 Ed25519 private key로 request·grant binding·artifact·observed-at에 결합된 opaque evidence 서명, verifier만 store에 주입 | Runtime key rotation·cross-process lifecycle 미설계 | local unit/race |
| Exact-path inventory audit | `검증됨` | Regular·soft-deleted inventory가 complete/non-truncated/empty일 때만 evidence 발급하고 generation·incomplete·permission·timeout·cancel은 거부 | 두 listing은 atomic snapshot이 아니며 staging writer exclusion 미검증 | local synthetic reader tests |
| Firestore absence persistence | `검증됨` | Raw·manifest absence phase 저장, exact replay write 0, different observation time·stale receipt/fence/ledger drift write 0 | Terminal completion과 `expired` 없음 | local unit/race + Firebase demo Firestore Emulator |
| Runtime·delete·finalization | `미연결` | Generic API는 absence phase를 계속 거부하고 새 auditor/store도 executable에 주입되지 않음 | Phase executor, retry·hold, takeover, finalizer와 scheduler 후속 범위 | executable/runtime 사용 안 함 |

### 실제 결과 상세

- Implementation commit: `f46f905` (`feat: attest read-only cleanup absence`)
- Validation fixture commit: `022d3ef` (`test: remove cleanup executor wall-clock expiry`)
- Request binding: Exact target·plan hash, receipt revision, cleanup owner/token/lease, ledger revision, next phase, artifact와 expected path를 canonical request hash로 고정한다.
- Grant boundary: Firestore transaction current state에서 발급한 grant는 30초 또는 lease expiry 중 더 이른 시각까지만 유효하며 destructive cleanup grant와 다른 concrete type이다.
- Key separation: GCS auditor constructor가 private key를 생성·보관하고 paired verifier만 반환한다. Evidence의 필드는 package 밖에서 설정할 수 없다.
- Evidence binding: Request hash, concrete grant capability seal, artifact, `confirmed_absent`와 nanosecond-resolution UTC observation time을 Ed25519 payload에 포함한다.
- Provider gate: Exact path의 regular와 soft-deleted inventory 중 어느 쪽이든 candidate, incomplete/truncated 상태 또는 bounded provider error가 있으면 evidence를 반환하지 않는다.
- Transaction gate: Evidence와 grant를 검증한 뒤 grant/lease deadline context로 Firestore transaction을 제한하고 current receipt·index·attempt·target과 phase/revision을 다시 읽는다.
- Replay: Persisted `AuditedAt`까지 같은 exact replay만 write 0으로 수렴한다. 같은 phase의 다른 observation time이나 stale revision/fence는 conflict이며 control document write는 0이다.
- Immutable control state: Absence persistence는 exact attempt만 갱신하며 receipt, 두 uniqueness index와 cleanup target의 내용·update time을 바꾸지 않는 Emulator 회귀를 통과했다.
- Known residual: Regular과 soft-deleted listing은 sequential provider calls이므로 두 호출 사이의 out-of-band writer race를 원자적으로 제거하지 못한다. `atomic snapshot` 또는 `point-in-time proof`로 주장하지 않는다.
- Data 유형: Synthetic receipt·attempt·target과 Firebase demo project만 사용했다. 실제 GPS, 사용자, 기관, staging/production Firebase·GCS data와 object delete는 사용하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Signed absence audit와 Firestore persistence | [EVD-20260722-035](../../evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| 선행 cleanup execution ledger | [EVD-20260722-034](../../evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |

근거가 없는 staging·production·field 성과, 실제 사용자·기관 결과와 원자적 provider snapshot 주장은 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0027](../../decisions/ADR-0027-paired-read-only-cleanup-absence-attestation.md)
- 실제 제품 업데이트: 해당 없음 — local component가 executable·scheduler·사용자·운영 경로에 연결되지 않았다.
- 인시던트: 해당 없음 — wall-clock test fixture 정정은 local/CI validation 범위이며 production·staging·field 영향이 없다.
- 열린 위험: Sequential inventory 사이의 out-of-band writer race, staging IAM/write-exclusion, phase executor, progress-bearing takeover, retry·hold, terminal `expired` finalizer, response-loss correlation, auditor key lifecycle과 runtime composition이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: Phase-specific cleanup executor 또는 progress-aware expired takeover를 bounded 하위 단계로 설계한다.
- 실제 상태를 반영한 다음 검증: Raw absence 뒤 manifest dispatch만 허용되는 orchestration, crash/restart에서 audit-first 재개, expired cleanup attempt의 progress-preserving takeover를 각각 분리 검증한다.
- 필요한 사람의 결정·지원: Staging GCS versioning·soft-delete·lifecycle·retention·IAM과 모든 writer identity가 확인되기 전 actual runtime delete를 활성화하지 않는다.

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
- [x] GCS listing을 atomic snapshot 또는 point-in-time proof로 표현하지 않았다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보, 원본 object path와 GPS 좌표가 없다.
- [x] 관련 ADR·EVD를 원문으로 링크했다.
- [ ] 사람이 리포트 내용을 검토했다.
