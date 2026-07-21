---
id: ADR-0019
title: Current-state authorization for forward telemetry recovery
status: accepted
decided_at: 2026-07-21
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0019: Forward recovery의 current-state authorization

## 맥락

[ADR-0017](./ADR-0017-fenced-ingest-recovery.md)은 `reserved` receipt의 처리 소유권을 lease와 fencing token으로 정했고, [ADR-0018](./ADR-0018-generation-pinned-read-only-classifier.md)은 권한이 있는 caller만 generation-pinned artifact를 읽도록 opaque grant를 요구한다. 그러나 lease claim은 처리 경쟁의 승자를 정할 뿐 민감한 위치 artifact를 읽을 권한이 아니다.

기존 사용자 업로드 경로에는 Firebase ID token과 App Check에서 얻은 `Principal`을 tenant·beneficiary membership·installation·trip·assignment·현재 precise-location consent와 비교하는 authorizer가 있다. Recovery worker에는 사용자 token이 없으므로 다음 중 어느 것도 허용할 수 없다.

- worker를 사용자인 것처럼 만든 synthetic principal
- 과거 upload authorization 성공을 current permission으로 재사용
- stale sweeper candidate가 조립한 receipt revision·fence·artifact path 신뢰
- claim transaction과 독립적으로 읽은 관계 문서를 조합한 grant 발급

또한 `authorization` package가 이미 `ingest`를 import한다. Opaque grant mint 함수는 의도적으로 `ingest` 내부에만 있으므로 `ingest`가 기존 authorizer를 역으로 import하거나 Firebase adapter가 grant를 직접 만들면 import cycle 또는 capability 경계 약화가 생긴다.

## 결정 기준

- 개인정보: current authorization 전에 Cloud Storage call을 0으로 유지할 것
- 주체성: system worker가 Firebase 사용자를 가장하지 않을 것
- 원자성: receipt/index linkage, current fence와 관계 상태를 같은 Firestore transaction snapshot에서 확인할 것
- 계보: classifier request는 caller DTO가 아니라 authoritative receipt에서 만들 것
- 시간: grant는 lease·reservation·관계별 expiry보다 먼저 끝날 것
- 실패 의미: 정책 denial, trusted document 손상, provider unavailable과 caller context 종료를 섞지 않을 것
- 분리: accepted integrity audit과 forward recovery의 issuer·정책을 공유하지 않을 것

## 검토한 선택지

### 선택지 A: 사용자 upload authorizer에 synthetic principal 전달

- 장점: 기존 snapshot evaluator와 Firestore reader를 그대로 재사용할 수 있다.
- 단점: worker가 사용자를 가장하며 receipt/fence와 authorization read가 원자적이지 않다. `ClientSessionID` 등 일부 값은 current trip에서 다시 가져와 자기 자신과 비교하게 되어 original receipt lineage를 증명하지 못한다.
- 판단: 적용하지 않는다.

### 선택지 B: Firestore adapter가 정책을 평가하고 grant까지 발급

- 장점: 한 package에서 provider read와 정책을 끝낼 수 있다.
- 단점: opaque capability mint를 export하거나 Firebase package에 노출해야 한다. provider adapter가 authorization authority가 되어 domain boundary가 뒤집힌다.
- 판단: 적용하지 않는다.

### 선택지 C: provider-neutral system authorizer와 read-only transaction store 분리

- 장점: Firestore는 current fact를 원자적으로 읽고, `ingest` domain authorizer는 explicit system policy를 평가한 뒤 authoritative request와 opaque grant를 함께 만든다. 사용자 principal 없이도 beneficiary 관계를 current state로 확인할 수 있다.
- 단점: user-upload 정책과 일부 shape·시간 검사가 중복되며 별도 test matrix가 필요하다.
- 판단: privacy와 capability 경계를 보존하므로 채택한다.

## 결정

### 1. Authorizer API는 request와 grant를 함께 반환한다

`ingest` package에 다음 의미의 port와 authorizer를 둔다.

```go
type ForwardRecoveryAuthorizationStore interface {
    LoadCurrentForwardRecovery(
        context.Context,
        ForwardRecoveryAuthorizationQuery,
    ) (CurrentForwardRecoverySnapshot, error)
}

func (a *SystemRecoveryAuthorizer) Authorize(
    ctx context.Context,
    tenantID string,
    reservationKey string,
    lease LeaseGrant,
) (ArtifactClassificationRequest, ArtifactReadAuthorizationGrant, error)
```

- caller는 prebuilt `ArtifactClassificationRequest`를 전달하지 않는다.
- authorizer는 store가 반환한 authoritative receipt에서 request를 직접 만든다.
- `LeaseGrant`는 `sweeper` owner만 허용한다. HTTP request owner와 cleanup owner는 forward artifact-read grant를 받을 수 없다.
- authorizer만 `artifactReadGrantIssuerForwardRecovery`로 grant를 mint한다.
- 이 경계는 accepted receipt용 `artifactReadGrantIssuerAcceptedIntegrityAudit`을 절대 발급하지 않는다.

### 2. Firestore read는 하나의 read-only transaction이다

`FirestoreAdmissionStore`가 provider adapter로서 새 port를 구현한다. 한 transaction callback에서 다음을 순서대로 읽는다.

1. idempotency index
2. client-batch index
3. receipt
4. tenant, installation, trip, consent revision
5. installation의 Firebase UID로 찾은 membership
6. trip의 assignment ID와 person ID로 찾은 assignment와 pseudonymous consent-state projection

두 index와 receipt의 3-way linkage, receipt state/revision/fence, 관계 문서의 coherent read time을 같은 transaction snapshot에서 확인한다. create, update와 delete는 0건이다.

Membership은 worker identity로 사용하지 않는다. Installation에 현재 연결된 UID의 tenant membership이 active beneficiary이고 그 person이 trip·assignment의 person과 같은지 확인하는 **관계 사실**로만 읽는다. UID와 App ID는 snapshot 내부 검증을 벗어나 request, grant, result, log, metric과 사람 리포트로 나오지 않는다.

### 3. System policy는 명시적으로 별도 평가한다

Grant 발급 조건은 모두 충족돼야 한다.

- authoritative receipt와 두 index의 linkage가 유효하고 receipt가 `reserved`
- 입력 tenant와 reservation key가 receipt에 exact match
- receipt revision이 양수이고 current lease owner kind가 `sweeper`
- caller의 owner ID, fencing token과 lease expiry가 receipt의 current fence와 exact match
- current time이 lease expiry와 reservation deadline 이전임
- tenant가 active
- installation이 receipt tenant/installation과 일치하고 active·미폐기
- installation에 연결된 membership이 active beneficiary이며 current time과 receipt capture window에 유효
- trip이 receipt tenant/device/trip/installation/consent revision과 exact match
- trip person이 membership person과 같고 trip이 `recording|ended`, ingest expiry 이전
- receipt capture window가 trip start/end와 current time 규칙 안에 있음
- assignment가 trip의 exact assignment이고 device/person이 일치하며 current time과 receipt capture window에 active
- consent revision이 precise-location용 current granted revision이고 철회·만료되지 않음
- consent-state projection이 같은 person·purpose·revision의 current granted 상태
- trusted document의 ID, enum, timestamp, validity window와 server-owned label shape가 모두 유효

정책 불일치는 bounded unauthorized error로 닫는다. 필수 문서의 malformed shape, 불가능한 timestamp 관계, incoherent read time과 provider 실패는 unavailable이다. Caller context의 cancel/deadline은 표준 context error로 보존한다. Provider 원문 오류, document path, UID, App ID와 credential detail은 외부 error에 복사하지 않는다.

### 4. 시간과 TOCTOU를 보수적으로 제한한다

`checked_at`은 trusted application UTC clock과 Firestore read time의 큰 값으로 정한다. 두 시각의 차이가 승인된 clock-skew bound를 넘으면 grant를 발급하지 않는다.

Grant expiry는 다음 값 중 가장 이른 값이다.

```text
checked_at + short policy TTL
current forward fence expiry
reservation deadline
trip ingest expiry
membership valid-to
assignment valid-to
consent revision expiry
consent-state expiry
```

최솟값이 `checked_at`보다 늦지 않으면 unauthorized로 닫는다. Classifier는 이후 각 Storage provider boundary 전에 request binding, grant expiry와 forward fence expiry를 다시 검사한다. Claim 후 consent 철회, installation revoke, lease renew/takeover, receipt revision 증가 또는 cleanup transition이 일어나면 이전 grant/request는 exact binding을 잃거나 짧은 expiry에 도달해 재사용할 수 없다.

Claim transaction과 authorization transaction을 하나로 합치지는 않는다. Claim 뒤에 관계가 바뀔 수 있기 때문에 artifact read 직전 별도 transaction에서 current receipt/fence와 관계를 다시 읽는 것이 의도한 경계다.

### 5. Authoritative request 변환을 고정한다

Forward request는 receipt의 다음 값만 사용한다.

```text
receipt/reservation/state/revision,
tenant/device/trip/installation/batch/client-batch/consent IDs,
schema/validator/body hash/sample count/capture bounds,
created-at, artifact-expiry,
deterministic raw/manifest path,
current receipt fence
```

Accepted lineage는 항상 `nil`이다. Expected path는 receipt immutable input으로 다시 계산하며 저장된 임의 path를 신뢰하지 않는다. 완성된 request는 `ValidateArtifactClassificationRequest`를 통과해야만 grant mint로 진행한다.

## 구현·검증 gate

### Provider-neutral unit

- valid current snapshot만 request와 opaque forward grant 생성
- malformed 입력과 non-sweeper lease는 store call 0
- request가 authoritative receipt에서만 생성됨
- state/revision/owner/token/expiry mismatch와 clock skew fail-closed
- tenant·membership·installation·trip·assignment·consent·state deny/invalid-shape table
- 각 expiry source가 grant expiry를 clamp하는 boundary test
- context cancel/deadline 보존과 provider detail 비노출
- accepted-integrity issuer 발급 0

### Firebase adapter unit·emulator

- 두 index와 receipt의 3-way linkage 손상 거부
- claim 직후 authorization 성공
- claim 후 consent revision 교체·철회, installation revoke, membership revoke 또는 assignment 종료 시 grant 0
- renew/takeover/cleanup 뒤 stale fence/revision grant 0
- zero/incoherent read time과 missing/malformed document 분리
- transaction retry 때 current snapshot 재평가
- 모든 authorization path에서 Firestore mutation 0, authorization 전 Storage call 0

### Runtime 차단 조건

- provider-neutral unit, race, Firestore Emulator와 clean CI 전 worker wiring 금지
- startup composition test에서 authorizer를 우회한 classifier injection 금지
- staging ADC/IAM, transaction contention, clock와 current-consent race 검증 전 readiness 유지

## 결과와 위험

- 처리 소유권과 민감 artifact read capability가 분리된다.
- stale caller가 request revision이나 fence를 골라 grant를 받을 수 없다.
- system 정책과 user-upload 정책의 공통 규칙이 당분간 중복된다. 한쪽 변경 시 cross-policy conformance fixture로 차이를 드러내고, 의미가 완전히 같아질 때만 dependency-free policy package 추출을 검토한다.
- Membership read가 Firestore operation과 개인정보 접촉 범위를 늘린다. 그러나 revoked beneficiary가 위치 artifact를 계속 읽게 하는 것보다 안전하며 snapshot 밖으로 식별자를 내보내지 않는다.
- Firestore read 뒤 관계 변경의 TOCTOU를 완전히 제거하지는 못한다. 짧은 grant TTL, exact receipt/fence binding, 매 provider call 전 재검증과 worker 후속 fence mutation으로 범위를 제한한다.
- 이 결정과 local 구현은 production access, 실제 사용자 동의, staging Storage read 또는 runtime readiness를 증명하지 않는다.

## 연결 문서

- 선행 결정: [ADR-0017](./ADR-0017-fenced-ingest-recovery.md), [ADR-0018](./ADR-0018-generation-pinned-read-only-classifier.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 사전절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 제품 업데이트: 해당 없음 — 결정 문서이며 runtime 변경 없음
- 증거: [EVD-20260721-024](../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
