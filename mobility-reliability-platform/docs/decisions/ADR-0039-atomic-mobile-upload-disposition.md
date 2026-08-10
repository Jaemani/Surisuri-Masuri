# ADR-0039: 모바일 upload disposition의 atomic 전이와 commit correlation

- 상태: accepted
- 결정일: 2026-08-11
- 로드맵 위치: M3 (7월 sync·recovery gate, 실제 코드 정리일 8월 11일)
- 관련 결정: [ADR-0034](./ADR-0034-immutable-mobile-upload-body.md), [ADR-0035](./ADR-0035-single-flight-mobile-batch-materialization.md), [ADR-0036](./ADR-0036-fail-closed-mobile-upload-lease.md)

## 맥락

모바일 local outbox는 upload lease를 얻은 뒤 서버 응답을 `acknowledged`, `retry` 또는
`hold`로 반영해야 한다. Parent upload batch와 bound event outbox를 서로 다른 write로
처리하면 ACK 일부만 저장되거나 parent와 child가 다른 상태로 남을 수 있다. SQLite의
`COMMIT` Promise가 거부되었다는 사실만으로 transaction이 commit되지 않았다고 단정할
수도 없다.

현재 대상은 HTTP transport나 Firebase 인증이 아니라 local SQLite state machine의
persistence boundary다.

## 결정

- Command는 client batch/session/sample count, attempt, lease owner/expiry, body digest와
  canonical `observedAt`를 요구한다. Authority가 leased prestate와 일치하지 않으면
  전이를 거부한다.
- `acknowledged`는 parent에 receipt/server batch/state/replay를 기록하고, 같은
  transaction에서 정확히 bound된 sample 수의 child outbox를 acknowledged로 바꾼다.
- `hold`는 parent와 child 모두에 같은 low-cardinality error code를 남기고 retry를
  차단한다. `retry`는 최대 15분 이내의 canonical `next_attempt_at`을 기록한다.
- 각 update는 state, authority, digest, binding count/position과 이전 시각을 CAS로
  확인한다. Parent 또는 child 변화 수가 예상 sample 수와 다르면 전체 transaction을
  rollback한다.
- Writer는 `foreign_keys=ON`과 `busy_timeout=5000`을 확인한다. `BEGIN IMMEDIATE`가
  open 이후 reject할 수 있으므로 transaction marker를 유지하고 compensating rollback을
  시도한다.
- `COMMIT` 응답이 유실되면 rollback하지 않는다. Writer를 닫고 새 `query_only` read
  connection에서 expected state를 읽는다. `committed`만 성공으로 반환하며,
  `not_committed`와 `unverifiable`은 fail-closed error로 처리한다.
- Retry commit-response loss 뒤 다른 worker가 재lease해 expected state가 사라지는
  경우도 `unverifiable`로 닫는다. 재lease 이후 lineage를 보존하는 ledger는 후속
  결정의 범위다.

## 결과와 제한

ACK·hold·retry의 parent/child 부분 전이가 local transaction 경계에서 거부된다.
Schema v4 migration은 terminal binding과 child position 연속성을 검사하므로 잘못된
state를 조용히 정상으로 승격하지 않는다. 이 결정은 HTTP, 서버 idempotency, Firebase
Auth/App Check, staging/production 배포를 포함하지 않는다.

## 검증

- WSL2 local Node SQLite synthetic fixtures에서 atomic disposition, v4 migration audit,
  bounded due fallback과 commit-response loss correlation을 검증했다.
- 코드 commit은 [`a9a57cc`](https://github.com/Jaemani/Surisuri-Masuri/commit/a9a57ccb424ad6b6b66983e201436990d426e970)이다.
- Mobile 15개 test file/229 tests, TypeScript, Android·iOS Expo static export,
  workspace check/test 및 Firebase Rules 24 tests가 통과한 실행 기록은
  [EVD-20260811-001](../evidence/2026-08.md#evd-20260811-001--모바일-upload-disposition과-v4-state-integrity)에 묶었다.
- 실제 Expo multi-connection contention, HTTP offline→reconnect, Firebase Auth/App
  Check, Android/iPhone 실기기 E2E는 미검증이다.

## 관련 기록

- 제품 업데이트: [UPD-20260811-01](../product-updates/UPD-20260811-01-mobile-upload-disposition.md)
- 사람 대상 리포트: [HR-20260811-01](../reports/human/HR-20260811-01-mobile-upload-disposition.md)
- 증거: [EVD-20260811-001](../evidence/2026-08.md#evd-20260811-001--모바일-upload-disposition과-v4-state-integrity)
- 인시던트: 해당 없음 — local 구현·검증 중 발견한 오류이며 배포·사용자·기관 운영 영향 없음
