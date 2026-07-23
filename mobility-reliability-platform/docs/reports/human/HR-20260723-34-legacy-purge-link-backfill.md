---
id: HR-20260723-34
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: R8k-c partial - cleanup target inverse registry and legacy backfill
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Cleanup target inverse registry와 legacy backfill

## 한눈에 보기

- 이번 회차의 사전 목적: 기존·신규 cleanup target이 receipt purge에서 누락되지 않도록 inverse registry와 fail-closed migration 경계를 만든다.
- 보고 기준일의 실제 상태: 새 target+link atomic create와 legacy cleanup target tenant-wide bounded backfill을 local·Firebase demo Emulator에서 구현했다. Parent receipt와 target의 reservation/cleanup context까지 strict 검증하며 integrity finding이 존재하면 지원하지 않는 상태로 write 0 중단한다.
- 가장 중요한 차이 또는 위험: R8k-c 전체 중 registry와 cleanup-target backfill만 진행됐다. Finding schema/writer, link+child delete, final linkage와 production inventory는 없다.
- 사람에게 필요한 결정·확인: 실제 migration 전에 Admin/IAM writer 목록과 integrity finding 실제 존재 여부·schema 소유자를 확정해야 한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8k-c metadata lifecycle gate
- 계획한 기술 주제: Deterministic inverse registry, child+link atomic create, legacy target inventory, exact transaction revalidation, poison hold와 migration evidence
- 예상 산출물: Link/legacy page contracts, Firestore adapters, Emulator concurrency·negative tests, 제품 업데이트와 증거 문서
- 검토할 질문: `receipt_id`가 없는 문서를 놓치는가, target-only/link-only partial state가 가능한가, stale page가 cursor를 전진시키는가, corrupt parent에 link를 붙이는가, purge fence 뒤 missing link를 보충하는가, finding schema를 추정하는가
- 계획 완료 조건: Registry writer와 cleanup target backfill이 local gate를 통과하고, 아직 미구현인 finding/delete/final linkage를 명시적으로 분리한다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Inverse link contract | `local 검증됨` | Kind+document ID deterministic link, strict snapshot/parent/pair binding | Finding ID는 safe legacy ID만 허용하나 writer 없음 | Go pure/race |
| 신규 target writer | `local 검증됨` | Target와 receipt 하위 link를 same transaction create; exact replay write 0 | Runtime 미연결 | Go fake seam + Firestore Emulator |
| Legacy discovery | `local 검증됨` | Tenant-wide `__name__ ASC`, page 25+lookahead; `receipt_id` query 미사용 | Point-in-time/global scan 아님 | Go contract + Emulator |
| Backfill transaction | `local 검증됨` | Current page exact 재조회, target·receipt·link reread 뒤 missing link만 create | Durable operator checkpoint 없음 | Go unit/race + Emulator |
| Strict binding | `local 검증됨` | Target shape/hash/doc ID/tenant와 parent base state·reservation·cleanup transition, link pair 검증 | Actual Admin/IAM writer exclusion 미검증 | Emulator poison matrix |
| Finding gate | `local 검증됨` | Name-only limit(1)이 nonempty면 unsupported/write 0 | Finding backfill은 미구현 | Firestore Emulator |
| Concurrency | `local 검증됨` | Same advisory page workers가 created 1/replayed 1로 수렴 | 다중 target page rollback 직접 fault injection은 후속 | Firestore Emulator |
| 전체 local gate | `통과` | Go vet/race/build, workspace check/build/test, Android/iOS export, Rules 24 tests와 clean CI run 29967356380 | 사람 검토는 발행 전 별도 필요 | WSL2 Docker + local workspace + GitHub Actions |
| Runtime·제품 | `미연결` | Operator CLI·HTTP·scheduler·startup/readiness 변화 없음 | 의도된 격리 | Source composition |

### 실제 결과 상세

- Implementation commits: `bf5b95f`, `9f768d9`, `49e402c`, `0a2fad4`.
- Backfill request는 UUID tenant, optional UUID cursor와 page size 1~25만 허용한다. Page의 `ObservedExhausted`와 count는 해당 관측 의미이며 global completion이나 applied count가 아니다.
- Advisory page는 commit transaction 안에서 같은 query로 다시 만들어 exact page·lookahead가 다르면 conflict/write 0이다.
- Malformed safe document ID도 tenant-wide name query에 나타나며 field 누락 때문에 숨지 않는다. Strict target decoder 실패 시 whole-page hold다.
- Parent receipt는 synthetic index projection으로 base key derivation과 receipt lifecycle을 검증하고 target의 reservation key·cleanup mode/origin/policy/transition/quiescence를 결합한다.
- Existing link drift, foreign target, invalid parent, reservation drift와 fenced-unregistered는 link를 만들지 않는다.
- Integrity finding 저장 구현이 없으므로 존재를 발견하면 내용을 추정하지 않고 explicit unsupported로 transaction을 종료한다.
- 검증에는 synthetic control document와 Firebase demo Emulator만 사용했고 실제 GPS, 사용자, 수리이력, 기관 또는 production Firebase 데이터는 사용하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Target inverse registry와 legacy backfill | [EVD-20260723-043](../../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) | `generated` — local/Emulator/clean CI 통과, 사람 검토 대기 | Codex + delegated tests + independent review / 2026-07-23 |
| 선행 receipt purge fence | [EVD-20260723-041](../../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-23 |
| 선행 nested attempt purge | [EVD-20260723-042](../../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) | `generated` — local/Emulator/clean CI, 사람 검토 대기 | Codex + independent review / 2026-07-23 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0033](../../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md) — R8k-c가 부분 구현됐으며 전체 ADR은 계속 `proposed`
- 실제 제품 업데이트: [UPD-20260723-09](../../product-updates/UPD-20260723-09-legacy-purge-link-backfill.md) — local control-plane component이며 배포·사용자 화면 변화 없음
- 인시던트: 해당 없음 — synthetic local/Emulator 검증이며 production·staging·field 영향이 없다.
- 열린 위험: Finding schema/writer/backfill, linked target/finding purge, final linkage, Rules/indexes, global writer inventory와 runtime이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8k-c bounded purgeLinks+exact child delete와 final linkage gate
- 실제 상태를 반영한 다음 검증: Finding writer 여부를 먼저 확정하고, link page의 exact child+link delete·cursor/count atomicity, fresh empty/unregistered verification, final receipt+indexes transaction을 분리한다.
- 필요한 사람의 결정·지원: Production mutation 전 Admin/IAM writer 목록, legacy finding schema·보존정책과 purge job 자체 retention을 확정한다.

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
- [x] Backfill을 finding coverage·global orphan-zero·metadata purge 완료로 표현하지 않았다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [x] Clean CI run의 최종 성공 상태를 확인했다.
- [ ] 사람이 리포트 내용을 검토했다.
