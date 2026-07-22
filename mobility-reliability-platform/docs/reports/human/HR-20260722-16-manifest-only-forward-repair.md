---
id: HR-20260722-16
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R6 two-pass forward reconciliation partial implementation
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Manifest-only forward repair 경계

## 한눈에 보기

- 이번 회차의 사전 목적: 누락된 canonical manifest를 정상 raw generation의 재업로드 없이 복구하고, fresh authorization과 두 번째 분류 전에는 receipt를 stored로 확정하지 않는 R6 경계를 구현·검증한다.
- 보고 기준일의 실제 상태: phase-aware planner, pass-1 evidence와 current state에 결합된 write-only capability, GCS manifest-only create와 unit-level exact replay 경계가 `main` commit `db2d2e8`에 구현됐다. WSL2 local 전체 Go gate, pinned official Storage testbench, 독립 재리뷰와 clean CI를 통과했다.
- 가장 중요한 차이 또는 위험: R6 전체가 끝난 것은 아니다. Firestore final action grant/store, transaction 내부 동의 재평가와 attempt completion, outcome-read capability는 남아 있다. Official testbench는 soft-delete inventory를 지원하지 않아 exact replay success는 staging GCS에서 별도 검증해야 한다.
- 사람에게 필요한 결정·확인: 현재는 startup·worker·readiness에 연결하지 않는다. 다음 증분의 Firestore action transaction이 끝난 뒤 staging bucket·IAM 검증 범위를 승인할 필요가 있다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 공간·텔레메트리 파이프라인 구현 중, fail-closed 복구 인프라의 R6 증분
- 계획한 기술 주제: two-pass recovery, immutable conditional create, short-lived capability, generation-pinned replay, cancellation/deadline semantics
- 예상 산출물: ADR-0020, pure planner, manifest-only recovery port, GCS adapter, synthetic unit/race와 official testbench evidence
- 검토할 질문: raw를 쓰는 API 없이 manifest만 복구할 수 있는가, renewal·consent withdrawal·provider ambiguity 뒤 stale 결과가 terminal receipt로 승격되지 않는가
- 계획 완료 조건: provider-neutral planner·manifest write 검증뿐 아니라 Firestore final action/attempt transaction, Emulator races, staging Storage semantics까지 완료해야 한다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Phase-aware planner | `검증됨` | initial/confirmation/post-manifest phase와 exact prior pins에 따라 create/confirm/stored/rejected/hold/release 후보를 분리 | Firestore mutation은 아직 없음 | local synthetic |
| Manifest write authorization | `검증됨` | pass-1 valid_raw_only evidence, exact raw pin과 fresh current request가 같을 때만 short-lived write grant 발급 | runtime authorizer wiring 없음 | local synthetic |
| GCS manifest-only create | `부분 검증` | raw body/write API 없이 manifest path에만 DoesNotExist create; attrs/digest 검증 | actual GCS IAM·lifecycle 미검증 | memory backend + official testbench |
| Exact replay | `부분 검증` | complete inventory와 generation-pinned exact bytes에서만 replay success | official testbench가 soft-delete query를 지원하지 않아 success는 unit 범위만 확인 | local synthetic |
| Deadline·cancel | `검증됨` | caller cancel, provider cancel, capability expiry를 분리하고 provider call 전후·최종 success 전 재검증 | 실제 네트워크 장기 지연 미검증 | local synthetic/race |
| Final receipt action | `미착수` | action matrix와 원자성 규칙만 ADR에 존재 | 다음 구현 증분 | 해당 없음 |

### 실제 결과 상세

- 결과: 정상 raw pin을 다시 쓰거나 삭제하지 않는 manifest-only mutation surface와 stale evidence를 거부하는 capability boundary를 구현했다.
- 관측 수치: targeted package 2개와 gateway 전체 8개 Go package가 WSL2 Docker `-race`에서 통과했다. Official testbench integration 3개가 통과했다. 성능·latency 수치는 측정하지 않았다.
- 데이터 유형: `synthetic`, `testbench`; field data 없음
- 알려진 제한: staging GCS version/soft-delete semantics, Firestore final action, runtime orchestration과 실제 사용자 경로는 검증하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Planner·manifest capability·GCS boundary와 전체 gate | [EVD-20260722-025](../../evidence/2026-07.md#evd-20260722-025--two-pass-forward-recovery-planner와-manifest-only-repair-boundary) | `verified` — local/testbench/clean CI | Codex + delegated review / 2026-07-22 |
| Current-state forward authorization 선행 경계 | [EVD-20260721-024](../../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization) | `verified` | WSL2 Docker + Firestore Emulator |
| Generation-pinned classifier 선행 경계 | [EVD-20260721-023](../../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier) | `verified` | WSL2 Docker + official testbench |

근거가 없는 field·staging·production 성과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0020](../../decisions/ADR-0020-two-pass-forward-reconciliation.md)
- 실제 제품 업데이트: 해당 없음 — startup·worker·사용자 흐름에 연결하지 않았다.
- 인시던트: 해당 없음 — 확인된 실패는 local 구현·testbench 한계이며 production·staging·field 영향이 없다.
- 열린 위험: actual GCS soft-delete/version inventory, final transaction 원자성, commit-response loss와 stale attempt closure가 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: Firestore final action capability와 receipt action·attempt completion의 원자 transaction
- 실제 상태를 반영한 다음 검증: action/phase/class/reason/pins/fence/revision binding, transaction 내부 current consent 재평가, stored/rejected/hold/release 경쟁, commit 응답 유실 뒤 read-only outcome 조회
- 필요한 사람의 결정·지원: 다음 증분까지 worker·scheduler·readiness를 계속 차단한다. 이후 staging GCS bucket/IAM 실험을 별도 승인 범위로 검토한다.

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
- [x] 합성·testbench와 field 데이터를 구분했다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·EVD를 원문으로 링크했다.
- [ ] 사람이 리포트 내용을 검토했다.
