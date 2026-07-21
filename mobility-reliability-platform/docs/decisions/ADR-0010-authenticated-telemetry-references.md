# ADR-0010: 인증 주체와 텔레메트리 참조 계약 분리

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0005](./ADR-0005-location-and-identity-separation.md), [ADR-0007](./ADR-0007-firebase-first-hybrid.md), [ADR-0009](./ADR-0009-fail-closed-ingest-kernel.md)

## 맥락

초기 `telemetry-batch.v1`은 UUID `actorId`, 자유 문자열 `consentVersion`, client `batchId`와 임의 `idempotencyKey`를 함께 받았다. Firebase ID token의 subject는 UUID가 아닌 Firebase UID이고, 정책 version은 특정 사람이 정밀위치 수집에 동의했다는 증거가 아니다. 또한 모바일의 local session·batch ID와 서버 domain ID를 같은 의미로 쓰면 offline 재시도와 Firestore aggregate 식별자가 결합된다.

production adapter를 이 계약에 맞추면 Firebase UID를 UUID로 위장하거나, raw 위치 object에 인증 identity를 넣거나, 유효한 동의 revision·기기 배정·서버 trip을 확인하지 못한 채 receipt를 만들 위험이 있다.

## 검토한 선택지

1. v1을 그대로 두고 `actorId`에 Firebase UID를 넣도록 UUID 검사를 완화한다.
2. v1 필드 의미를 같은 이름 아래에서 변경한다.
3. v1은 compatibility 기록으로 보존하고, identity와 local/server ID를 분리한 `telemetry-batch.v2`를 만든다.

## 결정

선택지 3을 채택한다.

### 인증과 권한

- Firebase ID token과 App Check token은 HTTP header에서 검증한다.
- 검증 결과 principal은 `FirebaseUID`와 `AppID`만 가진다. 둘은 raw telemetry JSON과 Storage object path에 넣지 않는다.
- `telemetry-batch.v2`에서 `actorId`를 제거한다.
- Firestore authorizer가 `/tenants/{tenantId}/memberships/{firebaseUid}`의 active membership, 서버 trip, 기기 배정, installation, 불변 정밀위치 `consentRevisionId`를 교차 확인한다.
- 권한 확인 전에는 idempotency index, receipt 또는 Storage object를 만들지 않는다.

### client ID와 server ID

- client가 생성한 UUIDv4 계열 correlation ID는 `clientSessionId`, `clientBatchId`, `clientSampleId`, `installationId`로 명시한다.
- 서버 domain ID인 `tripId`와 수신 `batchId`는 UUIDv7이다. `tripId`는 인증된 session-start command가 발급하고 batch는 그 값을 참조한다.
- request에는 `clientBatchId`만 포함하며 server `batchId`는 첫 reservation에서 생성한다.
- idempotency key는 client 문자열을 신뢰하지 않고 다음 UTF-8 문자열의 SHA-256 lowercase hex로 서버가 계산한다.

```text
schema_version + U+001F + tenant_id + U+001F + installation_id + U+001F + client_batch_id
```

- Firestore transaction은 derived idempotency index, `(tenantId, clientBatchId)` hash index와 server batch receipt를 원자적으로 연결한다.

### wire와 저장 형식

- v2 request는 `tenantId`, `deviceId`, `tripId`, `clientSessionId`, `installationId`, `consentRevisionId`, `clientBatchId`, `sentAt`, `samples`만 가진다.
- camelCase는 JSON/TypeScript wire에만 사용한다. Firestore document와 Storage manifest는 snake_case를 사용하며 adapter가 명시적으로 변환한다.
- Stage 1 raw object는 제한된 JSON batch를 deterministic gzip으로 저장한다. `.ndjson.zst`는 측정된 필요 없이 도입하지 않는다.
- object path에는 server `batchId`만 사용하고 Firebase UID, 이름, 전화번호, public QR code를 넣지 않는다.

## 결과

- 인증 identity와 raw 위치 payload가 분리된다.
- client local ID와 server aggregate ID를 둘 다 보존해 offline 재시도와 domain projection을 연결할 수 있다.
- 정책 version이 아니라 실제 consent revision을 권한 판단에 사용한다.
- v1 consumer와 v2 consumer가 같은 이름을 다르게 해석하지 않는다. gateway production ingest는 v2만 허용한다.
- session-start command, canonical Firestore Rules, Firebase authorizer와 두-key receipt transaction이 production adapter의 선행조건이 된다.
