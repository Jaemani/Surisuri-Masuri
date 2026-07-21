---
id: ADR-0017
title: Lease·fencing token 기반 telemetry reservation 복구
status: accepted
decided_at: 2026-07-21
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0017: Lease·fencing token 기반 telemetry reservation 복구

## 맥락

[ADR-0015](./ADR-0015-atomic-telemetry-admission.md)는 authorization, 두 uniqueness index와 최초 receipt를 하나의 Firestore transaction으로 만들고, [ADR-0016](./ADR-0016-immutable-telemetry-artifact-lineage.md)은 transaction 밖의 raw object·manifest·finalizer를 exact generation 계보로 연결한다. 그러나 `reserved` receipt를 처리하는 실행 주체에 소유권이 없어 다음 경쟁이 남는다.

이 결정은 ADR-0015의 단일 `expires_at`이 처리 마감과 보존기간을 함께 나타내던 부분을 분리한다. ADR-0016 manifest v1의 `expires_at` wire field는 artifact expiry 의미로만 유지하며 reservation 처리나 receipt purge 시각으로 사용하지 않는다.

- 최초 HTTP 요청이 Storage write 중 timeout된 뒤 client retry와 sweeper가 동시에 복구한다.
- 오래된 worker가 멈췄다가 새 worker의 복구 뒤 `MarkStored` 또는 `MarkRejected`를 호출한다.
- active worker가 있는데 반복 요청마다 같은 raw·manifest를 읽고 쓴다.
- raw만 생성된 상태에서 sweeper가 payload 근거 없이 manifest나 성공 상태를 추정한다.
- lifecycle·수동 변경으로 manifest만 남거나 `stored` receipt가 가리키는 generation이 사라진다.
- 현재 동의가 철회됐는데 오래된 reservation을 복구한다는 이유로 새 artifact를 만든다.

Cloud Storage object의 `DoesNotExist` 조건은 overwrite를 막지만 Firestore 상태 변경 순서를 통제하지 않는다. 반대로 Firestore transaction만으로 Storage 작업을 취소할 수도 없다. 따라서 Storage 작업은 중복 실행돼도 안전한 immutable operation으로 유지하고, control-plane 상태 변경에는 현재 실행 주체를 증명하는 fencing token을 요구해야 한다.

## 결정 기준

- 안전성: lease를 잃은 worker가 receipt를 완료·거절·만료 처리하지 못할 것
- 복구 가능성: process crash 어느 지점에서도 client replay 또는 sweeper가 상태를 재분류할 수 있을 것
- 개인정보: 현재 precise-location 동의가 무효면 sweeper가 새 위치 artifact를 만들지 않을 것
- 정직성: raw bytes가 없으면 서버가 payload나 완료 결과를 재구성했다고 주장하지 않을 것
- 계보: 조회·복구·삭제 판단이 path의 latest가 아니라 exact generation과 hash를 사용할 것
- 비용: sample별 Firestore write나 항상 켜진 coordinator 없이 Firestore·Cloud Storage·scale-to-zero worker로 운영할 것
- 검증 가능성: fake clock, concurrent transaction, crash matrix와 stale worker를 재현할 수 있을 것

## 검토한 선택지

### 선택지 A: immutable object만 믿고 모든 retry가 계속 실행

- 장점: 추가 상태와 transaction이 없다.
- 단점: 중복 Storage read/write가 증가하고, 늦게 끝난 worker의 Firestore finalizer를 막을 수 없다. sweeper와 HTTP retry의 역할도 구분되지 않는다.
- 판단: object 무결성은 보호하지만 control-plane ordering을 보호하지 못한다.

### 선택지 B: 분산 mutex 또는 Redis lock

- 장점: 익숙한 lock API와 짧은 TTL을 사용할 수 있다.
- 단점: 별도 상시 인프라와 장애 경계가 생기며 lock 만료 후 stale worker write를 막으려면 결국 fencing token이 필요하다.
- 판단: 현재 규모와 Firebase-first 비용 목표에 불필요하다.

### 선택지 C: Firestore receipt lease와 단조 증가 fencing token

- 장점: 현재 3-way linkage와 같은 transaction 경계에서 소유권을 검증하고 별도 coordinator 없이 stale finalizer를 거절할 수 있다.
- 단점: claim·renew·release transaction, clock skew, sweeper query와 recovery attempt 기록이 추가된다.
- 판단: fencing token으로 안전성을, TTL lease로 liveness를 분리할 수 있어 채택한다.

## 결정

### 1. Lease는 receipt에 두고 token은 절대 감소시키지 않는다

`reserved` receipt에 다음 server-only 필드를 둔다.

```text
fencing_token,                         # int64, 최초 1; takeover마다 +1
lease_owner_id?,                       # server UUID, client/UID가 아님
lease_owner_kind?(request|sweeper|cleanup),
lease_acquired_at?, lease_heartbeat_at?, lease_expires_at?,
recovery_attempt_count,
next_recovery_at?, last_recovery_code?,
cleanup_quiescence_until?,
cleanup_mode?(reservation_expiry|artifact_retention|explicit_deletion|security_approved_rejected),
cleanup_origin_status?(reserved|recovery_hold|stored|queued|projected|rejected),
next_integrity_check_at?,
hold_reason?, hold_review_due_at?,
expected_sample_count,
first_captured_at, last_captured_at,
validator_version,
reservation_deadline, artifact_expires_at,
receipt_retention_floor, purge_eligible_at?
```

- partial lease field, 음수 token, `lease_acquired_at <= lease_heartbeat_at < lease_expires_at` 순서를 만족하지 않는 lease, reservation deadline 뒤 lease는 corrupt/unavailable로 닫는다.
- 성공·거절·만료·manual hold 전환 때 active lease field는 제거하지만 마지막 `fencing_token`은 감사와 stale write 차단을 위해 보존한다.
- owner ID는 요청마다 서버가 생성한다. Firebase UID, App ID, 사람·기관 식별자, 좌표를 넣지 않는다.
- `expected_sample_count`와 captured bounds는 authorization에 사용한 검증 payload에서 reservation 시 함께 고정한다. raw가 없을 때 payload를 재구성하기 위한 값이 아니라, 존재하는 raw를 다시 검증하기 위한 expected lineage다.
- `validator_version`도 최초 reservation에 고정해 crash 뒤 manifest를 현재 배포 버전으로 소급 생성하지 않는다.
- 처리 마감, 위치 artifact 만료, control receipt 최소 보존과 purge eligibility를 한 `expires_at`으로 혼용하지 않는다. reservation 때 `reservation_deadline < artifact_expires_at`과 `receipt_retention_floor`를 고정하되 `purge_eligible_at`은 비워 둔다. terminal cleanup·감사 완료 transaction이 `max(receipt_retention_floor, completed_at + audit_window)`으로 두 index와 receipt에 함께 설정한다. TTL이 linkage 문서를 독립 삭제하게 하지 않는다. eligibility 도달 뒤 bounded purge job이 receipt 하위 attempt와 linked cleanup target·integrity finding을 먼저 비우고, 그 완료 증거를 확인한 마지막 transaction만 두 index와 receipt를 함께 삭제한다. production 기간은 개인정보 보존 승인과 staging lifecycle 검증 뒤 확정한다.
- 신규 `reserved` receipt는 `next_recovery_at=lease_expires_at`으로 시작한다. renew는 새 lease expiry, safe release는 계산된 backoff, takeover는 새 lease expiry를 같은 transaction에서 기록한다. terminal/hold/cleanup state는 reserved sweeper query에서 제외된다.

### 2. Claim·renew·release는 3-way linkage를 재검증한다

provider-neutral control port는 최소한 다음 의미를 가진다.

```go
AuthorizeAndReserve(ctx, principal, scope, reservation, leaseProposal) (Receipt, LeaseGrant, ReservationStatus, error)
ClaimRecoveryLease(ctx, tenantID, reservationKey, owner, now, duration) (LeaseGrant, LeaseStatus, error)
BeginCleanupTransition(ctx, tenantID, reservationKey, now) (Receipt, TransitionStatus, error)
BeginHeldCleanup(ctx, tenantID, reservationKey, now) (Receipt, TransitionStatus, error)
BeginAcceptedDeletion(ctx, tenantID, reservationKey, now, mode) (Receipt, TransitionStatus, error)
BeginRejectedArtifactCleanup(ctx, tenantID, reservationKey, approval, now) (Receipt, TransitionStatus, error)
ClaimCleanupLease(ctx, tenantID, reservationKey, owner, now, duration) (CleanupGrant, LeaseStatus, error)
RenewLease(ctx, tenantID, reservationKey, fence, now, duration) (LeaseGrant, error)
ReleaseLease(ctx, tenantID, reservationKey, fence, now, outcome) error
MarkStored(ctx, tenantID, reservationKey, fence, stored, now) (Receipt, error)
MarkRejected(ctx, tenantID, reservationKey, fence, code, now) (Receipt, error)
```

각 mutation transaction은 두 uniqueness index와 receipt linkage, 상태, reservation deadline, `lease_owner_id`, `fencing_token`, `lease_expires_at`을 다시 읽는다. cleanup entry primitive는 live forward owner가 호출하는 finalizer가 아니라 만료·삭제 경계의 새로운 claim이므로 아래의 origin별 precondition을 만족할 때만 기존 owner/fence 요구의 예외다.

- 신규 HTTP reservation은 authorization·두 index·receipt와 initial `fencing_token=1` lease를 같은 transaction에서 생성한다. 별도 claim transaction을 추가하지 않는다.
- 기존 `reserved` replay는 같은 authorization transaction 안에서 active lease이면 `replay_in_progress`, 만료 lease이면 token을 정확히 1 증가시킨 `replay_lease_acquired`를 반환한다.
- sweeper는 별도 `ClaimRecoveryLease`를 사용하지만 같은 3-way linkage·state·deadline·token 불변조건을 적용한다.
- active lease가 다른 owner에게 있으면 `lease_held`를 반환하며 Storage에 접근하지 않는다.
- renew·release와 실제 state mutation finalizer는 owner ID와 token이 모두 같고 lease가 유효할 때만 성공한다.
- `MarkStored`가 이미 `stored`인 receipt에 동일한 전체 artifact lineage를 받거나 `MarkRejected`가 이미 같은 rejection code를 받으면 mutation·revision 증가 없이 기존 receipt를 반환할 수 있다. 이 terminal read-only replay만 active lease/fence를 요구하지 않으며 linkage나 필드가 하나라도 다르면 unavailable이다.
- stale owner의 token이 작으면 bytes가 같아도 Firestore mutation은 실패한다.
- lease를 잃은 worker의 immutable Storage write가 뒤늦게 성공할 수는 있다. 이는 overwrite가 아니며 새 owner가 exact replay로 검증한다. stale worker는 receipt 상태를 바꾸지 못한다.
- `BeginCleanupTransition`은 같은 transaction에서 `reserved`, `reservation_deadline <= now`, active lease 없음과 3-way linkage를 확인하고 token을 정확히 1 증가시킨 뒤 lease를 제거하고 `cleanup_pending`, `cleanup_mode=reservation_expiry`, `cleanup_origin_status=reserved`, quiet-period 기준을 기록한다. deadline 전 recovery claim과 경쟁해도 둘 중 유효한 상태 전이 하나만 commit되며, deadline 뒤 recovery claim은 이 transaction보다 먼저 commit돼도 lease를 얻을 수 없다.
- `BeginHeldCleanup`은 reserved-origin `recovery_hold`와 승인된 artifact retention expiry를 확인해 `cleanup_pending`으로 보낸다. `BeginAcceptedDeletion`은 `stored|queued|projected`를 `deleting`으로 보내고 accepted replay 의미를 보존한다. `BeginRejectedArtifactCleanup`은 별도 보안 승인과 object ownership이 exact lineage로 증명된 경우에만 cleanup target/lease를 만들며 receipt status는 `rejected`로 유지한다. 세 primitive 모두 token을 증가시키고 `cleanup_mode`, immutable `cleanup_origin_status`, quiet period를 고정하며 일반 forward finalizer가 호출할 수 없도록 worker identity와 경로를 분리한다.
- Firestore server time으로 미래 timestamp를 직접 계산할 수 없으므로 Cloud Run의 UTC clock을 사용하되 transaction read time과 application time의 차이를 측정한다. 허용 skew를 넘으면 lease·consent·deadline·cleanup 판단을 fail-closed한다.
- fencing token은 clock skew가 있어도 **Firestore mutation ordering**을 보호한다. consent·deadline acceptance는 `max(application UTC, transaction read time)`으로 이미 만료됐을 가능성을 우선하고, 조기 cleanup 방지는 `min(application UTC, transaction read time)`으로 아직 만료 전일 가능성을 우선한다. lease takeover에는 transaction ordering과 skew guard를 함께 적용한다. 허용 skew를 넘으면 연산 방향과 관계없이 fail-closed하며 staging에서 양방향 offset fault를 주입한다.
- 기본 lease는 120초, 허용 범위는 30초~5분으로 시작한다. 남은 시간이 45초 이하일 때만 heartbeat를 허용하며 실제 latency evidence로 조정한다.

### 3. HTTP retry와 sweeper는 같은 claim primitive를 사용한다

- HTTP 요청은 `AuthorizeAndReserve`에서 현재 authorization을 먼저 재평가하고 신규 생성 또는 expired-lease takeover를 같은 transaction으로 직렬화한다.
- 다른 active owner가 있으면 object 작업을 중복 실행하지 않고 stable pending receipt를 반환한다. HTTP status·retry hint는 handler 변경 시 contract test로 확정한다.
- transient Storage/finalizer 오류 뒤 요청이 정상적으로 unwind하면 fence를 확인해 lease를 release하고 `next_recovery_at`을 backoff로 갱신한다.
- timeout처럼 release 성공을 모르면 lease expiry가 takeover를 허용한다.
- sweeper도 같은 claim transaction을 사용하지만, 원래 사용자의 principal을 가장하지 않는다. tenant, installation, trip, assignment와 current precise-location consent를 system recovery policy로 다시 읽는다.
- 현재 동의가 철회·교체·만료됐거나 tenant/trip linkage가 무효면 새 manifest나 receipt completion을 만들지 않고 `recovery_hold(consent_invalid)`로 전환한다. 동의 철회 자체를 기존 artifact의 자동 삭제 trigger로 해석하지 않는다. 삭제는 별도 명시적 삭제 요청 또는 승인된 보존기간 만료에서만 시작한다. 이미 withdrawal 전에 `stored`로 완료된 receipt를 이 규칙으로 소급 downgrade하지 않으며, 이 제한은 pending `reserved` forward recovery에만 적용한다.

### 4. Sweeper는 payload 재전송기가 아니라 receipt-driven reconciler다

sweeper는 `status == reserved AND next_recovery_at <= now`를 bounded page로 읽고 각 receipt를 개별 claim한다. query 결과만 신뢰하지 않고 claim transaction에서 상태를 다시 읽는다.

`stored`·`rejected` integrity 검사는 reserved sweeper query로 처리하지 않는다. stored finalizer와 rejected transition은 `next_integrity_check_at`을 설정하고, 별도 bounded integrity auditor가 exact receipt lineage와 version-aware Storage Inventory를 읽는다. auditor는 receipt를 최신 generation으로 다시 연결하거나 자동 삭제하지 않으며 stored-missing·rejected-artifact를 origin-preserving integrity finding/alert로 남긴다.

```text
receipt candidate
  -> fenced claim
  -> current recovery authorization
  -> deterministic raw/manifest path inspect
  -> generation pin
  -> bytes + attrs + receipt lineage classification
  -> fenced action / attempt ledger
```

- manifest가 있으면 version-aware inventory에서 candidate를 확인한다. bytes가 같아 보여도 manifest generation candidate가 둘 이상이면 authoritative generation을 자동 선택하지 않고 hold한다. 유일한 manifest generation만 pin해 그 안의 `object_generation`을 raw의 authoritative candidate로 사용한다.
- manifest가 없을 때만 deterministic raw path를 inspect하고 발견 즉시 generation을 pin한다. version inventory에서 복수의 모순된 candidate가 확인되면 자동 선택하지 않고 hold한다.

raw/manifest 존재 여부와 검증 결과에 따른 행동은 다음과 같다.

| receipt·artifact 분류 | 자동 행동 | 금지 행동 |
| --- | --- | --- |
| `reserved`, raw 없음, manifest 없음 | `awaiting_client_replay`와 backoff 기록; deadline 경과 시 `cleanup_pending` | raw body·sample·성공 상태 추정 |
| `reserved`, valid raw만 존재 | exact generation raw를 decompress·strict validate한 뒤 canonical manifest 생성, fenced finalizer | path/metadata만 보고 manifest 생성 |
| `reserved`, valid raw+manifest | 두 generation과 cross-lineage를 검증하고 fenced finalizer | latest generation 암묵 참조 |
| `reserved`, manifest만 존재 | `recovery_hold`와 무결성 경고 | raw 재생성, manifest 자동 삭제 |
| `reserved`, exact raw payload/body conflict | 현재 fence로 `object_conflict` reject | 일부 metadata만 보고 terminal reject |
| `reserved`, metadata·manifest·receipt lineage 불일치 | `recovery_hold`와 근거 기록 | overwrite, generic mismatch를 terminal content conflict로 오분류 |
| `stored`, 두 artifact valid | no-op/monitor success | receipt revision 증가 |
| accepted receipt, artifact 누락·불일치, `now < artifact_expires_at` | data-loss alert와 origin-preserving integrity finding/manual review | receipt를 `recovery_hold`로 downgrade, payload 추정 재생성 |
| accepted receipt, artifact 누락, `artifact_expires_at <= now` | 승인된 lifecycle/deletion evidence와 `deleting/deleted` workflow 대조; 증거가 없거나 불일치하면 integrity finding 후 `BeginAcceptedDeletion`/escalation | 정상 만료를 무조건 data-loss로 분류, accepted receipt를 `expired`로 downgrade, latest generation 재연결 |
| `rejected`, artifact 존재 | security/manual review | receipt 소유가 불명확한 object 삭제 |
| `reservation_deadline`이 지난 receipt | fenced transaction으로 `cleanup_pending` 전환 후 late-write quiet period 시작 | manifest 생성, `stored` 승격, 즉시 삭제 |

- raw exact 검증은 gzip bytes SHA-256·CRC32C·size뿐 아니라 decompressed body hash, strict telemetry schema, tenant/device/trip/installation/consent/client batch, sample count와 captured bounds까지 확인한다.
- sweeper가 terminal raw content conflict로 분류하려면 exact generation 전체 compressed bytes, deterministic recompression과 decompressed body hash까지 확인해야 한다. Content-Type, Cache-Control, custom metadata 또는 metageneration만 다른 경우에는 reject하지 않는다.
- manifest exact 검증은 raw generation·hash·CRC·size와 receipt expected lineage를 교차 확인한다.
- recovery runtime은 `validator_version`으로 strict decoder, validator와 canonical manifest builder를 선택하는 explicit registry를 가진다. 어떤 version도 그 version을 참조하는 reservation의 deadline·cleanup이 끝나기 전에 제거하지 않는다. unknown/retired version은 current validator로 대체하지 않고 `recovery_hold(validator_unavailable)`로 보낸다.
- receipt가 없는 bucket object는 이 receipt-driven sweeper가 자동 삭제하지 않는다. Storage Inventory/prefix audit로 별도 발견하고 manual hold 후 처리한다.
- reserved-origin `recovery_hold`는 artifact lifecycle을 자동 연장하지 않는다. 만료 전 hold는 `now <= hold_review_due_at < artifact_expires_at`과 reason을 기록하고 즉시 alert한다. 만료 이후 처음 발견된 reserved finding은 `hold_review_due_at=now`로 기록하고 즉시 `BeginHeldCleanup` 대상이 된다. accepted receipt의 integrity finding은 receipt를 `recovery_hold`로 downgrade하지 않고 별도 finding과 accepted deletion workflow를 사용한다. rejected receipt도 상태를 유지하고 ownership 확인·보안 승인 전에는 삭제하지 않는다. 별도 legal hold는 명시적 사람 승인·정책 없이는 만들지 않으며, provider 오류나 ambiguous lineage로 cleanup이 막히면 privacy/operations incident로 escalate한다.

### 5. Recovery attempt는 좌표 없이 별도 ledger에 남긴다

expired lease를 takeover하는 HTTP request와 각 sweeper recovery claim은 `/ingestReceipts/{receiptId}/recoveryAttempts/{attemptId}` 또는 동등한 server-only collection에 recovery attempt를 남기고 `recovery_attempt_count`를 같은 claim transaction에서 증가시킨다. 신규 최초 request는 recovery attempt가 아니다. attempt ID는 transaction 밖에서 생성한다.

```text
attempt_id, tenant_id, receipt_id,
owner_kind, fencing_token,
status(started|completed|failed),
classification?, action?, outcome?, error_class?,
object_path?, object_generation?, manifest_path?, manifest_generation?,
started_at, completed_at?, worker_version
```

- raw bytes, 좌표, 인증·App Check token, UID/App ID, 이름·전화번호를 기록하지 않는다. fencing token은 기록한다.
- attempt doc은 상태 원장이 아니라 감사·운영 증거다. receipt transaction 결과와 충돌하면 receipt가 우선한다.
- claim transaction은 `status=started`와 아직 알 수 없는 classification/action/outcome을 비워 생성한다. artifact inspect 뒤 현재 fence를 확인해 후속 update하고, completion update가 유실돼도 receipt revision과 attempt correlation으로 재구성한다.
- 로그에는 receipt ID, attempt ID, fence, 분류와 latency만 구조화하며 정밀 위치와 request body를 금지한다.

### 6. 상태 machine과 만료

현재 상태에 `cleanup_pending`, `expired`와 `recovery_hold`를 추가한다. forward reconciliation과 origin별 cleanup은 서로 다른 mode·claim·완료 조건을 사용한다.

```text
reserved -> stored -> queued -> projected
    |          |         |          |
    |          +---------+----------+-> deleting -> deleted
    -> rejected                         # status 유지; 승인된 artifact cleanup은 side target
    -> cleanup_pending -> expired       # reserved-origin partial artifact만
    -> recovery_hold                    # reserved-origin 손상·manual review
             -> cleanup_pending         # retention expiry 뒤 BeginHeldCleanup
```

- `recovery_hold` 해제는 자동 retry가 아니라 operator-reviewed command와 새 audit event를 요구한다.
- deadline 기반 `cleanup_pending` 전환은 `BeginCleanupTransition`, reserved-origin hold의 retention expiry는 `BeginHeldCleanup`만 수행한다. accepted는 `BeginAcceptedDeletion`으로 `deleting`, rejected는 승인된 side cleanup target으로 진입한다. 모든 cleanup entry는 token을 증가시켜 stale owner를 fence-out하고 `cleanup_origin_status`를 고정하며 `cleanup_quiescence_until`을 `max(last lease expiry, transition time) + late-write grace`로 설정한다. grace는 최대 lease와 Storage operation timeout의 합보다 길어야 하며 staging fault evidence로 확정한다.
- cleanup worker는 owner kind `cleanup`의 별도 lease를 claim·renew한다. quiet period 전에는 discover/delete하지 않는다.
- quiet period 뒤 version-aware discovery로 exact generation target을 만든다. 삭제 뒤에도 live generation을 다시 확인하며 늦은 generation이 발견되면 reserved-origin은 `expired`로 완료하지 않고, accepted/rejected origin은 상태를 downgrade하지 않은 채 linked supplemental target 또는 integrity finding으로 보낸다.
- `expired`는 associated partial artifact가 exact-generation cleanup됐거나 version-aware empty로 확인된 상태다. 두 uniqueness index를 즉시 삭제하거나 client batch ID를 재사용하지 않는다. 세 linkage 문서 retention cleanup은 `purge_eligible_at` 뒤의 bounded purge job이 담당한다.
- accepted artifact의 lifecycle 삭제는 `stored|queued|projected -> deleting -> deleted` 정상 보존 workflow이며 reserved sweeper가 대신하지 않는다. `deleting|deleted` receipt도 accepted lineage가 일치하는 replay에는 complete 의미를 유지한다.
- rejected artifact는 receipt status를 바꾸지 않는다. exact ownership 증명과 명시적 보안 승인 없이는 cleanup lease·target·delete를 만들지 않는다.
- expiry cleanup은 먼저 immutable deletion target에 path·exact generation·hash를 기록하고 raw generation, manifest generation 순서로 조건부 삭제한다. 중간 crash는 같은 target으로 재개하고 새 generation으로 갈아타지 않는다.
- Firestore는 parent receipt 삭제 시 `recoveryAttempts` subcollection을 cascade delete하지 않는다. purge job은 terminal 상태와 eligibility를 다시 확인하고 attempt를 document-name cursor로 bounded 삭제한 뒤 linked cleanup target과 integrity finding을 bounded 삭제한다. terminal receipt에는 새 attempt/target/finding 생성을 금지한다. 세 집합이 비었다는 완료 증거를 job에 기록한 다음에만 마지막 transaction이 두 uniqueness index와 receipt를 원자 삭제한다. purge job은 최소 증거(hash·count·완료시각)만 남기고 별도 승인된 보존기간 뒤 삭제한다.

동일 batch replay의 stable 상태 의미는 다음과 같다.

- `recovery_hold`: `replay_recovery_hold`, Storage call 0, HTTP `409 RECEIPT_RECOVERY_HOLD`
- `cleanup_pending` 또는 `expired`: `replay_expired`, Storage call 0, HTTP `410 RESERVATION_EXPIRED`
- `stored|queued|projected|deleting|deleted`는 accepted lineage가 일치하면 `replay_complete`, Storage call 0을 유지한다.
- `rejected`는 artifact cleanup 진행·완료와 관계없이 동일 rejection에 `replay_rejected`, Storage call 0을 유지한다.
- active/expired `reserved`는 앞 절의 in-progress/acquired 의미를 따른다.

### 7. 구현과 승격 순서

1. domain lease/fence type과 receipt invariant
2. Firestore claim·renew·release·fenced finalizer transaction과 Emulator 경쟁 테스트
3. HTTP ingest의 lease-held/pending 동작과 stale finalizer fault test
4. generation-pinned read-only reconciler classifier
5. valid raw-only/raw+manifest 자동 복구와 recovery attempt ledger
6. bounded sweeper query, backoff와 current-consent recovery authorizer
7. staging IAM·lifecycle, crash injection과 운영 runbook

1~6의 local 구현만으로 runtime readiness를 열지 않는다. startup wiring, Scheduler/Cloud Run IAM, lifecycle·retention, alert sink와 staging E2E까지 통과해야 한다.

## 결과와 위험

- stale worker가 완료·거절 상태를 덮는 경쟁은 fencing token으로 차단된다.
- HTTP retry와 sweeper가 같은 recovery primitive를 사용해 별도 복구 의미가 생기지 않는다.
- raw가 없는 reservation은 client replay 없이는 완료할 수 없다는 한계를 명시적으로 보존한다.
- Firestore transaction과 attempt ledger write가 늘지만 sample별 write는 계속 0이다.
- clock skew는 lease takeover 시점을 흔들 수 있다. token safety, 짧은 최대 duration, lease-age metric과 staging fault test로 관리한다.
- collection-group query와 recovery ledger에는 index·TTL·비용 검토가 필요하다.
- `recovery_hold`, expired index cleanup, deletion workflow와 operator UI는 후속 구현 없이는 운영 부담으로 남는다.

## 후속 검증

- concurrent claim에서 winner 1명, loser는 Storage 호출 0
- lease expiry takeover의 token 정확히 +1
- stale owner의 renew·release·MarkStored·MarkRejected 전부 거절
- crash-before-raw, raw-only, raw+manifest-before-finalizer, manifest-only, stored-missing matrix
- reserved cleanup transition 대 recovery claim/stale finalizer 경쟁
- accepted `deleting/deleted` replay-complete와 rejected side cleanup replay-rejected 보존
- rejected artifact ownership·보안 승인 누락 시 target/delete 0
- raw decompress·strict payload/receipt cross-validation corruption fixture
- current consent 철회 뒤 sweeper의 새 artifact write 0
- bounded query pagination, backoff, poison receipt가 다른 receipt를 막지 않음
- recovery attempt/log privacy scan
- Firestore Emulator transaction contention과 official Storage testbench generation-pinned read
- staging Scheduler identity, Cloud Run IAM, bucket lifecycle·retention·alert drill

## 연결 문서

- 선행 결정: [ADR-0015](./ADR-0015-atomic-telemetry-admission.md), [ADR-0016](./ADR-0016-immutable-telemetry-artifact-lineage.md)
- 상세 구현 계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 데이터 계약: [Target Domain Model](../data/TARGET_DOMAIN_MODEL.md)
- 위험: [RSK-06, RSK-10](../plans/RISK_REGISTER.md)
- 제품 업데이트: 해당 없음 — 결정 문서이며 runtime 변경이 아님
- 인시던트: 해당 없음 — production·field 영향 없음
