---
id: HR-20260722-24
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8c - generation-pinned cleanup delete and complete-empty audit
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Generation-pinned cleanup delete와 complete-empty audit

## 한눈에 보기

- 이번 회차의 사전 목적: Immutable dry-run target을 그대로 삭제 권한으로 쓰지 않고, current Firestore fence에서 짧게 재승인한 exact generation만 raw→manifest 순서로 삭제·감사한다.
- 보고 기준일의 실제 상태: Commit `0d6ad55`에서 concrete Firestore delete grant, generation+metageneration-pinned GCS delete backend와 complete regular/soft-deleted empty audit를 local component로 구현했다. Local Go race, Firebase demo Firestore Emulator, pinned official Storage testbench와 clean CI 결과는 [EVD-20260722-033](../../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit)에 기록한다.
- 가장 중요한 차이 또는 위험: Full success observation은 shape-only이며 receipt `expired`나 attempt completion 권한이 아니다. Outcome ledger, staging bucket policy 검증과 runtime 연결은 아직 없다.
- 사람에게 필요한 결정·확인: Staging versioning·soft-delete·lifecycle·retention·IAM과 복구 drill을 승인하기 전 실제 환경 delete와 scheduler/startup 연결을 금지한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8 expiry cleanup
- 계획한 기술 주제: Destructive capability separation, exact conditional mutation, raw-first crash boundary, version-aware absence audit, bounded provider taxonomy
- 예상 산출물: Cleanup execution plan·grant·observation contract, concrete Firestore authorizer, GCS executor, unit/Emulator/official testbench gate
- 검토할 질문: Fake store나 zero grant가 삭제를 열 수 없는가, timeout/404/soft-delete를 완료로 오인하지 않는가, raw가 불확실하면 manifest call이 0인가
- 계획 완료 조건: Local component와 synthetic provider boundary만 검증하고 durable completion·runtime·staging gate는 별도 단계로 남긴다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Destructive grant | `검증됨` | Concrete Firestore store만 current receipt·attempt·target에서 30초 이하 grant 발급 | Staging IAM/ADC 미검증 | local unit/race + Firebase demo Firestore Emulator |
| Exact delete | `검증됨` | Path·generation·metageneration 조건으로 target generation 하나만 삭제 | Full version/soft-delete staging 의미 미검증 | local scripted provider + pinned official testbench |
| Raw-first audit | `검증됨` | Raw complete-empty 뒤에만 manifest 단계; raw unknown/error는 manifest delete 0 | Durable crash ledger 미구현 | local unit/race |
| Missing counterpart | `검증됨` | Raw-only의 manifest 후속 감사, manifest-only의 raw 선행 감사 | Accepted/rejected origin 미구현 | local unit/race |
| Error taxonomy | `검증됨` | Inspect/delete 404, timeout/cancel/unavailable, permission/quota/412와 drift 분리 | 실제 네트워크 장애 drill 미검증 | local unit + official testbench의 404/412 |
| Completion/runtime | `미착수` | Success observation은 non-authoritative shape; receipt·attempt write 0 | 의도적 차단 | executable 미연결 |

### 실제 결과 상세

- Commit: `0d6ad55` (`feat: execute generation-pinned cleanup deletes`)
- Current authorization: Linked receipt/index, exact `started` cleanup attempt와 deterministic target을 한 Firestore transaction snapshot에서 읽고 target/receipt revision, owner, token과 lease expiry를 다시 결합한다.
- Grant boundary: Policy·checked/expiry time·revision·owner·fence·lease와 plan hash는 비공개 grant field와 capability seal에 묶인다. Zero grant와 forged/stale grant는 provider call 0이다.
- Clock/TTL: Firestore read time이 local clock보다 5초 이내 앞선 경우를 허용하되 이후 정상 시간 경과는 skew로 오인하지 않는다. Grant는 30초 TTL과 lease expiry 중 이른 exclusive deadline까지만 유효하다.
- Inventory boundary: Regular와 soft-deleted query가 모두 performed, complete, non-truncated이고 bounded candidate인지 확인한다. Path·SHA-256·size·generation·metageneration·soft flag와 duplicate generation을 검사한다.
- Lineage boundary: Candidate와 inspect snapshot의 path·SHA-256·CRC32C·size·generation·metageneration 전체가 target pin과 같아야 한다.
- Delete boundary: Latest/prefix/bulk surface 없이 exact generation handle과 generation/metageneration match 조건으로만 삭제한다.
- 순서: Raw preflight→inspect→delete→fresh complete-empty audit가 성공해야 manifest를 처리한다. Target에 없는 counterpart expected path도 별도 감사한다.
- Ambiguous response: Timeout·provider cancellation·unavailable은 가능한 경우 empty audit를 수행하지만 bounded error를 반환해 manifest로 진행하지 않는다. Permission·quota·412는 즉시 fail-closed한다.
- 404: Inspect 404는 fresh inventory를 다시 읽고, delete 404는 `not_found_observed`로 보존한다. 두 경우 모두 complete-empty 없이는 성공이 아니다.
- Soft-delete/late generation: Target generation이 soft-deleted이거나 같은 path에 다른 generation이 있으면 원 target을 바꾸거나 새 generation을 선택하지 않는다.
- Observation: 두 expected path가 `confirmed_absent`일 때만 plan/target hash-bound full observation을 반환한다. 공개 shape validator는 capability나 durable completion 증거가 아니다.
- Emulator: Missing/malformed target, stale revision/fence, terminal attempt와 current happy path가 expected zero/valid grant로 수렴했다. 기존 concurrent target create/replay·conflict write-zero suite도 함께 통과했다.
- Official testbench: Wrong metageneration 412에서 raw가 유지됐고 exact delete 뒤 manifest는 유지됐다. 같은 raw generation 재삭제는 404였다. Test가 만든 synthetic object 외에는 삭제하지 않았다.
- Review correction: Generic fake store의 destructive grant 발급, self-sealed result, TTL을 5초로 축소하던 clock check와 raw unknown 뒤 manifest 진행을 구현 중 발견·수정했다. 모두 uncommitted local 상태였고 독립 최종 리뷰의 남은 finding은 없었다.
- Data 유형: Synthetic receipt·attempt·target·artifact generation과 Firebase demo project만 사용했다. 실제 GPS·사용자·복지관 데이터와 실제 staging/production object는 사용하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Generation-pinned delete와 complete-empty audit | [EVD-20260722-033](../../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit) | `verified` — local/Emulator/pinned testbench/clean CI | Codex + independent final review / 2026-07-22 |
| Sealed classification과 immutable target 선행 경계 | [EVD-20260722-032](../../evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target) | `verified` — local/Emulator/clean CI | Codex + delegated review / 2026-07-22 |

근거가 없는 staging·production·field 성과와 실제 사용자·기관 결과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0025](../../decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md), [ADR-0024](../../decisions/ADR-0024-immutable-cleanup-dry-run-target.md)
- 실제 제품 업데이트: 해당 없음 — delete/audit component가 executable·scheduler·사용자·운영 경로에 연결되지 않았다.
- 인시던트: 해당 없음 — 검증 중 결함은 uncommitted local 범위에서 발견·수정됐고 production·staging·field 영향이 없다.
- 열린 위험: Durable outcome ledger, target state update, cleanup attempt completion/failure, lease renewal/release, receipt `expired`, purge와 staging bucket drill이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8d response-loss-safe cleanup outcome ledger와 fresh fenced completion transaction
- 실제 상태를 반영한 다음 검증: Delete result를 full observation만으로 신뢰하지 않고 current target·receipt revision·fence 아래 durable하게 기록하며, crash/retry가 중복 mutation 없이 수렴하는지 확인
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
- [x] GitHub clean CI 결과를 EVD-20260722-033에 최종 반영했다.
- [ ] 사람이 리포트 내용을 검토했다.
