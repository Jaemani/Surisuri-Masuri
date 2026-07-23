---
id: ADR-0033
title: Fenced resumable receipt linkage purge
status: proposed
decided_at: 2026-07-23
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0033: Receipt linkage purge는 receipt fence와 phase cursor를 가진 재개 가능한 job으로 수행한다

## 맥락

[ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md)의 reservation-expiry success finalizer는 attempt를 `completed/outcome=expired`, receipt를 `expired`로 전이하고 receipt·두 uniqueness index에 동일한 `purge_eligible_at`을 기록한다. 이 시각은 삭제 완료가 아니라 linked metadata purge를 시작할 수 있는 가장 이른 시각이다.

Firestore는 parent receipt를 삭제해도 `recoveryAttempts` subcollection을 cascade delete하지 않는다. Cleanup target은 tenant 아래 별도 top-level collection에 있고 향후 integrity finding도 같은 receipt에 연결된 별도 collection이다. Receipt와 두 uniqueness index를 먼저 삭제하면 다음 문제가 생긴다.

- Nested attempt와 target·finding이 owner 없이 남는다.
- 일부 child만 삭제한 process가 중단되면 어디까지 완료됐는지 알 수 없다.
- 재시도가 첫 page부터 시작해 count를 중복하거나 cursor 앞 문서를 건너뛸 수 있다.
- Purge 중 새 attempt·target·finding이 생성되면 empty 확인 뒤에도 orphan이 생긴다.
- 두 uniqueness index와 receipt를 서로 다른 commit에서 삭제하면 partial linkage가 된다.
- Generic recursive delete나 TTL은 exact receipt revision, linkage hash와 child identity를 검증하지 않는다.
- Firestore transaction은 collection query 전체를 원자 snapshot으로 소유하지 않으므로 page discovery와 delete commit 사이의 권한 경계를 따로 설계해야 한다.

따라서 metadata purge를 단일 대형 transaction이나 운영자 스크립트가 아니라, receipt에 purge fence를 먼저 세우고 child 집합을 bounded page로 지운 뒤 마지막 transaction에서만 3-way linkage를 제거하는 별도 durable protocol로 만들어야 한다.

## 결정 기준

- No parent-first delete: Linked child가 남아 있는 동안 receipt와 두 uniqueness index를 삭제하지 않는다.
- Writer fence: Purge job이 시작된 receipt에는 새 attempt·cleanup target·integrity finding을 만들지 않는다.
- Bounded work: Page 크기, document read/write 수, invocation 시간과 total step을 제한한다.
- Monotonic progress: Phase, cursor, deleted count와 job revision은 뒤로 가지 않는다.
- Atomic page commit: Exact child delete set과 job cursor/count 전진은 같은 transaction에서 commit 또는 rollback한다.
- Poison visibility: Malformed·foreign child를 건너뛰지 않고 bounded hold로 남겨 조사 가능하게 한다.
- Response-loss safety: Commit 응답 유실 뒤 mutation을 추측해 반복하지 않고 pre-state-bound read-only outcome으로 확인한다.
- Minimal retention: Purge job에는 좌표, object path, Firebase UID, device·trip·person ID와 payload를 넣지 않는다.
- Runtime isolation: Local contract·Firestore Emulator gate가 끝나도 scheduler, startup, readiness와 staging·production에는 자동 연결하지 않는다.

## 검토한 선택지

### 선택지 A: Receipt TTL 또는 parent delete에 맡긴다

- 장점: 구현이 작다.
- 단점: Firestore subcollection은 parent delete로 제거되지 않는다. Top-level target·finding과 두 uniqueness index도 원자적으로 정리되지 않으며 orphan coverage를 증명할 수 없다.
- 판단: 제외한다.

### 선택지 B: Admin SDK recursive delete 또는 BulkWriter를 한 번 호출한다

- 장점: 많은 문서를 빠르게 지울 수 있다.
- 단점: Process 중단·응답 유실 뒤 exact cursor와 count를 복원하기 어렵다. Linked collection과 final 3-way linkage의 순서, malformed child와 writer fence도 별도 durable state 없이 증명할 수 없다.
- 판단: 운영 도구로도 이 protocol을 우회하지 않는다.

### 선택지 C: Receipt fence + durable purge job + phase별 bounded page transaction

- 장점: 중단·재개, single-page 경쟁, poison hold, final linkage atomicity와 최소 감사 증거를 같은 상태머신으로 검증할 수 있다.
- 단점: Receipt schema, 모든 child create 경계, query/index, job codec, page transaction과 outcome correlation이 추가된다.
- 판단: 채택한다.

## 결정

### 1. Purge job ID는 tenant와 receipt에 결합된 deterministic key다

Top-level path는 다음과 같다.

```text
/ingestPurgeJobs/{purge_key}
purge_key = sha256("ingest-receipt-purge@1\x00" || tenant_id || "\x00" || receipt_id)
```

Receipt ID가 tenant 사이에서 우연히 같아도 같은 job document를 공유하지 않는다. Purge key는 lowercase hex digest이며 raw UID, device, trip, person, batch 또는 object path를 포함하지 않는다.

첫 버전 job은 다음 bounded state만 가진다.

```text
schema_version=ingest-receipt-purge.v1
policy_version=ingest-receipt-purge-policy.v1
purge_key, tenant_id, receipt_id
receipt_revision, linkage_hash
status=planned|attempts_purging|linked_documents_purging|ready|linkage_deleted|hold
revision

attempt_cursor?, attempt_deleted_count
link_cursor?, target_deleted_count, finding_deleted_count

verified_empty_at?, linkage_deleted_at?, purge_job_expires_at?
created_at, updated_at
held_from_status?, error_class?
```

Cursor는 마지막으로 같은 transaction에서 삭제가 확정된 Firestore document ID다. Count는 관측된 문서 수가 아니라 commit된 delete 수다. `revision`은 job 생성 후 모든 mutation마다 1씩 증가한다. Unknown enum, 음수 count, phase와 맞지 않는 cursor, 미래 phase residue와 timestamp 역전은 invalid다.

`linkage_hash`는 version, tenant ID, receipt ID, reservation key, client-batch key, 두 index document ID, post-fence receipt revision과 exact `purge_eligible_at`의 canonical tuple을 hash한다. Device·trip·person ID, object path와 payload는 넣지 않는다. 문자열 연결은 길이 prefix 또는 NUL separator가 있는 canonical encoder만 사용한다.

### 2. Job admission은 receipt에 purge fence를 같은 transaction으로 기록한다

Job 생성은 trusted server time에서 다음 current state를 다시 읽고 검증한다.

- exact tenant receipt와 두 uniqueness index의 3-way linkage
- 현재 구현 범위에서는 `receipt.status=expired`
- receipt와 두 index의 동일한 non-zero `purge_eligible_at <= now`
- R8h purge policy로 재계산한 eligibility와 receipt retention floor
- active recovery/cleanup lease, retry·hold cursor와 forward recovery cursor residue 0
- existing purge fence와 conflicting purge job 0
- expected receipt revision과 linkage hash

성공 transaction은 create-once purge job과 receipt의 다음 field만 함께 쓴다. Job의 `receipt_revision`은 admission 전 expected revision이 아니라 fence commit 뒤의 `expected revision + 1`이다.

```text
purge_job_id=<purge_key>
purge_started_at=<trusted server time>
purge_fence_version=ingest-receipt-purge-fence.v1
receipt revision +1
```

두 uniqueness index는 이 단계에서 쓰지 않는다. 같은 exact command replay는 기존 job·receipt fence를 semantic하게 확인해 write 0으로 반환한다. 다른 revision·linkage·job이 있으면 conflict이며 어떤 문서도 바꾸지 않는다.

Job admission은 첫 write 전 expected pre-fence receipt revision, linkage hash, job absence와 expected post-fence revision을 봉인한 outcome query를 만든다. Commit 응답 유실 뒤에는 mutation을 다시 호출하지 않는다. Fresh read가 exact job, post-fence receipt와 unchanged 두 index를 확인하면 `committed`, job absence, exact pre-fence receipt와 unchanged indexes를 확인하면 `not_committed`, 다른 valid winner나 partial fence는 `unverifiable`이다. Missing/malformed linkage와 read unavailable은 fail-closed한다.

`purge_started_at`은 purge eligibility나 artifact deletion time을 대체하지 않는다. Purge job은 Storage object delete 권한도 아니다.

### 3. Purge fence는 모든 linked child create 경계에서 authoritative하게 검사한다

Purge job create가 commit된 뒤 다음 mutation은 receipt를 current transaction에서 다시 읽고 `purge_job_id`, `purge_started_at`, `purge_fence_version`이 모두 비어 있는 경우에만 새 linked document를 만들 수 있다.

- recovery/cleanup attempt create와 takeover
- immutable cleanup target create
- integrity finding create
- retry·hold release 뒤 pristine attempt create

세 purge field가 일부만 있거나 job document와 binding이 다르면 fail-closed한다. Client Rules는 purge job, attempts, targets와 findings의 direct create/read/update/delete를 explicit deny한다. Admin/IAM 우회 writer는 staging에서 별도 inventory와 writer exclusion으로 검증하기 전 production readiness를 열지 않는다.

Top-level cleanup target와 integrity finding은 자신의 `receipt_id` field만으로 purge discovery를 보장할 수 없다. Field가 누락·변조된 문서는 equality query에 나타나지 않기 때문이다. 따라서 두 문서의 create transaction은 receipt 아래 inverse link도 함께 create해야 한다.

```text
/tenants/{tenant}/ingestReceipts/{receipt}/purgeLinks/{link_id}

link_id = sha256("ingest-purge-link@1\x00" || kind || "\x00" || document_id)
schema_version=ingest-purge-link.v1
tenant_id, receipt_id
kind=cleanup_target|integrity_finding
document_id
created_at
```

Top-level child와 inverse link는 함께 commit 또는 rollback한다. Link는 full Firestore path, UID, object path와 payload를 저장하지 않으며 kind·tenant·document ID로 exact child path를 재구성한다. Duplicate link, same child/different receipt와 link/child binding drift는 모두 fail-closed한다.

이 registry 도입 전 생성된 top-level child는 자동으로 covered 상태가 되지 않는다. R8k-c production gate는 기존 well-formed target/finding을 tenant+receipt 기준으로 inventory해 create-once link를 backfill하고, unregistered document가 0임을 확인해야 한다. Application 밖 Admin/IAM writer가 만든 malformed·unregistered top-level 문서까지 local query만으로 완전 탐지했다고 주장하지 않으며, staging global control-integrity inventory와 writer exclusion 전에는 orphan-zero production 보장을 열지 않는다.

### 4. Page discovery는 advisory이고 transaction이 exact delete authority를 다시 만든다

각 phase는 다음 query를 사용한다.

```text
attempts: /tenants/{tenant}/ingestReceipts/{receipt}/recoveryAttempts
          order by __name__ ASC, startAfter(cursor), limit(page_size + 1)

links:    /tenants/{tenant}/ingestReceipts/{receipt}/purgeLinks
          order by __name__ ASC, startAfter(cursor), limit(page_size + 1)
```

Page size는 1 이상 100 이하로 제한한다. Attempt page는 최대 100 delete+job update, inverse-link page는 최대 100 child delete+100 link delete+job update이므로 모두 Firestore 500-write 한도보다 작다. `page_size + 1` 중 첫 `page_size`만 exact delete set이며 마지막 한 문서는 `has_more` lookahead로만 사용한다. Empty page에는 delete set이 없다. Query 결과는 delete capability가 아니다. Page transaction은 다음을 다시 읽는다.

- exact purge job, expected status/revision/cursor
- receipt와 purge fence binding
- query가 제안한 각 exact attempt 또는 inverse link와 link가 가리키는 top-level child

각 attempt, inverse link와 linked child는 document ID, tenant ID, receipt ID, schema와 phase에 필요한 immutable binding을 strict decode한다. Link page transaction은 exact child와 link를 함께 삭제한다. Missing, changed, duplicate, foreign 또는 malformed document가 하나라도 있으면 전체 page delete와 cursor 전진은 0이다.

Transaction은 validated child delete와 다음 job update를 함께 commit한다.

```text
cursor = last deleted document ID
attempt_deleted_count += len(exact attempt set)
또는 target_deleted_count/finding_deleted_count += link kind별 committed child 수
revision += 1
updated_at = trusted server time
```

모든 page/phase transaction은 `current receipt.revision == job.receipt_revision`과 immutable purge fence를 exact 검사하며 page 진행 중 receipt revision은 바꾸지 않는다. 두 worker가 같은 job revision/cursor를 처리하면 한 transaction만 commit한다. 다른 worker는 stale conflict를 받고 새 current job을 읽기 전 mutation을 반복하지 않는다.

### 5. Phase 전환은 complete empty 확인 뒤 별도 transaction에서만 한다

마지막 non-empty page를 지운 사실만으로 다음 phase로 전환하지 않는다. Current cursor 뒤 `limit(1)` query가 empty이고 receipt purge fence가 유지된 경우에만 phase-completion command를 만든다. Completion transaction은 exact job revision/status/cursor, receipt fence와 empty observation의 짧은 validity window를 다시 확인하고 다음 phase로 전진한다.

```text
planned -> attempts_purging
attempts_purging -> linked_documents_purging
linked_documents_purging -> ready
```

`ready` 전환은 attempts와 inverse-link registry를 cursor 없는 fresh query로 다시 확인하고, well-formed top-level target/finding 중 unregistered document가 0인지 방어적으로 조회한 `verified_empty_at`을 요구한다. Query들은 순차 관측이지만 receipt purge fence가 application writer를 차단한다. Malformed top-level child의 완전 탐지는 staging global inventory gate에 남으므로 out-of-band Admin/IAM writer exclusion이 검증되지 않은 환경에서는 이 evidence를 production orphan-zero 또는 point-in-time proof로 해석하지 않는다.

### 6. Malformed child와 구조 drift는 skip하지 않고 hold한다

Page query·read 자체의 transient unavailable은 job을 바꾸지 않고 호출만 실패시킨다. 다음 상태는 자동 skip이나 delete 대신 job `hold`로 원자 전환한다.

- malformed or foreign child identity
- receipt/job/index linkage drift
- cursor regression 또는 count overflow
- unsupported schema/policy version
- purge fence partial residue

Hold에는 bounded `held_from_status`, `error_class`, revision과 timestamp만 남기고 provider 원문, path, document payload와 identity를 넣지 않는다. Hold가 아닌 job에는 두 field residue가 없어야 한다. Hold release와 operator actor audit는 별도 결정 없이는 구현하지 않는다.

### 7. Page commit 응답 유실은 pre-state-bound read-only correlation으로만 판별한다

각 page·phase transaction은 첫 write 전에 expected job status/revision/cursor, exact delete-set digest와 expected next cursor/count를 봉인한 outcome query를 만든다. Mutation이 non-nil error를 반환하면 같은 invocation에서 transaction을 다시 호출하지 않는다.

Fresh read-only outcome은 다음만 반환한다.

- `committed`: exact next job revision/cursor/count와 child absence가 함께 일치
- `not_committed`: exact pre-state job과 모든 child가 그대로 존재
- `unverifiable`: 다른 valid winner, partial/foreign state 또는 semantic drift

Missing/malformed job·receipt/fence, authorization/read unavailable과 invalid query는 outcome으로 축소하지 않고 unavailable이다. Correlation은 delete, retry, phase transition과 새로운 purge 권한을 만들지 않는다.

### 8. Final linkage delete는 job을 포함한 4문서 transaction이다

Job이 exact `ready`이고 fresh attempt/link empty와 unregistered-child inventory evidence가 유효할 때만 final transaction을 시작한다. Transaction은 다음을 다시 읽는다.

- purge job과 expected revision·linkage hash·`verified_empty_at`
- receipt, exact `receipt.revision == job.receipt_revision`과 purge fence
- 두 uniqueness index와 동일 `purge_eligible_at`
- receipt status·revision·terminal residue

성공 transaction은 두 uniqueness index와 receipt를 삭제하고 job을 `linkage_deleted/revision+1`로 갱신한다. 네 mutation은 같은 commit lineage를 가져야 한다. Job은 삭제하지 않는다.

Job에는 최종 deleted counts, linkage hash와 `linkage_deleted_at`, `purge_job_expires_at`만 bounded하게 남긴다. Job 자체 retention purge는 별도 worker와 결정으로 분리한다.

Final transaction은 첫 delete 전 ready job·receipt·두 index의 exact pre-state와 expected `linkage_deleted/revision+1` job을 outcome query에 봉인한다. Commit 응답 유실 뒤 fresh read에서 exact terminal job과 receipt+두 index 세 linkage document의 absence를 확인하면 `committed`, exact ready job과 세 linkage document의 presence를 확인하면 `not_committed`, 다른 valid job revision 또는 partial linkage면 `unverifiable`이다. Missing/malformed job과 read unavailable은 fail-closed하며 mutation을 replay하지 않는다.

### 9. 첫 구현은 세 gate로 나눈다

R8k-a:

- Pure purge job/admission/page contract와 strict validator
- Deterministic purge key와 linkage hash
- Receipt purge fence schema·codec
- Firestore create-once admission transaction과 response-loss outcome
- Existing attempt/target creation 경계의 purge fence 회귀검사

R8k-b:

- Nested attempt bounded page query·transaction·cursor/count
- Same-page concurrent worker single winner
- Malformed poison hold, crash/response-loss correlation
- Attempt exhaustion과 next-phase transition

R8k-c:

- Cleanup target·integrity finding inverse-link registry, codec와 atomic create
- Legacy well-formed child backfill/inventory gate와 bounded link+child page purge
- Fresh attempt/link empty와 unregistered-child verification
- Final job+receipt+two-index atomic linkage delete
- Rules/indexes와 Firestore Emulator full local vertical slice

각 gate는 별도 구현·증거·제품 업데이트를 가진다. R8k-a/b 완료만으로 receipt나 target·finding을 삭제 완료로 표현하지 않는다.

### 10. 구현 상태 — R8k-a/b 완료, R8k-c registry/backfill 부분 구현

2026-07-23 commit `7a1d3ed`에서 R8k-a의 pure job/admission/outcome contract, deterministic purge key·linkage hash, strict Firestore job codec, receipt purge fence, create-once admission과 read-only response-loss correlation을 구현했다. Concurrent admission은 Firebase demo Firestore Emulator에서 created 1/replayed 1로 수렴했고 job과 receipt fence만 같은 commit lineage로 바뀌며 두 uniqueness index는 write 0이었다. Full/partial fence 뒤 existing recovery/cleanup lease claim과 cleanup target create도 fail-closed한다. Commit `5374be6`에서는 R8k-b 진입 전 advisory page discovery를 `page_size+1`, ordered document ID, separate lookahead로 bounded했다. 이 observation은 transaction 내 exact reread·delete 권한이 아니다.

2026-07-23 commit `24c0050`에서 R8k-b의 nested `recoveryAttempts` purge를 구현했다. Advisory `page_size+1` 결과는 transaction 안에서 current page와 lookahead로 다시 읽고, delete 대상은 exact document를 재조회해 raw Firestore map digest와 strict terminal union을 검증한다. Valid page의 attempt delete와 job cursor·deleted count·revision 전진은 같은 transaction이고, 같은 page 경쟁은 한 winner만 전진한다. Malformed·foreign·unsupported·nonterminal·post-fence child, receipt/job linkage·fence drift와 count overflow는 delete 0의 durable `hold`로 전환한다. Progress-aware cleanup takeover가 남긴 valid `failed/lease_expired` historical ledger는 7개 nonterminal phase shape를 검증한 뒤 삭제할 수 있다.

`planned → attempts_purging`과 fresh exact-empty 확인 뒤 `attempts_purging → linked_documents_purging`도 job revision과 함께 원자 전환한다. Page·phase commit 응답 유실은 봉인된 pre/next state와 exact child presence를 fresh read로 비교하는 read-only correlation만 허용한다. Local contract·race와 Firebase demo Firestore Emulator 근거는 [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge)에 기록한다.

2026-07-23 commits `bf5b95f`, `9f768d9`, `49e402c`, `0a2fad4`에서 R8k-c의 첫 부분인 inverse-link registry와 cleanup-target legacy backfill을 구현했다. 새 cleanup target과 deterministic receipt 하위 link는 같은 transaction에서 create되고 exact pair replay는 write 0이다. Legacy inventory는 tenant-wide `__name__` page 25+lookahead를 사용해 `receipt_id`가 누락된 문서도 관측하며, commit transaction은 current page·target·strict parent receipt·existing link를 다시 검증한 뒤 missing link만 생성한다. Poison 한 건은 whole-page write 0/cursor hold이고, integrity finding collection이 nonempty이면 strict finding codec/writer가 없으므로 unsupported로 중단한다. 근거는 [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill)에 기록한다.

R8k-a/b와 위 R8k-c 부분은 local/synthetic/Emulator 범위다. Integrity finding registry/backfill, bounded target/finding+link purge, fresh global empty/unregistered verification, final receipt+index delete와 Rules/indexes가 남아 있으므로 ADR 상태는 계속 `proposed`다. Scheduler/startup/readiness와 staging·production에도 연결하지 않았으며 metadata purge 완료로 표현하지 않는다.

## 결과와 위험

- Transaction limit보다 많은 nested attempt도 bounded cursor로 중단·재개할 수 있다.
- Receipt fence가 application child writer를 닫고 page delete와 cursor를 같은 transaction에 묶어 orphan·skip·double-count 위험을 줄인다.
- Purge job은 최소 control evidence를 자체 retention까지 남기므로 삭제가 실제로 어디까지 진행됐는지 설명할 수 있다.
- Query는 transaction 밖 advisory read이므로 모든 exact child를 transaction에서 다시 읽어야 한다. 이 규칙을 생략하면 stale page가 foreign document를 삭제할 수 있다.
- Application fence는 out-of-band Admin SDK/IAM writer를 막지 못한다. Staging writer inventory와 least-privilege 검증 전에는 production purge를 활성화하지 않는다.
- Cleanup-target inverse-link schema와 legacy backfill은 local code에 있지만 integrity finding strict DTO/writer/backfill은 없다. Finding document 하나가 관측되면 backfill은 unsupported로 write 0 중단하며 full linkage purge는 아직 완성되지 않는다.
- Legacy page의 `ObservedExhausted`는 순차 관측일 뿐 global orphan-zero가 아니다. Registry rollout 전 fresh cursorless inventory와 out-of-band Admin/IAM writer exclusion gate가 필요하다.
- Operator hold release, held/accepted/rejected-origin cleanup, actual Storage deletion, scheduler/startup/readiness와 staging/production runtime은 이 결정의 구현 완료와 별개다.

## 연결 문서

- 선행 결정: [ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md), [ADR-0031](./ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md), [ADR-0032](./ADR-0032-bounded-cleanup-terminal-orchestration.md)
- 데이터 모델: [Target Domain Model](../data/TARGET_DOMAIN_MODEL.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- R8k-a 구현 증거: [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission)
- R8k-a 제품 업데이트: [UPD-20260723-07](../product-updates/UPD-20260723-07-receipt-purge-admission.md)
- R8k-a 사람 대상 리포트: [HR-20260723-32](../reports/human/HR-20260723-32-receipt-purge-admission.md)
- R8k-b 구현 증거: [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge)
- R8k-b 제품 업데이트: [UPD-20260723-08](../product-updates/UPD-20260723-08-nested-recovery-attempt-purge.md)
- R8k-b 사람 대상 리포트: [HR-20260723-33](../reports/human/HR-20260723-33-nested-recovery-attempt-purge.md)
- R8k-c registry/backfill 부분 구현 증거: [EVD-20260723-043](../evidence/2026-07.md#evd-20260723-043--cleanup-target-inverse-registry와-legacy-backfill)
- R8k-c registry/backfill 제품 업데이트: [UPD-20260723-09](../product-updates/UPD-20260723-09-legacy-purge-link-backfill.md)
- R8k-c registry/backfill 사람 대상 리포트: [HR-20260723-34](../reports/human/HR-20260723-34-legacy-purge-link-backfill.md)
- 인시던트: 해당 없음 — 설계 변경이며 production·staging·field 영향 없음
