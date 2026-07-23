# ADR-0037: 명시적 권한과 fail-closed 백그라운드 GPS 수집

- 상태: accepted
- 결정일: 2026-07-23
- 관련 결정: [ADR-0002](./ADR-0002-mobile-gps-sessions.md), [ADR-0003](./ADR-0003-offline-event-sync.md), [ADR-0008](./ADR-0008-foreground-telemetry-slice.md)

## 맥락

Foreground 수집은 명시적 주행 세션과 SQLite event log를 먼저 검증했지만 화면이
꺼지거나 앱이 background로 이동하면 OS가 callback을 계속 전달한다는 보장이 없다.
반대로 background 위치를 기본 활성화하면 고령 사용자의 권한 이해를 어렵게 하고,
세션 밖 위치나 출발 전 cached fix를 저장할 위험이 있다.

Expo TaskManager callback은 비동기 경계에서 겹칠 수 있다. 각 callback이 같은
`last_sample_at` snapshot을 읽은 뒤 sample을 개별 저장하면 중복·역순 event와
projection timestamp 회귀가 생길 수 있다. Native callback·SQLite 오류를 단순히
무시하면 UI는 기록 중으로 보이지만 batch가 흔적 없이 사라질 수도 있다.

## 검토한 선택지

1. 실기기 검증 전까지 foreground 수집만 유지한다.
2. Native background callback을 별도 best-effort 저장소에 바로 쓴다.
3. 사용자가 별도로 권한을 허용한 세션만 global task로 받고, foreground와 같은
   append-only event log·sample policy를 사용하되 callback 순서·DB 단조성·실패
   상태를 fail-closed하게 관리한다.

## 결정

선택지 3을 채택한다.

- Task는 React component가 아니라 entry point에서 한 번 전역 등록한다.
- Foreground 권한과 background 권한을 두 단계로 분리한다. Background 권한이 없거나
  TaskManager를 사용할 수 없으면 명시적 foreground 세션으로 동작한다.
- Background task가 실행 중이어도 active session이 없거나 권한이 철회됐거나 durable
  task failure가 남아 있으면 앱 복귀 시 task를 중지하고 `ready_to_resume`으로 닫는다.
- Android는 위치 foreground service 알림을, iOS는 background location indicator를
  요청한다. 이 설정은 development build 대상이며 store release 설정이 아니다.
- 한 callback의 location은 timestamp로 stable sort하고 공통 좌표·정확도·시간 policy를
  적용한다. 명시적 `started_at`보다 이전 cached fix와 이미 저장한 timestamp 이하는
  replay로 취급해 event를 만들지 않는다.
- 한 native batch는 최대 100개로 제한한다. 같은 JavaScript runtime의 callback은 한
  queue로 직렬화한다.
- SQLite append transaction 안에서 최신 persisted `last_sample_at` 또는 `started_at`을
  다시 읽고 새 sample timestamp가 strict하게 증가할 때만 event와 projection을 함께
  갱신한다. 다른 runtime·writer와 겹쳐도 timestamp 회귀는 거부한다.
- Native 오류, malformed payload, 과대 batch와 processing failure는 좌표·원본 오류를
  저장하지 않는다. Bounded code와 시각만 `app_metadata`에 남기고 안전한 고정 오류로
  task를 종료한다.
- Failure marker가 있으면 이후 빈 batch나 정상 callback도 처리하지 않는 latch로
  유지한다. 사용자의 명시적 재시도 직전에만 marker를 지우며, 앱이 복귀했을 때
  marker가 남아 있으면 계속 기록 중이라고 표시하지 않는다.
- 현재 세션은 `development_local_only`다. Background 구현이 server upload 권한을
  만들거나 Firebase·HTTP transport를 자동 활성화하지 않는다.

## 결과와 한계

- Foreground·background가 같은 session/event schema와 개인정보 표시 규칙을 사용한다.
- 단일 JS runtime의 callback 순서와 SQLite projection timestamp 단조성은 코드·unit
  수준에서 고정된다. Batch 전체는 하나의 DB transaction이 아니므로 중간 sample 뒤
  실패하면 부분 저장과 durable failure가 함께 남을 수 있다.
- Expo development client가 추가하는 debug permission·local network·arbitrary-load
  설정은 개발 전용이다. Release profile은 별도 보안 검토와 native config 증거 없이
  승격하지 않는다.
- WSL에는 `adb`가 없어 Android development build를 설치하지 못했고, Linux에서는
  iPhone native build를 직접 검증하지 못했다. 화면 잠금, 앱 swipe/kill, 재시작,
  foreground 알림·iOS indicator, 권한 철회, 배터리와 실제 SQLite multi-runtime
  contention은 모두 미검증이다.
- OS callback 재전달과 batch retry 계약은 아직 없다. Durable marker는 silent loss를
  줄이는 현재-failure latch이지 이력 원장이나 delivery guarantee가 아니다. OS가 오류
  callback 자체를 보내지 않고 수집을 중단하는 liveness failure는 탐지하지 못한다.
- iOS SQLite backup 제외·file protection과 원본 좌표의 보존·삭제 정책은 아직
  증빙되지 않았다. Synthetic local-only 범위를 벗어난 GPS 수집 전에 별도 privacy
  gate로 확정해야 한다.

## 관련 기록

- 제품 업데이트: [UPD-20260723-16](../product-updates/UPD-20260723-16-background-gps-static-boundary.md)
- 증거: [EVD-20260723-050](../evidence/2026-07.md#evd-20260723-050--백그라운드-gps-정적-구현-경계)
- 사람 대상 리포트: [HR-20260723-41](../reports/human/HR-20260723-41-background-gps-static-boundary.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없는 local 구현·검증
