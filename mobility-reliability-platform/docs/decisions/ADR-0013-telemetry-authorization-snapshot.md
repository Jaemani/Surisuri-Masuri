# ADR-0013: 텔레메트리 권한 snapshot과 현재 동의 계약

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0010](./ADR-0010-authenticated-telemetry-references.md), [ADR-0011](./ADR-0011-domain-command-worker-boundaries.md), [ADR-0012](./ADR-0012-firebase-dual-token-verifier-policy.md)

## 맥락

유효한 Firebase ID token과 App Check token은 호출 주체와 앱을 증명하지만, 특정 기관·설치·전동보장구·trip·정밀위치 동의에 대한 현재 권한까지 증명하지 않는다. 기존 도메인 모델에는 app installation의 정확한 저장 필드, trip이 참조할 기기 배정 ID, 종료 후 지연 업로드 기한, 불변 consent revision의 현재 상태 projection이 빠져 있었다.

또한 GPS 좌표를 권한 판단기에 전달하면 거절 경로와 운영 오류에 민감한 위치값이 섞인다. 반대로 sample 시각을 전혀 전달하지 않으면 동의 전 또는 trip 범위 밖에서 수집된 batch를 구분할 수 없다.

## 검토한 선택지

1. Firebase token만 검증하고 request의 tenant·trip·consent ID를 신뢰한다.
2. 과거 consent revision 한 건과 `(device_id, person_id)` query만 검사한다.
3. server-owned exact reference와 현재 consent projection을 두고, 식별자·sample 시간 범위만으로 authorization snapshot을 평가한다.

## 결정

선택지 3을 채택한다.

### 초기 허용 주체

- telemetry upload는 active membership의 `beneficiary` role이면서 `membership.person_id == trip.person_id`인 본인 사용 흐름만 허용한다.
- guardian·case worker·repairer·관리자의 대신 업로드는 자동 허용하지 않는다. guardian 관계 upload가 필요하면 `personRelationships`와 별도 allow matrix를 후속 ADR로 추가한다.
- tenant도 `status == active`여야 한다. suspended·closed tenant의 active membership은 권한을 만들지 않는다.

### App installation

`/tenants/{tenantId}/appInstallations/{installationId}`는 다음 server-owned 필드를 사용한다.

```text
installation_id, tenant_id, firebase_uid, app_check_app_id,
status(active|revoked), registered_at, last_verified_at?, revoked_at?,
schema_version, created_at, updated_at, revision
```

active 문서에 `revoked_at`이 있거나 UID·App ID·tenant·document ID가 하나라도 다르면 거절한다. membership document ID로 사용할 Firebase UID는 비어 있거나 slash/control character를 포함할 수 없다.

`schema_version`과 `revision`은 양수여야 하고 `created_at <= registered_at`, `created_at <= updated_at`이어야 한다. 핵심 필드가 맞더라도 이 metadata가 빠진 legacy/partial document는 authorization dependency 오류로 닫는다.

### Device assignment와 trip

`deviceAssignments`의 초기 계약은 다음과 같다.

```text
assignment_id, tenant_id, device_id, person_id,
assignment_type(primary_user|temporary_user),
status(active|ended|revoked), valid_from, valid_to?
```

trip은 query 대신 exact read가 가능하도록 `device_assignment_id`를 불변 reference로 가진다.

```text
trip_id, tenant_id, device_id, person_id, device_assignment_id,
installation_id, client_session_id, consent_revision_id,
started_at, ended_at?, capture_mode(foreground|background|reconciled_offline),
status(recording|ended|cancelled), ingest_expires_at
```

- `recording`과 `ended`만 ingest 가능하며 `now < ingest_expires_at`이어야 한다.
- `ended`에는 `ended_at`이 필수다.
- session-start/reconciliation command가 `ingest_expires_at`을 server time으로 정한다. 운영 기본값은 종료 후 72시간이고, 최대 허용치는 파일럿 전에 별도 retention 설정으로 고정한다.
- batch의 최소·최대 `capturedAt`만 권한 판단기에 전달한다. 좌표·속도·정확도·raw body는 전달하지 않는다.
- 첫 sample은 `started_at`보다 이르면 안 되고, ended trip의 마지막 sample은 `ended_at`보다 늦으면 안 된다. 진행 중 trip은 기기 시계 오차로 server now 이후 최대 5분까지만 허용한다.

### 현재 동의 projection

과거 `granted` revision이 immutable이므로 revision 한 건만으로 철회를 감지하지 않는다. server-only `/tenants/{tenantId}/consentStates/{derivedId}`를 추가한다.

```text
tenant_id, person_id, purpose_code,
current_revision_id, status(granted|denied|withdrawn|expired),
effective_at, expires_at?, updated_at, revision
```

`derivedId`는 `sha256(person_id + U+001F + purpose_code)` lowercase hex다. Domain Command API는 새 consent revision과 current state 전환을 같은 Firestore transaction에 기록한다.

authorizer는 참조 revision과 current state가 모두 같은 tenant·person·`precise_location` 목적·revision을 가리키고 현재 `granted`이며 미철회·미만료인지 검사한다. 동의는 첫 sample 이전에 효력이 있어야 한다. upload 전에 철회되면 과거에 수집된 미전송 batch도 받지 않는다.

### 오류와 읽기 방식

- exact path not-found, inactive, expired, relationship mismatch는 외부에 세부를 밝히지 않고 하나의 `batch_unauthorized` 403으로 처리한다.
- Firestore 장애, IAM 오류, decode 오류, unknown enum, 모순된 server document는 저장을 중단하고 generic `ingest_unavailable` 503으로 처리한다.
- snapshot reader는 좌표/body를 받지 않으며 짧은 request-derived timeout 안에서 exact document read를 수행한다.
- 첫 구현은 Firestore `GetAll`의 strongly consistent exact reads를 사용한다. adapter 오류 문자열·문서 path·UID·App ID는 HTTP 응답에 전달하지 않는다.

### 원자성과 rollout gate

read-only authorizer 반환 후 receipt reservation 전 membership·installation·consent가 철회될 수 있는 TOCTOU가 남는다. 따라서 이 authorizer만 연결해 production ingest를 열지 않는다.

다음 단계의 Firestore read-write transaction이 authorization 문서를 다시 읽고 `ingestIdempotency`, `ingestClientBatches`, `ingestReceipts` create를 같은 callback에서 수행해야 한다. callback 안에서는 외부 side effect를 금지하고 Cloud Storage write는 commit 뒤에 실행한다. 이 transaction adapter와 Storage `DoesNotExist` adapter가 모두 준비되기 전 readiness는 503을 유지한다.

## 결과

- 유효 token을 가진 비활성 사용자, 폐기된 앱 설치, 다른 사람의 기기/trip, 철회된 현재 동의가 원본 저장 전에 차단된다.
- Firestore authorization read가 query 없이 bounded exact path로 수렴한다.
- sample 시간은 검사하면서 원본 위치는 권한 경계에서 분리된다.
- guardian 대신 업로드와 장기 offline upload는 초기 범위에서 제한된다.
- 현재 단계는 정책·adapter 검증 단계이며 production authorization 활성화를 의미하지 않는다.

## 필수 검증

- active·future·expired·revoked membership/assignment/installation/consent 경계
- UID·App ID·tenant·person·device·trip·session·revision 각각의 mismatch
- current consent revision 전환과 철회
- sample 시작·종료·미래 시각 경계
- malformed server document와 Firestore unavailable의 generic 503 분류
- 모든 deny/error case에서 receipt/object write 0건
- authorization read와 receipt reservation 사이 철회 race를 read-write transaction 통합 테스트로 재현
