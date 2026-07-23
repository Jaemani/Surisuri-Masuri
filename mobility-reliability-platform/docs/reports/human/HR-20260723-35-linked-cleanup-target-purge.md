---
id: HR-20260723-35
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: R8k-c partial - bounded linked cleanup target purge
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Linked cleanup target의 bounded atomic purge

## 한눈에 보기

- 이번 회차의 사전 목적: Registry에 연결된 cleanup target과 inverse link를 stale page·partial delete·중복 count 없이 bounded transaction으로 제거한다.
- 보고 기준일의 실제 상태: Cleanup target+link atomic delete와 `link_cursor`, `target_deleted_count`, `revision` 동시 갱신을 local·Firebase demo Emulator에서 검증했다. Page/lookahead와 pair body를 transaction 안에서 다시 검증하며 poison 한 건이면 page 전체를 hold한다.
- 가장 중요한 차이 또는 위험: Cleanup target 경로만 구현했다. Integrity finding, global orphan-zero, `ready`, final receipt/index delete와 운영 worker는 없다.
- 사람에게 필요한 확인: Integrity finding schema·보존정책과 production Admin/IAM writer 목록을 확정하기 전에는 final linkage 단계나 운영 migration을 승인하면 안 된다.

## 8개월 로드맵 대비 현재 위치

내부 gate 이름보다 최종 제품 기준의 진행 상태를 우선한다. 2026년 7월은 5~12월 계획의 3개월차다.

| 월 | 계획한 결과 | 보고 시점 실제 상태 |
| --- | --- | --- |
| 5월 | 새 앱·데이터 구조와 휴대폰 GPS 1회 수집·지도 표시 | 새 저장소·문서·계약·앱 골격 완료. 실제 이동 경로 데모 증거 없음 |
| 6월 | Android/iPhone 백그라운드 GPS, SQLite 오프라인 보존·재동기화, 배터리·정확도 비교 | Foreground GPS와 SQLite outbox 코드·순수 테스트 완료. 백그라운드 실기기·비행기 모드 재동기화·배터리 측정 미완료 |
| 7월 | 휴대폰→서버 업로드, Go 수집 경계, 위치 정제·익명 히트맵·부하 결과 | Firebase/Go 수신 데이터의 권한·중복방지·복구·정리 안전성은 깊게 구현. 앱→서버 GPS 연결, 위치 정제 화면, H3 히트맵은 미완료 |
| 8월 | PyTorch 주행 품질 모델과 ONNX 모바일 추론 | 미착수 |
| 9월 | 전동보장구 Digital Twin과 부품 점검시점 예측 | 미착수 |
| 10월 | 근거 연결형 AI 리포트와 대상별 보고서 | 미착수 |
| 11월 | 실증·접근성·모니터링·보안 검토 | 미착수 |
| 12월 | 통합 데모·기술 백서·발표 패키지 | 미착수 |

따라서 현재 기술 기반과 서버 신뢰성은 계획보다 깊지만, 7월 말에 보여주기로 한 **휴대폰 GPS → 오프라인 보존 → 서버 업로드 → 정제 지도·히트맵** 종단간 데모는 아직 완료되지 않았다. 이번 cleanup-target 변경은 그 서버 안전성 기반의 일부이며 사용자 화면이 늘어난 결과가 아니다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8k-c metadata lifecycle gate
- 계획한 기술 주제: Bounded linked-child purge, exact transaction revalidation, pair/body integrity, atomic cursor/count와 response-loss recovery
- 예상 산출물: Pure contract, Firestore adapter, Emulator pagination·concurrency·poison·response-loss tests, Rules deny, CI selector와 증거 문서
- 검토할 질문:
  - Stale page 또는 changed lookahead에서 delete가 실행되는가
  - Target-only 또는 link-only partial delete가 가능한가
  - Concurrent worker가 count를 중복 증가시키는가
  - Malformed child를 건너뛰고 cursor가 전진하는가
  - Commit 응답 유실 뒤 mutation을 중복 실행하는가
- 계획 완료 조건: Cleanup target 경로의 bounded atomic delete를 검증하고 finding/final linkage/runtime을 명확히 분리한다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Linked page discovery | `local 검증됨` | Bounded `purgeLinks` page+lookahead 조회 | Global snapshot/inventory 아님 | Go contract + Emulator |
| Transaction revalidation | `local 검증됨` | Current job·receipt fence와 exact page/lookahead 재조회 | Runtime 미연결 | Firestore Emulator |
| Pair/body validation | `local 검증됨` | Link와 target schema/tenant/receipt/kind/document/created-at 결합 | Finding codec 없음 | Go unit/race + Emulator |
| Atomic delete/progress | `local 검증됨` | Target+link delete와 cursor/target count/revision same transaction | Finding count 변경 없음 | Firestore Emulator |
| Poison handling | `local 검증됨` | Malformed·foreign·missing·drift·unknown/finding 한 건이면 whole-page hold | Operator repair flow 없음 | Emulator poison matrix |
| Concurrency | `local 검증됨` | Same-page concurrent worker에서 single commit winner, count 중복 없음 | 다중 운영 worker 미검증 | Firestore Emulator |
| Response loss | `local 검증됨` | Exact pre/next state로 committed/not_committed/unverifiable 구분, mutation 재호출 없음 | Durable operator checkpoint 없음 | Firestore Emulator |
| Firebase client Rules | `local 통과` | Nested purgeLinks와 ingestIntegrityFindings get/list/write deny | Admin/IAM exclusion 아님 | Firebase Rules 24 tests |
| Mobile regression | `local 통과` | 기존 policy suite 유지 | 사용자 기능 변화 없음 | 65 tests |
| Combined purge gate | `local 통과` | Attempt+LegacyInventory+Linked selector 통과 | Staging/production 아님 | Firebase demo Emulator |
| Clean CI | `통과` | Source commit `3bfa29c`, run 29969434762 success | 사람 검토는 별도 | GitHub Actions |
| Runtime·제품 | `미연결` | Worker/scheduler/startup/readiness 및 사용자 화면 변화 없음 | 의도된 격리 | Source composition |

### 실제 결과 상세

- Advisory page는 mutation authority가 아니며 commit transaction 안에서 current page와 lookahead를 exact 재구성한다.
- 모든 target/link read와 strict validation이 끝난 뒤에만 page의 target과 link를 삭제한다.
- `link_cursor`, `target_deleted_count`, `revision`은 child/link delete와 같은 transaction에 포함된다.
- Poison 한 건이 있으면 해당 page의 정상 target도 삭제하지 않는다. Cursor와 count도 유지한다.
- Integrity finding은 codec·writer·delete adapter가 없으므로 내용을 추정하거나 삭제하지 않고 unsupported hold로 닫는다.
- Concurrent same-page processing은 한 worker만 commit했으며 replay 또는 loser가 target count를 다시 올리지 않았다.
- Response loss 판정은 exact pre/next job state와 target/link 존재 조합을 사용한다. Partial absence나 다른 valid winner는 `unverifiable`이며 mutation을 자동 재실행하지 않는다.
- Firebase client Rules deny와 CI linked selector를 source에 추가했다.
- Source commit `3bfa29c`의 clean CI가 workspace, Firestore·Storage, Go와 container smoke까지 통과했다.
- 모든 검증은 WSL2, Docker Go와 Firebase demo Emulator의 synthetic data로 수행했다.
- 실제 GPS, 사용자, 수리이력, 복지관 또는 staging·production 데이터를 사용하지 않았고 사용자 기능으로 배포하지 않았다.

## 3. 근거

| 실제 주장 | 증거 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Cleanup target+inverse link bounded atomic purge | [EVD-20260723-044](../../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge) | `generated` — local/Emulator/clean CI 통과, 사람 검토 대기 | Codex + independent review / 2026-07-23 |
| 선행 inverse registry와 legacy backfill | [EVD-20260723-043](../../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) | `generated` — local/Emulator/clean CI, 사람 검토 대기 | Codex + independent review / 2026-07-23 |
| 선행 nested attempt purge | [EVD-20260723-042](../../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) | `generated` — local/Emulator/clean CI, 사람 검토 대기 | Codex + independent review / 2026-07-23 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0033](../../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md) — Cleanup target linked purge만 부분 구현됐으며 전체 ADR은 계속 `proposed`
- 실제 제품 업데이트: [UPD-20260723-10](../../product-updates/UPD-20260723-10-linked-cleanup-target-purge.md) — local control-plane component이며 배포·사용자 화면 변화 없음
- 인시던트: 해당 없음 — synthetic local/Emulator 검증이며 production·staging·field 영향이 없다.
- 열린 위험: Finding codec/delete/backfill, fresh global inventory, `ready`, final receipt/index transaction, Admin/IAM writer inventory와 runtime이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: Integrity finding authority를 확정하거나 지원하지 않는 상태로 유지한 채 fresh inventory와 final linkage 조건을 분리 설계한다.
- 실제 상태를 반영한 다음 검증: Cursorless fresh scan, unregistered child 검증, final receipt+두 index transaction과 purge job retention을 각각 독립 gate로 둔다.
- 필요한 사람의 결정·지원: Production mutation 전 integrity finding schema·보존정책, Admin/IAM writer 목록과 운영 주체를 확정한다.

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
- [x] Cleanup target purge를 finding purge·global orphan-zero·final metadata purge 완료로 표현하지 않았다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 실제 사용자 기능 배포로 표현하지 않았다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [x] CI run 29969434762의 최종 성공 상태를 확인했다.
- [ ] 사람이 리포트 내용을 검토했다.
