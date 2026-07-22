---
id: ADR-0020
title: Two-pass forward reconciliation and manifest-only repair
status: accepted
decided_at: 2026-07-21
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0020: 2단계 forward reconciliation과 manifest-only repair

## 맥락

[ADR-0017](./ADR-0017-fenced-ingest-recovery.md)은 `reserved` receipt를 lease와 fencing token으로 보호하고, [ADR-0018](./ADR-0018-generation-pinned-read-only-classifier.md)은 artifact 상태를 generation-pinned 방식으로 읽기만 하며, [ADR-0019](./ADR-0019-current-forward-recovery-authorization.md)은 현재 관계·동의·fence를 재평가한 worker만 read capability를 받도록 정했다. 다음 R6 단계는 이 분류 결과를 실제 복구 행동으로 바꿔야 한다.

현재 `TelemetryArtifactStore.StoreBatch`는 raw gzip과 manifest를 함께 쓰는 신규 ingest port다. 그러나 `valid_raw_only`를 복구하는 sweeper는 원본 요청 body를 보유하지 않으며, 이미 검증한 immutable raw generation을 다시 업로드해서도 안 된다. 결합 write를 재사용하면 다음 위험이 생긴다.

- raw bytes를 추정·재생성하거나 동일 path에 새 generation을 만들 수 있다.
- read grant를 manifest write 권한처럼 재사용할 수 있다.
- 첫 분류 뒤 raw metageneration, soft-delete inventory, manifest generation 또는 current consent가 바뀌어도 `stored`로 확정할 수 있다.
- `DoesNotExist` manifest create의 timeout·precondition 결과를 성공 또는 충돌로 잘못 단정할 수 있다.
- arbitrary classifier 구현 또는 형식만 그럴듯한 result가 `MarkStored`에 도달할 수 있다.
- receipt terminal mutation과 recovery-attempt completion이 분리되어 운영 기록이 사실과 달라질 수 있다.

따라서 R6은 단순한 `switch classification`이 아니라 current authorization, private validator registry, manifest 전용 capability, 재분류와 fenced receipt mutation을 하나의 fail-closed protocol로 만들어야 한다.

## 결정 기준

- 원본 보존: forward recovery에서 raw object create·rewrite·delete를 0으로 유지할 것
- 현재 권한: artifact write 직전 current receipt/fence/동의를 다시 확인하고, artifact 근거에 의존하는 terminal receipt mutation은 관계·동의 재평가와 같은 Firestore transaction에서 commit할 것
- 계보: path가 없는 pinned result에 authoritative request의 deterministic path만 결합할 것
- 버전: receipt가 고정한 validator profile로 manifest를 만들고 current version fallback을 금지할 것
- 수렴: create 성공·412·timeout 뒤 추측하지 않고 전체 inventory를 재분류할 것
- 격리: read, manifest write, Firestore finalizer 권한을 서로 대체할 수 없게 할 것
- 진실성: classification, action, outcome과 attempt completion을 bounded 값으로 남길 것
- 운영성: transient retry와 operator hold, terminal reject를 분리할 것

## 검토한 선택지

### 선택지 A: 기존 `StoreBatch`로 raw와 manifest를 다시 저장

- 장점: 신규 port와 adapter가 필요 없다.
- 단점: sweeper에 원본 compressed body가 없고, raw generation 불변성과 create 0을 보장하지 못한다. 정상 raw를 새 generation으로 덮는 복구는 immutable lineage 계약을 깨뜨린다.
- 판단: 적용하지 않는다.

### 선택지 B: 첫 분류 결과만으로 manifest 생성 또는 `MarkStored`

- 장점: Storage read 수와 구현 단계가 줄어든다.
- 단점: 분류와 행동 사이의 generation drift, soft-delete 출현, competing manifest, consent withdrawal, lease renewal을 놓친다. `DoesNotExist`는 live object create만 제한하며 전체 version inventory의 유일성을 증명하지 않는다.
- 판단: 적용하지 않는다.

### 선택지 C: 2단계 재분류와 manifest-only capability

- 장점: raw를 건드리지 않고, 행동 직전 current authorization을 다시 확인하며, 쓰기 뒤 전체 cross-object 상태가 exact `valid_complete`로 수렴했을 때만 확정할 수 있다.
- 단점: Firestore·Storage read와 상태 전이 계약이 늘고, timeout·lease renewal·attempt ledger 테스트가 필요하다.
- 판단: 개인정보와 artifact 계보를 보존하는 유일한 선택이므로 채택한다.

## 결정

### 1. Production reconciler가 전체 protocol을 소유한다

`ingest` package의 production constructor는 provider-neutral `ArtifactInventoryReader`, current-state authorizer store, manifest writer와 fenced action store를 받는다. constructor 내부에서 package-private validator registry와 classifier를 합성한다.

- production API는 임의 `ArtifactClassifier`를 주입받지 않는다.
- classifier injection은 package 내부 test seam으로만 둔다.
- `ingest`는 `firebaseadapter` 또는 `gcsadapter`를 import하지 않는다.
- worker, scheduler, startup composition과 readiness는 R6 local·Emulator·Storage gate 전까지 연결하지 않는다.

이는 arbitrary result가 `MarkStored`에 들어가는 경로를 줄인다. Reconciler는 모든 result에 대해 class/reason 조합, validator version, `ObservedAt`, inventory summary와 허용된 pinned lineage shape를 다시 검증한다. 알려지지 않은 enum, 누락·추가 pin, 잘못된 digest/size/generation/metageneration은 side effect 0으로 닫는다.

### 2. Manifest 전용 write port를 분리한다

provider-neutral 계약은 다음 의미를 가진다.

```go
type RecoveryManifestWrite struct {
    ManifestPath string
    ManifestInput BatchManifestInput
    Raw           StoredArtifact
    CanonicalBody []byte
    Digest        ArtifactDigest
}

type TelemetryManifestRecoveryStore interface {
    CreateManifest(
        context.Context,
        ManifestRepairAuthorizationGrant,
        RecoveryManifestWrite,
    ) (StoredArtifact, error)
}
```

- `Raw`는 `valid_raw_only`의 pinned raw에 authoritative request path를 결합한 exact generation이다.
- CRC32C는 0일 수 있지만 SHA-256 lower hex, size, generation과 metageneration은 유효해야 한다.
- `ManifestInput`은 authoritative request에서만 변환한다.
- `CanonicalBody`와 digest는 receipt의 `validator_version`에 해당하는 private registry profile의 `buildManifest`로 생성한다.
- unknown validator/codec profile은 current builder로 fallback하지 않고 hold한다.
- adapter는 path, body digest, raw lineage, manifest input과 capability binding을 I/O 전에 다시 검증한다.
- 이 port에는 raw body나 raw create/delete method가 없다.

`ManifestRepairAuthorizationGrant`는 별도 opaque in-process capability다. zero value와 read grant는 사용할 수 없다. grant는 policy version, receipt/revision, reservation key, current fence, manifest path, raw generation/metageneration, canonical digest와 짧은 expiry에 봉인한다. expiry는 current fence와 관계·동의 expiry를 넘지 않는다.

첫 분류가 `valid_raw_only`여도 즉시 쓰지 않는다. manifest write 직전에 authorizer가 같은 current-state transaction을 다시 읽고 authoritative request와 별도 write grant를 만든다. 새 request가 최초 분류 request와 exact binding이 아니거나 lease renewal로 revision/fence가 바뀌면 기존 result를 폐기하고 첫 단계부터 다시 시작한다.

### 3. GCS writer는 create-only와 exact replay만 허용한다

GCS adapter는 manifest path에 `DoesNotExist` precondition을 걸어 한 번만 create한다.

- create 성공 시 반환 attrs와 body digest를 검증한다.
- 412 또는 commit-ambiguous timeout이면 성공으로 추정하지 않는다.
- exact path의 version inventory와 generation-pinned bytes/attrs를 읽어 요청한 canonical manifest와 정확히 같은 단일 live object이고 soft-deleted/추가 generation이 없을 때만 replay로 반환할 수 있다.
- divergent object, 복수 generation, soft-delete candidate, incomplete inventory와 unverifiable response는 overwrite·delete하지 않는다.
- adapter가 exact replay를 확인해도 cross-object 유일성과 current authorization을 증명한 것은 아니므로 후속 전체 classifier는 항상 실행한다.

방금 만든 manifest도 자동 삭제하거나 overwrite하지 않는다. 두 번째 pass가 실패하면 receipt는 action matrix에 따라 hold/release되며 operator가 pinned evidence를 검토한다.

### 4. 두 번의 fresh pass와 atomic final authorization 뒤에만 `stored`로 확정한다

`valid_complete`와 `valid_raw_only`의 성공 흐름은 다음과 같다.

```text
current authorize(read) -> classify pass 1
  valid_complete:
    current authorize(read) -> classify pass 2
  valid_raw_only:
    current authorize(manifest repair) -> create-only manifest
    current authorize(read) -> classify pass 2
pass 2 exact valid_complete
  -> current authorize(action)
  -> Firestore action transaction에서 current 관계·동의 재평가
  -> fenced MarkStored + attempt completion
```

두 번째 결과는 다음을 모두 만족해야 한다.

- 같은 authoritative request lineage와 validator version
- `valid_complete`와 `manifest_and_referenced_raw_valid` exact 조합
- raw/manifest pin이 각각 하나이고 request path와 결합했을 때 shape가 유효함
- 첫 pass가 `valid_complete`이면 pass 2의 raw와 manifest pin 모두 첫 pass의 SHA-256, CRC32C, size, generation, metageneration과 exact match
- 첫 pass가 `valid_raw_only`이면 pass 2의 raw pin은 첫 pass에서 검증한 raw와 exact match
- raw-only repair에서는 manifest pin이 writer가 반환한 created/replayed manifest와 exact match
- inventory coverage가 complete이고 drift·soft-delete·복수 generation이 없음
- current fence가 아직 유효함

하나라도 다르면 `MarkStored`는 0이다. 첫 결과가 이미 `valid_complete`여도 확인 pass를 생략하지 않는다.

Pass 2 뒤에 별도 current action authorization을 수행하고 `ForwardRecoveryActionGrant`를 발급한다. `stored`와 artifact 근거 기반 `rejected` transaction은 receipt/index/fence뿐 아니라 tenant·beneficiary membership·installation·trip·assignment·precise-location consent를 **같은 Firestore transaction snapshot에서 다시 읽고**, provider-neutral current-policy evaluator를 호출한 뒤에만 mutation한다. 이 transaction 안에서 정책이 deny·malformed·unavailable이면 terminal mutation은 0이다. 따라서 pass 2 도중 또는 직후 동의 철회·membership revoke가 commit돼도 `stored`로 승격되지 않는다.

Firestore adapter가 정책을 독자적으로 재구현하지 않도록 `ingest`가 pure current-policy evaluator와 action command/grant validator를 제공한다. Adapter는 transaction에서 읽은 provider-neutral snapshot을 evaluator에 전달하고, 모든 read가 끝난 뒤 receipt/index와 attempt를 write한다. Action grant의 짧은 TTL은 보조 방어이며 transaction 내 current-state 재평가를 대신하지 않는다.

### 5. Finalizer도 별도 opaque action capability를 요구한다

Read grant, manifest-repair grant와 `LeaseFence`는 finalizer 권한이 아니다. Sweeper 전용 port를 기존 request ingest finalizer와 분리한다.

```go
type ForwardRecoveryActionCommand struct {
    AttemptID       string
    ReceiptID       string
    ReceiptRevision int64
    Fence           LeaseFence
    Phase           RecoveryActionPhase
    Classification  ArtifactClassification
    ReasonCode      ArtifactReasonCode
    Action          RecoveryAction
    Raw             *ArtifactLineage
    Manifest        *ArtifactLineage
}

type ForwardRecoveryActionStore interface {
    CommitForwardRecoveryAction(
        context.Context,
        ForwardRecoveryActionGrant,
        ForwardRecoveryActionCommand,
    ) (Receipt, error)
    FailForwardRecoveryAttempt(
        context.Context,
        ForwardRecoveryAttemptGrant,
        ForwardRecoveryAttemptFailure,
    ) error
}

type ForwardRecoveryOutcomeStore interface {
    GetForwardRecoveryActionOutcome(
        context.Context,
        ForwardRecoveryOutcomeReadGrant,
        ForwardRecoveryOutcomeQuery,
    ) (RecoveryActionOutcome, error)
}
```

`ForwardRecoveryActionGrant`는 zero value를 사용할 수 없는 opaque capability이며 policy version, attempt ID, receipt revision, current fence, phase, exact class/reason, action, raw/manifest pins와 expiry에 봉인한다. Attempt-only failure도 별도 opaque `ForwardRecoveryAttemptGrant`를 요구한다. `stored`는 pass 2의 exact complete pins, `rejected`는 두 번 확인된 same raw-conflict evidence, hold/release는 bounded reason만 허용한다. Existing `MarkStored`/`MarkRejected`/`ReleaseLease`를 직접 호출하거나 constructible `LeaseFence`만 전달하는 것은 sweeper production 경로가 아니다.

기존 최초 HTTP ingest의 `AdmissionStore` 경로는 유지한다. Recovery attempt가 없는 최초 request는 sweeper action grant를 요구하지 않는다. 반면 expired HTTP replay takeover처럼 claim transaction에서 attempt가 만들어진 request-owner 경로는 그 attempt를 기존 request finalizer/release transaction에서 함께 완료하도록 별도 overload/command를 사용한다. HTTP replay attempt를 sweeper action port에 넣거나 영구 `started`로 남기는 구현은 허용하지 않는다.

Commit 응답이 유실됐을 때 pre-action grant나 제거된 fence로 mutation을 다시 호출하지 않는다. Reconciler는 fresh authoritative receipt/attempt correlation을 읽는 system outcome authorizer에서 별도 opaque `ForwardRecoveryOutcomeReadGrant`를 받고, read-only outcome port로 다음 값만 확인한다.

```text
attempt ID, server-stored action hash, bounded outcome,
receipt revision/state consistency, completed_at
```

Outcome grant는 tenant/receipt/attempt/action hash와 짧은 expiry에 새로 봉인하며 expired action grant를 재사용하지 않는다. Store는 completed attempt의 server-stored action hash와 receipt 결과가 exact match일 때만 `committed`를 반환하고, 위치·artifact path·UID/App ID는 반환하지 않는다. 결과가 committed면 mutation 0으로 기존 outcome을 채택한다. Not-committed 또는 unverifiable이면 old action을 replay하지 않고 current fence 상태에 따라 처음부터 reauthorize/reclassify하거나 hold한다.

### 6. Classification과 action을 phase-aware pure matrix로 고정한다

Action planner는 provider call과 Firestore mutation이 없는 pure function이다. 입력은 coarse class/reason뿐 아니라 `initial`, `confirmation`, `post_manifest_confirmation` phase, 이전 exact evidence와 manifest write outcome을 포함한다. 같은 분류라도 phase가 다르면 허용 행동이 다르다.

| 분류·조건 | 행동 | 금지 행동 |
| --- | --- | --- |
| `none/no_candidates` | `awaiting_client_replay` release + bounded backoff | artifact write, stored, reject |
| `valid_raw_only/raw_valid_manifest_absent` | manifest-only 2-pass repair | raw create/rewrite/delete |
| `valid_complete/manifest_and_referenced_raw_valid` | 확인 pass 뒤 fenced stored | single-pass stored |
| `raw_content_conflict` + `decompressed_body_hash_mismatch`, `payload_lineage_mismatch`, `strict_payload_invalid` | 같은 결과의 fresh confirmation 뒤 fenced reject `object_conflict` | manifest repair, 임의 reason reject |
| `manifest_only`, `manifest_conflict`, `metadata_conflict`, `generation_drift` | bounded recovery hold | stored, reject, artifact mutate |
| validator/codec unavailable, response unverifiable, inventory incomplete | operator-review hold | fallback decode/build, artifact mutate |
| provider permission denied | configuration hold + alert | missing/none 강등, busy retry |
| quota, timeout, provider unavailable | transient release/backoff | missing/none 강등, terminal mutation |
| caller cancel/deadline | caller error 보존; 안전한 mutation을 추측하지 않음 | provider error로 재분류 |
| current authorization denied | `current_authorization_denied` hold | artifact write/read 재사용 |
| current authorization unavailable | `authorization_unavailable` release/backoff | denial로 기록 |
| unknown/invalid result contract | side effect 0 + operator alert | default action |

`raw_content_conflict`만 receipt를 terminal `rejected`로 바꿀 수 있다. Manifest와 metadata 충돌은 원인과 소유권이 불명확하므로 reject하지 않는다. `stored_missing`은 accepted integrity audit 전용이며 forward planner 입력이면 invalid contract로 닫는다.

Phase별 추가 불변조건은 다음과 같다.

- `initial`: raw-only는 manifest repair 준비, complete와 raw conflict는 confirmation 준비만 한다. 이 단계에서 stored/rejected는 0이다.
- `confirmation`: exact complete는 stored action authorization으로 진행한다. Raw conflict는 이전 pass와 class, reason, raw pin이 모두 exact match일 때만 rejected action authorization으로 진행한다. 다른 결과는 hold 또는 transient release다.
- `post_manifest_confirmation`: expected raw와 created/replayed manifest에 exact match하는 `valid_complete`만 stored로 진행한다. `valid_raw_only`, `none`, raw conflict와 다른 non-transient 결과는 hold한다. Manifest repair 재진입과 terminal reject는 0이다. Quota/timeout/provider unavailable만 bounded release/backoff할 수 있다.

Lease release code에는 기존 `artifact_unavailable`, `finalizer_unavailable` 외에 `awaiting_client_replay`, `authorization_unavailable`을 추가한다. Hold reason, action, outcome, error class도 server-controlled bounded enum으로 제한하며 provider 원문 오류나 좌표·body·UID·App ID를 저장하지 않는다.

### 7. Lease renewal은 기존 증거를 모두 무효화한다

각 provider/action boundary의 deadline은 caller deadline, capability expiry와 fence expiry 중 최솟값이다. 남은 lease가 renewal window보다 짧으면 manifest write나 확인 pass 전에 `RenewLease`할 수 있다.

Renewal 뒤에는 receipt revision/fence expiry가 달라졌으므로 이전 request, read/write grant, classification result와 planned action을 모두 폐기한다. 새 grant로 단순히 기존 result를 감싸지 않고 current authorize부터 다시 실행한다. Stale owner가 늦게 manifest create를 완료하더라도 두 번째 authorization과 fenced Firestore mutation이 `stored` 승격을 막는다.

### 8. Receipt mutation과 attempt completion을 한 transaction에 기록한다

Recovery attempt는 eventually terminal이어야 한다. Fenced action store는 다음 의미의 completion을 지원한다.

```text
attempt_id, status(completed|failed), classification, action, outcome,
bounded error_class, pinned lineage summary, completed_at
```

- `MarkStored`, `MarkRejected`, `MarkRecoveryHold`, `ReleaseLease`는 current receipt/index/fence를 확인하고 receipt mutation과 현재 attempt completion을 같은 Firestore transaction에 기록한다.
- attempt ID와 fence가 current started attempt와 일치해야 한다.
- 동일 action/result의 응답 유실 복구는 fresh outcome-read grant와 read-only correlation 조회로 수행하며 mutation과 revision 증가가 0이다.
- stale attempt/fence completion은 0건이다.
- receipt action 없이 bounded failure로 끝내야 하고 current fence가 아직 유효하면 `FailForwardRecoveryAttempt`가 attempt만 `failed`로 바꾸는 transaction을 제공한다. 이 API는 receipt terminal state나 artifact lineage를 쓰지 않는다.
- crash, caller cancellation 또는 응답 유실로 attempt-only failure도 기록하지 못하면 다음 successful claim transaction이 만료된 이전 `started` attempt를 `failed/lease_expired`로 먼저 닫고 새 attempt를 `started`로 만든다. Active fence의 attempt를 다른 owner가 닫을 수 없다.
- 과거 구현에서 receipt mutation만 성공한 경우에만 별도 idempotent repair API가 receipt revision·last recovery correlation에서 ledger를 재구성한다. 현재 protocol의 commit 응답 유실은 outcome 조회로 판정하며 이미 성공한 terminal receipt를 ledger 오류 때문에 rollback하지 않는다.
- attempt 문서와 log에는 raw body, 좌표, Firebase UID, App ID, token, provider credential과 원문 오류를 넣지 않는다.

### 9. Current authorization disposition은 artifact action과 다른 capability domain이다

Current authorization 자체가 denied 또는 unavailable인 경우에는 classifier 결과를 꾸며 일반 `ForwardRecoveryActionGrant`로 처리하지 않는다. 별도 `ForwardRecoveryDispositionCommand`, opaque `ForwardRecoveryDispositionGrant`와 Firestore port를 사용한다.

- public authorization 입력은 tenant, reservation, exact lease와 attempt뿐이다. Caller는 action, hold/release code, provider 오류, artifact path·lineage를 제공하지 않는다.
- coherent receipt·fence를 읽고 current 관계가 정책상 거부된 경우에만 `denied`를 파생하며, 결과는 `recovery_hold(current_authorization_denied)`로 고정한다.
- coherent receipt·fence는 읽었지만 관계 snapshot이 의미상 malformed인 경우에만 `unavailable`을 파생하며, 결과는 `release_lease(authorization_unavailable)`와 bounded backoff로 고정한다.
- transport 오류, missing/unreadable snapshot, caller cancellation·deadline은 current-state authority가 없으므로 disposition capability를 발급하지 않고 mutation 0으로 끝낸다.
- Firestore transaction은 linked receipt, 모든 current 관계와 exact `started` attempt를 다시 읽고 동일 disposition을 재평가한 뒤 receipt와 attempt를 함께 갱신한다. Preflight 뒤 allowed 또는 반대 disposition으로 바뀌면 write 0이다.
- attempt에는 classifier phase/class/reason 대신 `decision_domain=current_authorization`과 bounded `authorization_disposition`을 기록한다. 일반 artifact action은 `decision_domain=artifact_reconciliation`을 기록한다.
- fresh outcome query는 decision domain, prior fence, action hash와 expected revision에 함께 결합한다. Domain·disposition·code가 바뀐 완료 기록은 committed로 채택하지 않는다.
- hold review due는 current evaluation 이후이며 artifact expiry와 같을 수 없고 반드시 그보다 이르다.

이 필드가 도입되기 전 완료 attempt에 `decision_domain`이 없다면 outcome correlation은 추정 마이그레이션하지 않고 unverifiable로 닫는다. 현재 runtime이 연결되지 않은 단계에서는 운영 데이터 migration을 수행하지 않는다. 향후 기존 attempt 원장을 보존한 채 배포해야 한다면 별도 migration evidence와 rollout 결정을 먼저 남긴다.

### 10. Bounded single-receipt composition과 commit finalizer tail

R6 component를 실제 순서로 합성하는 `ForwardRecoveryReconciler`는 이미 claim된 receipt 하나만 처리한다. Candidate query, claim, pagination과 poison-item 격리는 R7 worker 책임으로 남긴다. Production constructor는 provider-neutral `ArtifactInventoryReader`, `TelemetryManifestRecoveryStore`와 하나의 `ForwardRecoveryControlStore`만 받고 package-private validator registry, classifier, current authorizer와 outcome authorizer를 내부에서 만든다. Arbitrary classifier·capability minter를 받는 constructor는 package-private test seam뿐이다.

`ForwardRecoveryControlStore`는 lease renewal, current authorization read, action/disposition/attempt mutation과 outcome read 계약을 합성한다. Firestore adapter가 이 전체 interface를 구현하는지 compile-time assertion으로 고정한다. Storage 쪽은 raw body나 generic object store가 아니라 manifest-only port만 전달되므로 reconciler에서 raw create·rewrite·delete를 호출할 type surface가 없다.

기본 단일 receipt budget은 다음과 같다.

```text
total wall budget: 2m
operational budget: total - 5s outcome/finalizer tail
max evidence epochs: 2
max operational logical steps: 24
max detached control-plane finalizer steps: 12
renewal threshold: <= 45s
renewal duration: 2m
```

- Operational context는 authorize, classify, manifest create, normal action과 renew에만 사용한다.
- Commit을 호출한 뒤에는 caller cancellation과 별개인 bounded tail에서 exact outcome read, attempt-failure barrier와 필요 시 disposition만 허용한다.
- Detached tail에서 artifact read/write, renewal 또는 normal action replay는 금지한다.
- Renewal 성공은 hard epoch barrier다. 이전 request, grant, result, plan, manifest evidence와 outcome query를 모두 버리고 initial authorization부터 다시 시작한다.
- Renewal 호출 오류·취소는 새 exact fence를 증명할 계약이 없으므로 `lease unknown`으로 끝내고 이전 fence로 mutation하지 않는다.
- Classifier/read, manifest 또는 action capability expiry는 old evidence를 버리고 새 epoch에서 다시 authorization한다. Unknown contract는 bounded attempt failure 외 artifact·receipt action 0으로 닫는다.

Action/disposition commit의 nil이 아닌 모든 응답은 잠재 response loss로 취급한다. Exact command에서 만든 fresh outcome query가 `committed`를 증명하면 caller cancellation보다 authoritative commit을 우선한다. 첫 read가 `not_committed`여도 pending transaction이 늦게 commit할 수 있으므로 old query를 즉시 폐기하지 않는다.

```text
commit error
  -> exact outcome read
  -> committed: adopt, mutation replay 0
  -> not_committed:
       attempt-failure transaction barrier
       -> barrier success: late action conflicts, attempt closed
       -> barrier failure: exact old outcome read once more
            -> late committed: adopt
            -> policy denial/unavailable: fresh disposition transaction
            -> otherwise: unverified, mutation guess 0
  -> unverifiable/unavailable: unverified, old action replay 0
```

Caller cancellation/deadline이 normal operation 중 발생하면 같은 bounded tail에서 `caller_canceled|caller_deadline` attempt failure를 fresh authorize한다. 현재 정책 때문에 failure capability를 받을 수 없을 때만 fresh disposition으로 전환한다. Disposition direct/correlated commit이 증명되면 authoritative success로 반환하고, failure/disposition commit ambiguity는 caller error와 `commit unverified`를 함께 보존한다.

Completed attempt에 decision provenance가 섞인 채 `failed`로 바뀐 오염 원장이 takeover를 통과하지 않도록 failed-attempt validator도 `decision_domain`과 `authorization_disposition` empty invariant를 강제한다. 이는 새 owner가 provenance가 모순된 prior attempt 위에서 실행되는 것을 차단한다.

## 구현·검증 gate

### Provider-neutral unit·race

- 모든 classification/reason 조합의 pure action matrix와 unknown enum fail-closed
- `valid_complete`: writer 0, fresh authorize/classify 2회, final action authorization과 transaction 내 current-policy 재평가 뒤 exact pins일 때만 stored 1
- `valid_raw_only`: raw write 0, manifest create 1, version-pinned canonical body, second exact complete와 final atomic authorization일 때만 stored 1
- nil/extra pin, path·digest·size·generation·metageneration·validator·ObservedAt mismatch의 side effect 0
- manifest write 전 또는 pass 2 뒤 consent withdrawal, fence expiry 또는 renewal 시 create/finalizer 0
- initial/confirmation/post-manifest phase별 action table과 post-write repair·reject 재진입 0
- initial complete의 pass 사이 raw 또는 manifest generation/metageneration 교체 시 stored 0과 generation-drift hold
- zero/read/write grant로 finalizer 호출 0, action grant command/pin/phase 변조 mutation 0
- renewal 뒤 old request/grant/result/action 재사용 0
- race detector에서 concurrent owner/cleanup winner 뒤 stale finalizer 0

### GCS adapter unit·testbench

- manifest `DoesNotExist` create와 exact metadata/digest
- 412 exact replay, divergent replay, timeout/ambiguous response 분리
- raw create/upload/delete call 0
- wrong body/digest/path/raw lineage/grant는 provider I/O 전 실패
- create 사이 raw soft-delete/metageneration/new generation, second manifest 출현을 확인 pass가 탐지
- overwrite/delete 0과 context deadline clamp

### Firestore unit·Emulator

- stored/rejected와 current relationship/consent 재평가, attempt completion의 단일 transaction 원자성
- hold/release와 attempt completion의 원자성
- awaiting-client-replay와 authorization-unavailable bounded release
- hold reason·review due validation, reserved query 제외와 stale fence 차단
- attempt completed/failed idempotency, stale attempt/fence 거부
- next claim이 expired prior started attempt를 failed/lease-expired로 닫고 새 started attempt를 만드는 경쟁
- 최초 HTTP ingest에는 recovery attempt 0, expired HTTP replay takeover finalizer/release에는 attempt terminal 1
- receipt mutation 성공·ledger 응답 유실 뒤 correlation 기반 재구성
- stored/rejected/hold/release 각각 commit 응답 유실 뒤 fresh outcome grant 조회, old action mutation replay 0
- transaction retry, consent withdrawal, lease renewal/takeover, cleanup 전환 경쟁
- receipt/attempt/log privacy field scan

### Runtime 차단 조건

- provider-neutral unit/race, Firestore Emulator, official Storage testbench와 clean CI 전 worker·scheduler wiring 금지
- startup composition test에서 private classifier/registry 우회 금지
- staging ADC/IAM, GCS soft-delete/versioning, Scheduler caller, alert sink 검증 전 readiness 유지

## 결과와 위험

- 정상 raw generation을 그대로 보존하면서 누락된 canonical manifest만 복구할 수 있다.
- Storage create의 모호한 결과와 분류 뒤 drift가 terminal `stored`로 잘못 승격되는 것을 막는다.
- 추가 authorization·inventory read로 비용과 latency가 증가한다. bounded worker와 per-receipt deadline을 R7에서 제한한다.
- permission denial과 conflict는 자동 수렴 대신 hold를 늘릴 수 있다. 이는 불명확한 artifact를 덮거나 삭제하는 것보다 안전하며 operator runbook과 alert가 필요하다.
- private registry에 prior validator profile을 보존해야 한다. 아직 live reservation이 있는 version을 제거하려면 migration evidence와 별도 ADR이 필요하다.
- 이 결정은 local 구현 protocol이며 production access, 실제 사용자 동의, staging Storage mutation 또는 runtime readiness를 증명하지 않는다.

## 연결 문서

- 선행 결정: [ADR-0017](./ADR-0017-fenced-ingest-recovery.md), [ADR-0018](./ADR-0018-generation-pinned-read-only-classifier.md), [ADR-0019](./ADR-0019-current-forward-recovery-authorization.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 사전절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 제품 업데이트: 해당 없음 — runtime·사용자 흐름 변화 없음
- 증거: [EVD-20260722-025](../evidence/2026-07.md#evd-20260722-025--two-pass-forward-recovery-planner와-manifest-only-repair-boundary), [EVD-20260722-026](../evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계), [EVD-20260722-027](../evidence/2026-07.md#evd-20260722-027--current-authorization-disposition-원자-경계), [EVD-20260722-028](../evidence/2026-07.md#evd-20260722-028--bounded-forward-reconciler-composition) — single-receipt local composition까지, candidate worker·startup·staging은 미구현
- 인시던트: 해당 없음 — production·staging·field 영향 없음
