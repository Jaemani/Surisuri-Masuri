# ADR-0034: 모바일 업로드 본문 영속화와 정확한 ACK 경계

- 상태: accepted
- 결정일: 2026-07-23
- 관련 결정: [ADR-0003](./ADR-0003-offline-event-sync.md), [ADR-0008](./ADR-0008-foreground-telemetry-slice.md), [ADR-0009](./ADR-0009-fail-closed-ingest-kernel.md)

## 맥락

서버는 `clientBatchId`뿐 아니라 수신한 JSON 원문의 해시도 멱등성 경계로 사용한다. 모바일이 재시도 때 객체를 다시 직렬화하면 의미가 같은 샘플도 키 순서, 누락된 optional field 또는 런타임 여분 field 때문에 다른 원문이 될 수 있다. 이 경우 이미 처리된 배치를 안전한 replay로 확인하지 못하고 conflict가 된다.

현재 기기에 저장된 세션은 모두 `development_local_only`다. Firebase 사용자·기기·주행·동의가 연결되지 않은 기존 좌표를 후속 코드가 자동 업로드 대상으로 바꾸면 안 된다.

## 검토한 선택지

1. 재시도할 때마다 SQLite sample을 읽어 JSON을 다시 생성한다.
2. sample 단위로 즉시 전송하고 실패한 sample만 다시 만든다.
3. 고정 field 순서의 batch 원문을 한 번 만들고, 원문과 digest를 ACK 전까지 영속화해 그대로 재시도한다.

## 결정

선택지 3을 채택한다.

- wire builder는 허용된 field만 고정 순서로 명시 투영한다. 객체 spread, runtime extra field와 호출자 property 순서는 wire bytes에 영향을 주지 않는다.
- optional sensor field는 canonical representation으로 정규화한다. `activityHint` 기본값은 `unknown`, nullable sensor 값은 `null`이다.
- builder의 전송 산출물은 `clientBatchId`, `sampleCount`, exact JSON `body`다. 수정 가능한 typed batch를 재시도 경계에 노출하지 않는다.
- 후속 SQLite store는 exact `body`와 digest를 ACK 전까지 보존한다. 앱 재시작과 lease 만료 뒤에도 body를 재생성하지 않는다.
- `200` 또는 `202`라도 response의 `clientBatchId`, `sampleCount`, receipt·server batch ID와 허용 state가 모두 일치할 때만 ACK한다. 불완전한 성공 응답은 같은 body로 재조회한다.
- `409`는 서버의 bounded error code를 읽어 idempotency, client-batch, object conflict를 구분하고 자동 재시도하지 않는다. 오류 detail과 원본 좌표는 복사하지 않는다.
- `401`은 token 갱신 경계, `403`과 계약 위반 4xx는 hold, `408`·`429`·5xx와 transport failure는 동일 body retry로 분류한다.
- 기존 `development_local_only` session을 server-bound로 승격하는 migration이나 API는 만들지 않는다. 업로드 가능한 새 session은 Firebase Auth·App Check와 server-managed assignment·consent scope가 연결된 후 별도로 생성한다.

## 결과

- 모바일 재시도와 서버 raw-body 멱등성 정의가 일치한다.
- exact body의 SQLite migration, digest 검증, lease/backoff/ACK 전이는 후속 구현 gate다.
- 현재 구현은 pure protocol이며 네트워크 전송, Firebase token, 실기기 재시작, 서버 ACK를 증명하지 않는다.
- 서버가 응답 계약을 바꾸면 모바일이 성공으로 추정하지 않고 같은 body 재시도 상태에 머문다.
