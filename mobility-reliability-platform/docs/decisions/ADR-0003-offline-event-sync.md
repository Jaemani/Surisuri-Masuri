# ADR-0003: Offline-first 이벤트 로그와 멱등 동기화

- 상태: accepted
- 결정일: 2026-07-21

## 맥락

주행 중 네트워크 단절, 앱 background 전환, 프로세스 종료가 발생할 수 있다. 위치 이벤트의 유실과 중복은 누적거리와 위험도 계산을 왜곡한다.

## 검토한 선택지

1. 위치 샘플을 발생 즉시 개별 API로 전송
2. 연결 상태일 때만 세션을 허용
3. SQLite append-only log와 batch acknowledgment 사용

## 결정

선택지 3을 채택한다.

- 모바일 이벤트는 안정적인 client event ID와 sequence를 갖는다.
- 서버 acknowledgment 전에는 로컬에서 삭제하지 않는다.
- batch마다 tenant 범위의 idempotency key를 부여한다.
- 서버는 전체 수신과 일부 거부 결과를 명시적으로 반환한다.
- 순서가 바뀐 이벤트도 저장한 뒤 projector가 일관되게 처리한다.

## 결과

- 모바일과 서버 모두 동기화 상태머신이 필요하다.
- 중복 전송은 정상 복구 동작으로 간주한다.
- raw event, receipt, projection을 별도로 관측한다.
