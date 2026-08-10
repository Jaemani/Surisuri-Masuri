# ADR-0036: Bounded scan·exact-body 재검증과 fail-closed 모바일 upload lease

- 상태: accepted
- 최초 결정일: 2026-07-23
- 보강 결정일: 2026-08-11
- 관련 결정: [ADR-0003](./ADR-0003-offline-event-sync.md), [ADR-0034](./ADR-0034-immutable-mobile-upload-body.md), [ADR-0035](./ADR-0035-single-flight-mobile-batch-materialization.md)
- 관련 구현 결정: [ADR-0039](./ADR-0039-atomic-mobile-upload-disposition.md)

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

이 결정을 실제 upload state에 연결하면서 두 가지 추가 문제가 드러났다. 첫 100개
active row가 모두 미래 retry/lease이면 101번째 이후의 due row가 계속 가려질 수 있고,
응답을 받은 뒤 ACK·retry·hold를 parent batch와 child outbox에 따로 기록하면 부분
완료가 남을 수 있다. SQLite migration 중 stale `user_version`을 읽는 초기화 경합과
terminal child의 위치 gap도 같은 fail-closed 경계에서 함께 다뤄야 한다.

## 검토한 선택지

1. 저장된 digest를 신뢰하고 body를 바로 HTTP transport에 넘긴다.
2. Body를 parse·reserialize한 뒤 새 digest와 비교한다.
3. 가장 오래된 active batch의 control metadata와 exact stored body를 하나의
   exclusive transaction에서 검증한 뒤에만 bounded lease를 발급한다.
4. 첫 bounded window가 막히면 전체 active set을 다시 authority로 삼아 무제한 scan한다.

## 결정

선택지 3을 채택한다. 단, bounded window 뒤의 due fallback과 terminal disposition은
다음 규칙으로 보강한다.

- `pending|leased` 중 생성 순서상 가장 오래된 active row를 최대 100개까지 읽어
  integrity/FIFO scan한다. 이 창 안에서 malformed row가 발견되면 해당 batch와 bound
  outbox를 hold하고, actionable row가 있으면 그 row를 먼저 처리한다.
- 창의 모든 row가 canonical하게 미래 상태여서 actionable row가 없을 때만
  `next_attempt_at`/`lease_expires_at` 조건을 쓰는 global due SQL prefilter로 가장 오래된
  후보를 찾는다. 이 query는 후보를 좁히는 용도일 뿐 authority가 아니며, 후보는 다시
  같은 JS canonical timestamp·attempt·body digest·binding·CAS 검사를 통과해야 한다.
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
- ACK·retry·hold disposition은 leased authority, session·sample count·attempt·owner·
  expiry·body digest와 `updated_at`를 함께 CAS한다. Parent upload batch와 bound outbox
  child는 하나의 exclusive transaction에서 terminal/재시도 상태로 전이한다.
- Commit 응답이 유실되면 rollback으로 단정하지 않는다. writer lifecycle을 닫은 뒤
  새 query-only read connection에서 expected terminal state를 correlation한다. 결과는
  `committed`, `not_committed`, `unverifiable` 세 가지로 제한하고, 확인할 수 없으면
  fail-closed error를 반환한다.
- Schema v4 migration은 writer lock을 잡은 뒤 `user_version`을 읽고, batch body/binding,
  terminal outbox state, child position의 `MIN=0`, `MAX=sample_count-1`, count 연속성을
  검사한다. 실패한 local state는 자동 복구하지 않고 migration을 거부한다.

## 결과

- Stored body와 stored digest가 서로 일치하지 않으면 network authority가
  발급되지 않는다. Body와 digest를 함께 바꾸는 변조는 이 검사로 탐지하지
  못한다.
- 첫 100개가 미래 상태여도 global due fallback이 101번째 이후 due row를 찾을 수
  있다. 다만 SQL prefilter가 canonical authority가 아니므로, fallback 후보의 malformed
  metadata·digest·binding은 여전히 hold 또는 rollback으로 끝난다.
- Malformed control metadata가 lexical comparison으로 조기 takeover되거나 영구히
  candidate에서 사라지지 않고 operator-visible hold로 수렴한다.
- Hash provider 장애, invalid new owner/expiry와 cardinality failure는 attempt를
  증가시키지 않고 rollback한다.
- Node SQLite synthetic test는 exact bytes, mismatch hold, malformed metadata,
  provider rollback, future backoff, unexpired/expired lease, authority boundary와
  logical single winner를 검증한다.
- Node SQLite synthetic test는 bounded window 뒤 101번째 due fallback, migration
  position gap/out-of-range와 stale initializer 방지, ACK·retry·hold parent/child
  atomicity, BEGIN/COMMIT 응답 유실 correlation도 검증한다.
- 실제 두 Expo `useNewConnection`의 `BEGIN IMMEDIATE` contention과 5초 busy timeout
  loser 동작은 Android development build에서 아직 검증하지 않았다. 현재 결과를
  native concurrency 완료로 해석하지 않는다. HTTP transport, Firebase Auth/App Check,
  Android/iPhone 실기기 E2E도 이 결정의 검증 범위가 아니다.
- Retry commit-response loss 직후 다른 worker가 재lease하면 기존 correlation state가
  사라져 `unverifiable`이 될 수 있다. 현재는 안전하게 실패하며, 재lease 이후에도
  lineage를 보존하는 ledger는 후속 결정으로 남긴다.
- 이 SHA 검사는 accidental mismatch를 탐지한다. DB schema를 우회해 body와 digest를
  함께 바꾼 공격까지 인증하는 MAC이나 secure storage는 아니다.

## 관련 기록

- 제품 업데이트: [UPD-20260723-15](../product-updates/UPD-20260723-15-mobile-upload-lease.md), [UPD-20260811-01](../product-updates/UPD-20260811-01-mobile-upload-disposition.md)
- 증거: [EVD-20260723-049](../evidence/2026-07.md#evd-20260723-049--모바일-exact-body-upload-lease와-control-metadata-hold), [EVD-20260811-001](../evidence/2026-08.md#evd-20260811-001--모바일-upload-disposition과-v4-state-integrity)
- 사람 대상 리포트: [HR-20260723-40](../reports/human/HR-20260723-40-mobile-upload-lease.md), [HR-20260811-01](../reports/human/HR-20260811-01-mobile-upload-disposition.md)
- 인시던트: 해당 없음 — 미배포 local 구현·리뷰 단계에서 발견·정정
