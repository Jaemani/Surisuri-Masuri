---
id: UPD-20260723-10
date: 2026-07-23
status: draft
version_or_deployment: receipt-purge-r8k-c-linked-target-local
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: Linked cleanup target의 bounded atomic purge

## 요약

Receipt 아래 inverse registry에 연결된 cleanup target을 bounded page 단위로 읽고, target과 link를 같은 Firestore transaction에서 삭제할 수 있게 됐다. Transaction은 current job·receipt fence, page·lookahead와 각 target/link body를 다시 검증한 뒤 delete와 `link_cursor`, `target_deleted_count`, `revision` 갱신을 함께 commit한다. Poison 한 건이면 page 전체를 삭제하지 않으며, commit response loss도 mutation 재시도 없이 exact state correlation으로 판정한다.

이 변경은 source commit `3bfa29c`의 local component와 Firebase demo Emulator에만 있다. 실제 사용자 기능 또는 운영 worker로 배포된 변화가 아니다.

## 변경 전 문제

- Inverse link registry와 legacy backfill은 있었지만 linked target과 link를 실제로 제거하는 bounded mutation은 없었다.
- Advisory page가 stale해지거나 lookahead가 바뀐 상태에서 cursor가 전진하면 child가 누락될 수 있었다.
- Target만 삭제되거나 link만 삭제되면 이후 orphan 검증과 재시도가 모호해질 수 있었다.
- Poison child를 건너뛰며 정상 child만 삭제하면 손상된 page의 증거가 사라질 수 있었다.
- Commit 응답 유실 뒤 같은 mutation을 재호출하면 count가 중복 증가하거나 다른 winner와 충돌할 수 있었다.

## 변경 후 동작

- `purgeLinks` page와 lookahead를 bounded 조회한다.
- Commit transaction 안에서 current job·receipt fence와 exact page·lookahead를 다시 읽는다.
- Cleanup target과 link의 schema, tenant, receipt, kind, document identity, paired `created_at`과 parent receipt context를 strict 검증한다.
- 모든 검증이 끝난 뒤 target과 inverse link를 같은 transaction에서 삭제한다.
- `link_cursor`, `target_deleted_count`, `revision`을 delete와 같은 transaction에서 갱신한다.
- Malformed·foreign·missing·identity drift·unknown kind/schema 또는 integrity finding이 있으면 whole-page hold로 닫고 child/link delete와 cursor/count 전진을 하지 않는다.
- Concurrent workers 중 한 worker만 commit하며 count를 중복 증가시키지 않는다.
- Response loss는 exact pre/next job과 target/link 존재 상태를 read-only로 비교해 판정하고 mutation을 자동 재호출하지 않는다.
- Firebase client Rules에서 nested `purgeLinks`와 `ingestIntegrityFindings` direct get/list/write를 거부한다.
- CI Emulator selector에 `FirestoreEmulatorReceiptPurgeLinked` 경로를 포함했다.

## 범위

- 포함: Cleanup target+inverse link bounded atomic delete, exact page/lookahead transaction reread, strict pair/body validation, cursor/count/revision atomic update, poison hold, concurrent single winner, response-loss correlation, Firebase client Rules deny와 CI selector.
- 제외: Integrity finding codec/delete/backfill, `ready`, fresh global orphan-zero, final receipt·두 index delete, worker/runtime/startup, staging·production migration.
- 배포 환경: `local component + Firebase demo Firestore Emulator`
- 데이터 유형: Synthetic receipt, cleanup target, inverse link와 poison control documents. GPS·사용자·기관 데이터 없음.

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 |
| --- | --- | --- | --- |
| Target+link와 progress atomicity | Go unit/race + Firestore Emulator pagination/concurrency | `local pass` | [EVD-20260723-044](../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge) |
| Strict page/lookahead와 pair/body 검증 | Emulator stale/poison matrix | `local pass` | [EVD-20260723-044](../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge) |
| Response-loss exact correlation | Emulator committed/not-committed/unverifiable cases | `local pass` | [EVD-20260723-044](../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge) |
| Firebase client Rules deny | Rules get/list/write regression | `24 tests pass` | [EVD-20260723-044](../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge) |
| Mobile regression | Local workspace test | `65 tests pass` | [EVD-20260723-044](../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge) |
| Combined purge Emulator selector | Attempt+LegacyInventory+Linked | `local pass` | [EVD-20260723-044](../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge) |
| Clean GitHub Actions | CI run 29969434762, source `3bfa29c` | `success` | [CI run](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29969434762) |

## 배포와 롤백

- Firebase/GCP staging·production, 앱스토어, 사용자·기관 환경에는 배포하지 않았다.
- Operator/runtime/startup에 연결되지 않아 실제 tenant receipt나 linked document를 삭제하지 않는다.
- Local code rollback은 source commit `3bfa29c`의 revert로 가능하다. 실제 운영 데이터 mutation이 없어 데이터 rollback은 없다.
- 향후 운영 활성화에는 finding schema·보존정책, fresh inventory, Admin/IAM writer exclusion, final linkage와 durable operator checkpoint의 별도 승인이 필요하다.

## 알려진 제한과 후속 작업

- Integrity finding link는 지원하지 않으며 page 전체를 hold한다.
- Cursor exhaustion은 global orphan-zero 증거가 아니다.
- `ready` 전환과 final receipt·두 index delete는 구현하지 않았다.
- Purge job 자체의 retention과 terminal cleanup도 정하지 않았다.
- Client Rules deny만으로 Admin SDK/IAM writer를 배제할 수 없다.

## 관련 기록

- 결정: [ADR-0033](../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md)
- 증거: [EVD-20260723-044](../evidence/2026-07.md#evd-20260723-044--bounded-linked-cleanup-target-purge)
- 사람 대상 리포트: [HR-20260723-35](../reports/human/HR-20260723-35-linked-cleanup-target-purge.md)
- 선행 업데이트: [UPD-20260723-09](./UPD-20260723-09-legacy-purge-link-backfill.md)
- 인시던트: 해당 없음 — local synthetic/Emulator 개발이며 production·staging·field 영향 없음
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: human-review-required
- 실제 주장과 근거 일치 여부: Local contract/race, Firebase demo Emulator, Rules·mobile regression과 source commit `3bfa29c`의 clean CI 범위에서 일치한다.
- 검토 메모: 이 업데이트를 integrity finding purge, global orphan-zero, receipt metadata purge 완료, runtime 배포 또는 사용자 제공 기능으로 확대 해석하지 않는다.
