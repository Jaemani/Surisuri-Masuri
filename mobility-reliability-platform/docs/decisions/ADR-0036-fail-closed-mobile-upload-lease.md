# ADR-0036: Exact-body 재검증과 fail-closed 모바일 upload lease

- 상태: accepted
- 결정일: 2026-07-23
- 관련 결정: [ADR-0003](./ADR-0003-offline-event-sync.md), [ADR-0034](./ADR-0034-immutable-mobile-upload-body.md), [ADR-0035](./ADR-0035-single-flight-mobile-batch-materialization.md)

## 맥락

Materializer는 canonical body와 SHA-256을 SQLite에 저장하지만, 저장 시점의
검증만으로 전송 시점의 body 무결성을 증명하지 못한다. 앱 재시작, 파일 손상이나
예상하지 못한 local mutation 뒤에는 transport authority를 발급하기 직전에 exact
stored bytes를 다시 검사해야 한다.

SQLite의 `next_attempt_at`, `lease_expires_at`과 `attempt_count` 제약도 authority
판정에 충분하지 않다. Timestamp column은 arbitrary TEXT를 허용하며
`attempt_count >= 0`은 양수 REAL·일부 TEXT·JavaScript safe integer 밖의 SQLite
INTEGER까지 통과시킬 수 있다. SQL 문자열 시간 비교나 JS number 강제 변환은
backoff를 조기 무시하거나 lease를 잘못 takeover할 수 있다.

또한 `BEGIN IMMEDIATE` transaction 안에서 async SHA-256을 계산하는 동안 GPS
writer와 다른 sync writer가 같은 SQLite file을 사용할 수 있다. Busy policy가
connection마다 다르면 정상 GPS append가 즉시 `SQLITE_BUSY`로 실패할 수 있다.

## 검토한 선택지

1. 저장된 digest를 신뢰하고 body를 바로 HTTP transport에 넘긴다.
2. Body를 parse·reserialize한 뒤 새 digest와 비교한다.
3. 가장 오래된 active batch의 control metadata와 exact stored body를 하나의
   exclusive transaction에서 검증한 뒤에만 bounded lease를 발급한다.

## 결정

선택지 3을 채택한다.

- `pending|leased` 중 가장 오래된 active row를 시간 조건 없이 먼저 읽는다.
- 저장된 `next_attempt_at`과 `lease_expires_at`은 canonical UTC인지 검증한 뒤 epoch
  number로 due/expired를 판정한다. SQL TEXT ordering을 authority로 사용하지 않는다.
- 저장된 lease owner는 UUID v1~v8 shape를 요구한다.
- `attempt_count`는 raw number로 읽지 않는다. SQLite `typeof`와
  `CAST(... AS TEXT)`를 읽고 nonnegative safe integer이며 증가 여유가 있을 때만
  number로 변환한다.
- Malformed retry·lease·attempt metadata는 자동 복구하지 않는다. Bounded local
  error code로 parent batch를 먼저 `held`로 바꾸고 연결된 outbox 전체를 같은
  transaction에서 `held`로 전환한다.
- Stored body는 parse·reserialize하지 않고 exact JS string을 UTF-8 SHA-256으로
  계산한다. Stored digest는 lowercase 64 hex여야 한다.
- Body/digest mismatch는 `local_body_digest_mismatch`로 durable hold한다. SHA
  provider exception이나 malformed provider result는 disk corruption 증거가
  아니므로 transaction 전체를 rollback한다.
- 성공 시에만 UUID owner, 최대 5분의 canonical expiry와 `attempt_count + 1`을
  저장하고 방금 hash한 같은 body string을 transport용 결과로 반환한다.
- Pending lease와 expired takeover는 exact state, prior attempt, owner/expiry,
  body/digest와 retry metadata를 CAS한다. Expired takeover도 `state='leased'`를
  명시해 schema cardinality trigger를 다시 실행한다.
- Main GPS connection, materializer connection과 lease connection 모두
  `busy_timeout=5000`을 사용한다.

## 결과

- Stored body와 stored digest가 서로 일치하지 않으면 network authority가
  발급되지 않는다. Body와 digest를 함께 바꾸는 변조는 이 검사로 탐지하지
  못한다.
- Malformed control metadata가 lexical comparison으로 조기 takeover되거나 영구히
  candidate에서 사라지지 않고 operator-visible hold로 수렴한다.
- Hash provider 장애, invalid new owner/expiry와 cardinality failure는 attempt를
  증가시키지 않고 rollback한다.
- Node SQLite synthetic test는 exact bytes, mismatch hold, malformed metadata,
  provider rollback, future backoff, unexpired/expired lease, authority boundary와
  logical single winner를 검증한다.
- 실제 두 Expo `useNewConnection`의 `BEGIN IMMEDIATE` contention과 5초 busy timeout
  loser 동작은 Android development build에서 아직 검증하지 않았다. 현재 결과를
  native concurrency 완료로 해석하지 않는다.
- 이 SHA 검사는 accidental mismatch를 탐지한다. DB schema를 우회해 body와 digest를
  함께 바꾼 공격까지 인증하는 MAC이나 secure storage는 아니다.

## 관련 기록

- 제품 업데이트: [UPD-20260723-15](../product-updates/UPD-20260723-15-mobile-upload-lease.md)
- 증거: [EVD-20260723-049](../evidence/2026-07.md#evd-20260723-049--모바일-exact-body-upload-lease와-control-metadata-hold)
- 사람 대상 리포트: [HR-20260723-40](../reports/human/HR-20260723-40-mobile-upload-lease.md)
- 인시던트: 해당 없음 — 미배포 local 구현·리뷰 단계에서 발견·정정
