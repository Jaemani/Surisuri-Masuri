# ADR-0035: 단일 활성 모바일 배치 materialization과 전용 SQLite 연결

- 상태: accepted
- 결정일: 2026-07-23
- 관련 결정: [ADR-0003](./ADR-0003-offline-event-sync.md), [ADR-0034](./ADR-0034-immutable-mobile-upload-body.md)

## 맥락

SQLite v2는 canonical body, digest와 batch item을 보존할 구조는 제공했지만 실제 pending GPS event를 배치로 만드는 runtime은 없었다. Materialization 도중 앱이 종료되거나 item 일부만 기록되면 body와 outbox가 서로 다른 상태가 될 수 있다. 이미 만든 body를 앱 재시작 뒤 다시 직렬화하면 서버의 raw-body 멱등성 경계도 깨진다.

Expo SQLite v57의 `withExclusiveTransactionAsync`는 별도 native connection을 만들지만 connection-local `PRAGMA foreign_keys`를 기본 연결에서 상속하지 않는다. 반대로 같은 path를 옵션 없이 다시 열면 cached native connection을 재사용할 수 있다. 따라서 transaction helper 이름만으로는 FK와 연결 격리를 동시에 보장할 수 없다.

SQLite v2의 초기 batch trigger에는 SQL `NULL` 3값 논리 결함도 있었다. JSON scope 비교가 `NULL`이면 `WHEN NOT (...)`이 참이 되지 않아 malformed body가 삽입될 수 있었다.

## 검토한 선택지

1. 전송 시점마다 pending event를 다시 읽어 body를 생성한다.
2. 여러 pending batch를 미리 생성하고 uploader가 병렬 처리한다.
3. 활성 batch 하나를 우선 재발견하고, 없을 때만 같은 session의 최대 500개를 한 transaction에서 materialize한다.

## 결정

선택지 3을 채택한다.

- `pending` 또는 `leased` batch가 있으면 metadata만 반환하고 UUID, clock, builder와 SHA provider를 다시 호출하지 않는다.
- 활성 batch가 없을 때 가장 오래된 pending server-bound GPS가 속한 session을 고른다. 다른 session의 event를 같은 batch에 섞지 않는다.
- 같은 session의 event를 `sample_sequence` 오름차순으로 최대 500개 선택한다.
- canonical builder로 body를 한 번 만들고 exact UTF-8 body의 lowercase SHA-256을 계산한다.
- batch strict insert, position `0..n-1` item strict insert와 outbox `pending→batched` trigger 결과 확인을 하나의 `BEGIN IMMEDIATE` transaction에서 수행한다. `OR IGNORE`, partial commit과 outbox 수동 전이는 허용하지 않는다.
- 반환값에는 body, digest와 좌표를 포함하지 않고 batch ID, session ID, count와 state만 포함한다. 실제 body read는 후속 lease·transport 경계에서만 수행한다.
- Expo runtime은 `{ useNewConnection: true }`로 전용 native connection을 열고 transaction 시작 전에 FK를 활성화·확인한다. Core도 transaction connection의 FK 상태를 다시 검사한다.
- Schema v3는 초기 batch trigger를 `COALESCE(..., 0)`으로 fail-closed하게 재생성한다. V2 database에 이미 malformed body가 있으면 ID나 body를 노출하지 않고 migration을 중단해 명시적 복구를 요구한다.
- 기존 `development_local_only` session은 materializer가 선택하지 않으며 server-bound로 승격하지 않는다.

## 결과

- 앱 재시작 후 활성 batch는 저장된 ledger에서 재발견되며 body를 다시 만들지 않는다.
- Item 중간 실패, digest 실패 또는 FK 비활성 연결은 transaction 전체 rollback으로 끝난다.
- 단일 활성 batch 정책은 단순한 복구 경계를 제공하지만 한 batch가 terminal 상태가 되기 전 다음 500개를 만들지 않는다. 처리량 확장은 측정 근거가 생긴 뒤 별도 결정한다.
- Node SQLite synthetic test는 schema, 순서, 500 cap, rollback과 재호출을 검증한다. Expo native connection·JSON1, OS 강제종료와 실제 서버 ACK는 아직 증명하지 않는다.
- Firebase Auth/App Check, server-managed assignment/current consent와 HTTP transport가 연결되기 전 현재 앱 flow는 계속 local-only이며 실제 upload batch를 만들지 않는다.

## 관련 기록

- 제품 업데이트: [UPD-20260723-13](../product-updates/UPD-20260723-13-mobile-upload-materializer.md)
- 증거: [EVD-20260723-047](../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3)
- 사람 대상 리포트: [HR-20260723-38](../reports/human/HR-20260723-38-mobile-upload-materializer.md)
- 인시던트: 해당 없음 — 미배포 local 개발 단계에서 발견·정정
