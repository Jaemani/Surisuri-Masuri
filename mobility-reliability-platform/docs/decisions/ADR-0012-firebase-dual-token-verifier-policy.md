# ADR-0012: Firebase dual-token verifier 실패·폐기 정책

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0009](./ADR-0009-fail-closed-ingest-kernel.md), [ADR-0010](./ADR-0010-authenticated-telemetry-references.md), [ADR-0011](./ADR-0011-domain-command-worker-boundaries.md)

## 맥락

Telemetry v2는 raw GPS body 밖의 HTTP header에서 Firebase ID token과 App Check token을 검증해야 한다. malformed·중복 header, SDK 인증서/JWKS 장애, 허용하지 않은 앱, emulator 환경변수의 production 유입을 서로 다른 실패로 다루되 token과 SDK 오류 문자열을 응답·로그에 노출하면 안 된다.

Firebase ID token의 폐기를 매 batch마다 원격 확인할지, cryptographic verification과 Firestore membership·installation 상태를 결합할지도 결정해야 한다. 위치 batch는 빈도가 높아 매 요청 원격 revocation check를 추가하면 latency·비용·provider coupling이 커진다.

## 결정

### Header와 principal

- `Authorization: Bearer <ID token>`과 `X-Firebase-AppCheck: <App Check token>`을 각각 정확히 하나만 허용한다.
- 중복, comma-combined, control/whitespace가 섞인 opaque token, token당 16KiB 초과는 SDK 호출 전에 거절한다.
- HTTP server 전체 header budget은 64KiB로 제한한다.
- ID token에서는 Firebase UID, App Check에서는 App ID만 provider-neutral `Principal`로 전달한다.
- App ID allowlist는 비어 있을 수 없고 trim 후 exact case match한다.

### 오류 계약

- malformed·missing·invalid token은 `401 unauthenticated`다.
- token은 유효하지만 App ID가 allowlist 밖이면 `403 forbidden`이다.
- Firebase Auth certificate provider를 사용할 수 없으면 `503 verifier_unavailable`이다.
- App Check SDK가 runtime JWKS 오류와 invalid token을 안정적으로 구분하지 못하는 경우 SDK adapter는 `401`로 보수적으로 처리한다. startup client 생성 실패는 readiness를 열지 않는다.
- 원본 token, App ID, SDK/provider 오류 문자열은 HTTP 오류 body와 구조화 로그에 넣지 않는다.

### Production factory와 emulator

- 외부 package가 사용하는 SDK constructor는 `NewProductionTokenVerifiers` 하나로 제한한다.
- factory는 ADC client 생성 전에 Auth, Firestore, Storage, Realtime Database, Pub/Sub emulator 환경변수가 하나라도 있으면 실패한다.
- App Check client가 background JWKS refresh에 사용하는 context는 process lifetime을 유지한다.
- 현재 verifier가 `cmd/server`에 연결되지 않은 동안 executable은 계속 `503 adapters_unconfigured`다. helper 단위검사를 active production guard라고 부르지 않는다.

### ID token 폐기

- 고빈도 telemetry request마다 `VerifyIDTokenAndCheckRevoked`를 호출하지 않고 `VerifyIDToken`으로 서명·issuer·audience·expiry를 검증한다.
- 즉시 접근 차단의 운영 원천은 batch마다 확인하는 active tenant membership, app installation 상태, device assignment, server trip과 consent revision이다.
- 계정 disable·refresh token revoke는 새 ID token 발급을 막지만 이미 발급된 token의 잔여 수명을 즉시 없애지 못할 수 있음을 수용하고 문서화한다.
- authorizer가 구현·연결되기 전에는 이 정책의 전제조건이 충족되지 않으므로 readiness와 ingest를 열지 않는다.
- 실제 pilot에서 threat model이 잔여 token 수명을 허용하지 못하면 cache된 revocation signal 또는 위험기반 remote check를 새 ADR로 검토한다.

## 검증 전략

- fake seam unit test: header parser, call short-circuit, allowlist, 401/403/503, sanitization, request context
- SDK wrapper unit test: UID/App ID mapping, nil client fail-closed, 오류 sanitization, production emulator guard
- HTTP integration test: verifier 오류가 body decode·ingestor 전에 안정적인 status/code로 변환됨
- staging E2E: 실제 ID token, App Check debug provider, 등록/미등록 앱, startup ADC/JWKS, inactive membership
- Go App Check emulator가 없으므로 fake test를 실제 App Check E2E로 표현하지 않음

## 결과와 한계

- malformed request가 Firebase SDK와 telemetry body 처리에 도달하기 전 차단된다.
- production emulator 우회 경로를 공개 factory에서 차단한다.
- App Check provider 장애를 항상 503으로 구분할 수 없고 ID token 잔여 수명 window가 존재한다.
- membership·installation·trip·consent authorizer와 실제 Firebase E2E가 다음 필수 gate다.
