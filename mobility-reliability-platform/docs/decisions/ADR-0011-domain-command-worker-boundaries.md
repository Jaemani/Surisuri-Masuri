# ADR-0011: 도메인 명령 API와 비동기 worker 경계 분리

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0007](./ADR-0007-firebase-first-hybrid.md), [ADR-0009](./ADR-0009-fail-closed-ingest-kernel.md), [ADR-0010](./ADR-0010-authenticated-telemetry-references.md)

## 맥락

Go telemetry gateway는 모바일 raw GPS batch의 인증·scope 검증·멱등 receipt·Storage object에 집중하며 범용 비즈니스 CRUD를 담당하지 않는다. 그러나 v2 batch가 참조하는 server UUIDv7 `tripId`를 발급하고 수리·점검·부품 교체·동의 revision을 기록할 신뢰 가능한 command 경계가 필요하다.

또한 projection, 수리 importer, feature/fact/report 생성은 요청 응답 안에서 실행하기 어렵고 at-least-once retry·checkpoint·DLQ·replay 제어가 필요하다. 이 책임의 런타임을 정의하지 않으면 모바일 offline capture와 server trip의 연결, event replay, 배포·IAM·복구 소유가 문서상 비어 있게 된다.

## 검토한 선택지

1. telemetry gateway에 모든 비즈니스 command와 비동기 작업을 추가한다.
2. 모바일·콘솔이 Firestore에 수리·점검·동의·trip을 직접 쓴다.
3. telemetry gateway, domain command API, async worker를 별도 책임과 배포 단위로 나눈다.

## 결정

선택지 3을 채택한다.

### Domain Command API

- session-start/stop, 수리, 점검, 부품 교체, 동의 revision, purpose-limited PII read command를 처리한다.
- 모든 command는 Firebase ID token, App Check, active membership, role, 사람·기기 assignment와 목적을 검사한다.
- session-start는 server UUIDv7 `tripId`를 발급하고 모바일 `clientSessionId`, installation, device, consent revision을 연결한다.
- offline에서 server trip 발급 전 capture가 필요한 경우 모바일은 local-only session으로 보존하고, 인증된 reconciliation command가 성공하기 전 production telemetry batch로 전송하지 않는다.
- 성공한 state change는 immutable domain event와 idempotency receipt를 남기며 client가 server-only collection을 직접 수정하지 못한다.
- 초기 runtime 후보는 Firebase Functions v2 callable/HTTPS다. 구현 시 실제 latency·비용·공통 authz 요구가 맞지 않으면 별도 ADR로 Cloud Run service 전환을 결정한다.

### Async Workers

- projection, importer, feature, fact, report job을 Cloud Tasks/Pub/Sub의 at-least-once 전달을 전제로 처리한다.
- 각 worker는 idempotency key, checkpoint, processor version, retry policy, DLQ, correlation/replay run ID를 가진다.
- replay mode에서는 FCM, SMS, 외부 API 등 side effect를 실행하지 않는다.
- worker는 trigger, service account, IAM, image/function version, timeout, rollback을 독립 배포 manifest로 기록한다.
- CPU·시간 요구가 작은 event worker는 Functions v2 후보, 장시간 batch/import/model job은 Cloud Run Job 후보로 둔다. 선택은 workload 측정 후 고정한다.

### 공통 authorization

- telemetry gateway와 Domain Command API는 언어·runtime이 달라도 같은 role/scope test vector를 통과해야 한다.
- client `tenantId`는 후보 scope이며, active membership과 domain 관계가 최종 권한 근거다.
- 각 서비스의 Admin SDK 사용은 Security Rules 우회 권한이므로 직접 write target과 server-only field allowlist를 test로 고정한다.
- 복지관 tenant와 수리소 tenant가 다른 경우 수리사를 복지관 member로 암묵 편입하지 않는다. Domain Command API가 server-only `dataAccessGrant`의 제공·수신 tenant, 목적, 대상 기기, action, 만료·철회를 검사하며 QR public code만으로 cross-tenant 권한을 만들지 않는다.

## 결과

- telemetry ingest의 공격면과 책임이 좁게 유지된다.
- trip 발급과 offline local session reconciliation의 소유가 명확해진다.
- 수리·점검·동의와 raw GPS가 서로 다른 API로 들어와도 immutable event와 projection에서 합류한다.
- 비동기 실패를 request timeout과 분리하고 replay·DLQ·rollback을 독립적으로 검증할 수 있다.
- Functions/Go 서비스 사이에 authorization 정책이 어긋날 위험과 운영 단위가 늘어난다. 공통 test vector와 배포 manifest로 통제한다.

## 구현 전 필수 후속

- command JSON Schema와 idempotency receipt 계약
- session-start와 offline reconciliation sequence
- role·membership·assignment·consent allow/deny matrix
- cross-org `dataAccessGrant`와 QR lookup의 active/revoked/expired/target mismatch allow-deny matrix
- Functions v2 App Check enforcement와 emulator/debug-provider test 경로
- worker trigger·checkpoint·DLQ·replay side-effect test
- service별 IAM·cost·readiness·rollback runbook
