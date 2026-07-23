---
id: UPD-20260723-09
date: 2026-07-23
status: draft
version_or_deployment: receipt-purge-r8k-c-registry-local
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: Cleanup target inverse registry와 legacy backfill

## 요약

새 cleanup target은 parent receipt 아래 deterministic `purgeLinks` 문서와 같은 Firestore transaction에서 생성되고, registry 도입 전 생성된 well-formed cleanup target은 tenant-wide bounded inventory로 링크를 보충할 수 있게 됐다. Backfill은 advisory page를 mutation transaction 안에서 다시 조회하고 target·parent receipt·기존 link를 strict 검증하며, poison 한 건이나 지원되지 않는 integrity finding 한 건이 있으면 page 전체를 write 0으로 정지한다. 이 변화는 local component와 Firebase demo Emulator에만 있으며 target/finding 삭제나 production migration은 아니다.

## 변경 전 문제

- Top-level cleanup target의 `receipt_id` field만으로 purge discovery를 수행하면 field가 누락·변조된 legacy 문서를 equality query가 놓칠 수 있었다.
- 새 target과 inverse link가 서로 다른 commit이면 target-only 또는 link-only partial state가 생길 수 있었다.
- Registry 도입 전 target을 링크 없이 둔 채 purge를 시작하면 receipt fence 뒤 orphan을 보충할 안전한 방법이 없었다.
- Integrity finding은 저장 codec·writer가 구현되지 않아 임의 schema를 추정한 backfill이 오히려 잘못된 purge authority를 만들 수 있었다.

## 변경 후 동작

- Cleanup target link ID는 kind와 document ID를 NUL-separated SHA-256 input에 결합해 결정론적으로 만든다.
- 새 cleanup target create transaction은 strict existing target·existing link를 함께 읽고 둘 다 없을 때만 둘을 생성한다. Exact pair는 replay write 0, 한쪽만 있거나 binding이 다르면 conflict write 0이다.
- Legacy inventory는 tenant의 `ingestCleanupTargets`를 `__name__ ASC`, 최대 25건과 lookahead 1건으로 읽는다. `receipt_id ==` query를 사용하지 않는다.
- Commit transaction은 advisory page와 lookahead를 exact 재조회한 뒤 각 target의 allowlist/type/hash/document ID/tenant를 검증하고, parent receipt의 base identity·state·retention·cleanup transition과 target reservation context를 다시 결합한다.
- Existing link는 snapshot path/body/parent/pair를 strict 검증한다. Missing link는 parent가 unfenced일 때만 create 대상으로 분류한다.
- Malformed·foreign·linkage drift·fenced-unregistered target 하나가 있으면 page의 link create와 cursor advance가 모두 0이다.
- `ingestIntegrityFindings`는 body를 해석하지 않고 name-only `limit(1)`로 존재 여부만 본다. 하나라도 있으면 unsupported로 전체 transaction을 중단한다.
- Concurrent same-page backfill은 Firestore retry 뒤 created 1건과 registered replay 1건으로 수렴했다.

## 범위

- 포함: Inverse-link pure contract·strict codec, cleanup target+link atomic create, legacy target page contract, tenant-wide advisory query, transactional exact requery/reread, strict parent binding, finding existence probe, create-only backfill, unit/race·Firestore Emulator·workspace·CI gate.
- 제외: Integrity finding DTO/schema/writer와 backfill, link+child delete, unregistered global proof, final receipt·두 index delete, purge job retention, operator CLI, `cmd/server`, scheduler/startup/readiness, staging·production migration.
- 배포 환경: `local component + Firebase demo Firestore Emulator`
- 데이터 유형: `synthetic cleanup target, receipt, inverse link and malformed control documents`; GPS·사용자·기관 데이터 없음

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| 새 target과 link atomic create/replay/conflict | Go unit/race + Firebase demo Emulator concurrency | `local pass` | [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) |
| Tenant-wide bounded page와 stale observation 차단 | Pure contract + fake transaction + Emulator prefix/lookahead regression | `local pass` | [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) |
| Strict target·parent receipt·link binding과 poison write 0 | Unit/race + Emulator malformed/foreign/drift/fence matrix | `local pass` | [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) |
| Integrity finding 존재 시 unsupported/write 0 | Name-only query Emulator test | `local pass` | [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) |
| 전체 Go·workspace·모바일·Rules gate | WSL2 Docker와 local workspace | `pass` | [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) |
| Clean GitHub Actions | CI run 29967356380 | `success` | [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill) |

## 배포와 롤백

- Firebase/GCP staging·production, 앱스토어와 사용자 환경에는 배포하지 않았다.
- Operator/runtime entry point가 이 backfill을 호출하지 않으므로 실제 tenant target이나 receipt를 읽거나 쓰지 않는다.
- Local code rollback은 commits `bf5b95f`, `9f768d9`, `49e402c`, `0a2fad4`의 역순 revert로 가능하다. 실제 production backfill을 수행하지 않아 운영 데이터 rollback은 없다.
- 향후 migration은 tenant별 dry observation, finding-empty gate, page별 결과 저장, fresh final inventory와 Admin/IAM writer exclusion을 별도로 승인해야 한다.

## 알려진 제한과 후속 작업

- `ObservedExhausted`는 한 순차 query 시점의 cursor 이후가 비었다는 뜻이며 global orphan-zero나 point-in-time snapshot이 아니다.
- Integrity finding은 한 건만 있어도 unsupported다. Writer와 strict codec을 구현하기 전에는 finding link를 만들지 않는다.
- Application writer 밖 Admin SDK/IAM writer가 malformed target을 추가하지 못한다는 증거는 없다.
- Page size는 25이며 durable operator checkpoint·response-loss outcome은 아직 없다.
- Linked target/finding과 link의 bounded delete, fresh empty/unregistered 검증, `ready`와 final receipt/index transaction은 후속 R8k-c gate다.

## 관련 기록

- 결정: [ADR-0033](../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md)
- 증거: [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill)
- 사람 대상 리포트: [HR-20260723-34](../reports/human/HR-20260723-34-legacy-purge-link-backfill.md)
- 선행 업데이트: [UPD-20260723-08](./UPD-20260723-08-nested-recovery-attempt-purge.md)
- 인시던트: 해당 없음 — local synthetic/Emulator 개발이며 production·staging·field 영향 없음
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: human-review-required
- 실제 주장과 근거 일치 여부: local pure/unit/race, Firebase demo Emulator, workspace gate, clean CI와 independent review 범위에서 일치한다.
- 검토 메모: Legacy target backfill을 integrity finding coverage, global orphan-zero, linked-document purge 또는 production migration 완료로 확대 해석하지 않는다.
