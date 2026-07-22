---
id: ADR-0025
title: Generation-pinned cleanup delete with version-aware absence audit
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0025: exact-generation 삭제와 version-aware 부재 감사를 분리한다

## 맥락

[ADR-0024](./ADR-0024-immutable-cleanup-dry-run-target.md)는 cleanup 분류가 관측한 raw와 manifest의 exact path, generation, metageneration과 digest를 create-once target으로 고정했다. 그러나 이 target과 cleanup lease는 Cloud Storage mutation 권한이 아니며, delete RPC의 반환값도 artifact가 사라졌다는 충분한 증거가 아니다.

삭제 경계에는 다음 경쟁과 provider 의미가 남아 있다.

- Target 생성 뒤 같은 exact path에 late generation이 생길 수 있다.
- Delete 요청의 응답이 유실돼 caller는 실패를 받았지만 provider에서는 삭제가 완료됐을 수 있다.
- Direct exact-generation delete의 `404`는 이미 없다는 신호일 수 있으나, list 권한·soft-delete·version inventory가 정상이라는 증거는 아니다.
- Bucket에 soft delete가 활성화돼 있으면 delete한 generation이 복구 가능한 soft-deleted generation으로 남을 수 있다.
- Raw 삭제가 불확실한 상태에서 manifest를 먼저 삭제하면 payload와 감사 계보의 복구 가능성을 동시에 잃는다.

R8c의 목적은 persisted target을 current cleanup fence 아래 다시 승인하고, exact generation만 조건부 삭제하며, 각 단계 뒤 complete version-aware inventory로 부재를 별도 확인하는 local component를 만드는 것이다.

## 결정 기준

- 최소권한: cleanup claim, target create, provider delete와 cleanup completion 권한을 분리한다.
- 대상 고정: latest, prefix, wildcard 또는 caller가 새로 선택한 generation을 삭제하지 않는다.
- 순서 보존: raw가 없다는 별도 감사 결과 전에는 manifest delete를 호출하지 않는다.
- 불확실성 보존: timeout, cancellation, permission, quota, provider unavailable과 `404`를 단독 완료로 축소하지 않는다.
- 재개 가능성: 응답 유실 뒤 mutation replay 전에 read-only audit로 현재 상태를 판별한다.
- provider 현실성: official testbench가 증명할 수 없는 soft-delete·version 의미를 staging 완료로 과장하지 않는다.

## 검토한 선택지

### 선택지 A: Target의 `planned`를 곧바로 delete 권한으로 사용

- 장점: Authorizer와 Firestore read가 추가되지 않는다.
- 단점: Target 생성 이후의 lease takeover, receipt revision과 attempt terminal 변화를 놓치며 create 권한을 mutation 권한으로 확대한다.
- 판단: 제외한다.

### 선택지 B: Delete RPC 2xx 또는 404를 단계 완료로 기록

- 장점: Provider 호출 수가 적고 일반적인 idempotent delete 구현과 비슷하다.
- 단점: Response loss, noncurrent generation, soft-deleted generation과 late generation을 구분하지 못한다. 404를 permission/list 실패와 잘못 결합할 위험도 있다.
- 판단: 개인정보 artifact의 부재 증거로 부족하므로 제외한다.

### 선택지 C: Fresh capability, exact conditional delete와 별도 full inventory audit

- 장점: Mutation 대상과 control-plane ownership을 다시 결합하고, delete 전송 결과와 데이터 부재 사실을 분리한다. Crash 뒤 audit-first 재개가 가능하다.
- 단점: 추가 read와 outcome 상태가 필요하며 staging bucket 정책 검증 전에는 terminal completion을 열 수 없다.
- 판단: 채택한다.

## 결정

### 1. Delete capability는 persisted target과 current cleanup state를 함께 읽어 발급한다

System cleanup delete authorizer는 같은 read-only Firestore transaction snapshot에서 다음을 읽는다.

1. 두 uniqueness index와 authoritative `cleanup_pending` receipt
2. Exact `started` cleanup attempt
3. Deterministic cleanup target document

Target의 canonical hash, receipt revision, cleanup owner, fencing token, lease expiry, attempt ID, mode와 origin을 current state와 다시 비교한다. `delete_candidate/planned` target만 delete capability를 받을 수 있다. `verified_empty`, `hold`, malformed·conflicting target은 provider delete 0이다.

Capability는 policy version, target hash, receipt revision, exact cleanup fence, raw/manifest lineage 전체와 짧은 expiry를 opaque seal에 묶는다. Target create capability, artifact read grant 또는 cleanup lease와 서로 교환되지 않는다. Provider boundary는 매 호출 직전에 capability와 target, 현재 시각을 재검증한다.

### 2. Provider delete는 generation과 metageneration을 모두 조건으로 건다

Cloud Storage adapter는 target 한 개에 대해 다음 형태만 허용한다.

```text
bucket.Object(exactPath)
  .Generation(exactGeneration)
  .If(GenerationMatch=exactGeneration,
      MetagenerationMatch=exactMetageneration)
  .Delete(ctx)
```

Path는 기존 exact artifact path validator를 통과해야 하고 generation과 metageneration은 모두 양수여야 한다. Latest-generation handle, prefix delete, bulk delete, rewrite와 soft-deleted restore/delete surface는 제공하지 않는다.

- `412`는 precondition drift다. 자동으로 최신 generation을 다시 선택하지 않는다.
- Direct delete `404`는 `not_found_observed`일 뿐 `confirmed_absent`가 아니다.
- `401/403`, `408/504`, `429`, cancellation과 나머지 provider 오류는 bounded redacted error로 보존한다.

Go Storage SDK v1.62.1은 generation-pinned delete를 idempotent operation으로 분류하며 HTTP delete에 generation과 두 match precondition을 전달한다. 이 SDK 동작도 dependency pin과 adapter test의 일부로 고정한다.

### 3. Raw-first 순서를 post-delete audit까지 포함해 강제한다

Executor는 target의 immutable lineage를 다음 순서로 처리한다.

1. Raw lineage가 있으면 exact conditional delete를 한 번 호출한다.
2. Raw exact path의 regular-version과 soft-deleted inventory를 별도 query로 다시 읽는다.
3. Raw path가 `confirmed_absent`일 때만 manifest 단계로 이동한다.
4. Manifest lineage가 있으면 exact conditional delete를 한 번 호출한다.
5. Manifest path도 같은 방식으로 post-delete audit한다.

Manifest-only target도 먼저 expected raw path가 비어 있음을 확인해야 한다. Raw-only target은 raw 확인 뒤 expected manifest path가 비어 있음을 확인한다. Target에 없는 경로를 추측하지 않으며 expected path는 persisted receipt/target binding에서만 가져온다.

Raw delete가 drift, provider error, cancellation 또는 incomplete audit로 끝나면 manifest delete call count는 0이다. Caller cancellation 뒤 late provider success 가능성이 있으면 결과는 unknown으로 남기고 다음 fresh attempt가 mutation보다 audit을 먼저 수행한다.

### 4. `confirmed_absent`는 complete empty inventory만 의미한다

한 exact path가 다음 조건을 모두 만족해야만 `confirmed_absent`다.

- regular-version query가 수행됐고 truncated되지 않았다.
- soft-deleted query가 수행됐고 truncated되지 않았다.
- 전체 coverage가 `complete`다.
- 두 candidate 집합이 모두 0이다.

Target generation이 soft-deleted 집합에 있거나 다른 generation이 같은 path에 하나라도 있으면 완료가 아니다. 기존 target을 새 generation으로 바꾸지 않고 hold/finding 후보로 반환한다. List `404`와 incomplete coverage는 absent가 아니라 audit unavailable이다.

Delete 2xx 뒤 complete empty와 delete 404 뒤 complete empty는 모두 provider 상태상 absent로 관측할 수 있지만, delete 전송 결과는 서로 다른 outcome으로 보존한다. Completion ledger는 이 차이를 지우지 않는다.

### 5. R8c success observation은 mutation completion 권한이 아니다

R8c local component는 두 expected path 모두 complete-empty로 감사된 호출에만 full `CleanupExecutionObservation`을 반환한다. 이 observation은 `plan_hash`, `target_hash`, 경로별 delete RPC outcome과 `completed_at`의 내부 일관성을 검사하는 **non-authoritative shape**다.

- full observation에서 허용하는 delete RPC outcome: `deleted_observed|not_found_observed|not_attempted`
- full observation에서 허용하는 audit outcome: `confirmed_absent`
- timeout, provider cancellation과 unavailable은 가능한 경우 complete-empty 재감사를 수행하더라도 bounded error로 종료하고 full success observation을 만들지 않는다.
- permission, quota와 `412`는 unknown/NotFound로 낮추지 않고 즉시 bounded error로 종료한다.
- `present|soft_deleted|unavailable`과 전체 `hold|retry` disposition의 durable 기록은 R8d outcome ledger가 fresh fence 아래 정의한다.

Shape validator는 공개 hash로 내부 일관성만 검사하므로 capability나 영속 완료 증거가 아니다. Observation과 error에는 좌표, raw body, Firebase UID/App ID, credential과 provider 원문 오류를 넣지 않는다. Path와 digest는 server-only plan/target에서만 다루고 사람 대상 리포트에는 원문을 복사하지 않는다.

이 result만으로 다음 mutation을 허용하지 않는다.

- Cleanup target 또는 attempt의 terminal 상태 기록
- Receipt `expired`
- Lease clear/renewal
- `purge_eligible_at` 설정
- Accepted/rejected-origin cleanup
- Scheduler, startup, readiness와 production 연결

[ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)은 immutable target을 유지한 채 exact cleanup attempt에 response-loss-safe execution ledger를 기록하고 fresh fenced completion transaction으로 `expired`를 commit하는 계약을 정의한다. 이 ADR-0025의 구현 증거와 ADR-0026의 설계 승인은 구분한다.

### 6. Official testbench와 staging 증거를 구분한다

Pinned official Cloud Storage testbench에서는 다음을 검증한다.

- exact generation+metageneration conditional delete
- wrong metageneration의 `412` drift
- absent exact generation의 direct `404`
- 다른 object path가 삭제되지 않음

Testbench는 현재 production의 `Versions`, `SoftDeleted`, `MatchGlob` inventory 의미를 완전히 재현하지 않는다. 따라서 다음은 실제 staging bucket에서 별도 gate를 통과하기 전 미검증이다.

- versioning on/off에서 noncurrent generation 의미
- soft delete on/off와 restore window
- lifecycle·retention lock과 delete IAM
- delete 2xx/404 뒤 regular+soft-deleted empty 판정
- late generation과 response-loss crash 재개

Staging gate 전에는 executor를 runtime에 연결하거나 `expired` completion을 열지 않는다.

## 구현 상태 — 2026-07-22

R8c local component는 commit `0d6ad55`에 구현됐다.

- Concrete `FirestoreAdmissionStore`가 current receipt·exact started cleanup attempt·persisted target을 한 transaction snapshot에서 읽은 뒤에만 30초 이하 opaque delete grant를 발급한다.
- Provider adapter는 exact path·generation·metageneration conditional delete만 노출하고 raw complete-empty audit 전 manifest 단계 진입을 금지한다.
- Raw-only는 expected manifest를 후속 감사하고 manifest-only는 expected raw를 먼저 감사한다. Soft-deleted 또는 late generation, incomplete/truncated inventory와 lineage drift는 fail-closed한다.
- Raw delete timeout/cancel/unavailable은 empty 재감사 뒤에도 bounded error로 종료해 manifest delete call을 0으로 유지한다.
- Local Go race, Firebase demo Firestore Emulator, pinned official Storage testbench와 GitHub clean CI 근거는 [EVD-20260722-033](../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit)에 기록한다.

이 R8c 결정 시점의 구현은 local component와 synthetic test boundary였다. 당시 미구현이던 Firestore attempt execution/outcome ledger와 local success-only receipt `expired` finalizer는 후속 [ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)~[ADR-0030](./ADR-0030-atomic-cleanup-expiry-finalization.md)에서 구현했다. Retry·hold, target 생성 전 cleanup lease renewal과 별도 release, purge, scheduler·startup·readiness, staging IAM·lifecycle·soft-delete drill과 production activation은 계속 미구현·미연결이다. Target in-place state update는 여전히 제외한다.

## 결과와 위험

- Exact target 외 generation과 prefix 전체를 삭제하는 surface가 없다.
- Raw 부재가 확인되기 전 manifest 삭제가 구조적으로 차단된다.
- 404와 timeout을 삭제 완료로 오인하지 않고 response loss를 재감사할 수 있다.
- Soft-deleted artifact를 개인정보 삭제 완료로 과장하지 않는다.
- 추가 inventory read 비용과 staging 운영 검증이 필요하다.
- Retention policy가 delete를 막으면 target은 hold/retry로 남으며 자동 우회하지 않는다.

## 연결 문서

- 선행 결정: [ADR-0024](./ADR-0024-immutable-cleanup-dry-run-target.md), [ADR-0018](./ADR-0018-generation-pinned-read-only-classifier.md)
- 실행계획: [Telemetry 복구 실행계획](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 규칙: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 증거: [EVD-20260722-033](../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit)
- 사람 대상 리포트: [HR-20260722-24](../reports/human/HR-20260722-24-generation-pinned-cleanup-delete.md)
- 제품 업데이트: 해당 없음 — runtime·사용자·운영 경로 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
