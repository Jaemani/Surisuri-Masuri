# ADR-0008: Foreground 우선 수집과 로컬 품질 게이트

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0002](./ADR-0002-mobile-gps-sessions.md), [ADR-0003](./ADR-0003-offline-event-sync.md)

## 맥락

Android와 iOS의 background 위치 수집은 권한 단계, 개발 빌드, 앱 종료와 제조사별 정책까지 함께 검증해야 한다. 이 복잡성을 첫 구현에 모두 넣으면 위치 권한·SQLite 순서·세션 복구의 기본 오류와 OS lifecycle 오류를 구분하기 어렵다. 또한 휴대폰 GPS는 좌표 범위 오류, 오래되거나 잘못된 timestamp, 매우 낮은 정확도를 만들 수 있다.

## 검토한 선택지

1. foreground와 background를 한 번에 구현한다.
2. 좌표를 즉시 서버로 보내고 서버에서만 품질을 판정한다.
3. foreground 수집과 로컬 event outbox를 먼저 검증하고 background·server sync를 후속 gate로 분리한다.

## 결정

선택지 3을 채택한다.

- 사용자의 명시적 시작·종료가 있는 foreground 세션을 첫 vertical slice로 구현한다.
- 초기 sampling은 High accuracy와 5m distance interval을 공통 요청하고, Android에서만 5초 time interval도 요청한다. iOS가 같은 5초 주기로 수집된다고 가정하지 않으며 실제 배터리·정확도 측정 전 최적값으로 주장하지 않는다.
- 위도·경도 범위와 양의 timestamp를 검증한다.
- horizontal accuracy가 `100m`를 초과하거나 음수·비정상 값이면 좌표를 저장하지 않고 reason만 event로 남긴다.
- 플랫폼이 accuracy를 제공하지 않은 `null` sample은 보존하되 후속 품질 처리에서 unknown으로 구분할 수 있게 한다.
- altitude·speed·heading의 비정상 optional 값은 sample 전체를 폐기하지 않고 `null`로 정규화한다.
- 앱 재시작에서 미종료 세션을 발견하면 자동 수집을 시작하지 않고 사용자에게 재개·종료를 선택하게 한다.
- event 순서와 accepted GPS sample 순서를 분리한다. 거부 reason과 session lifecycle event가 wire sample sequence에 구멍을 만들지 않게 한다.
- Auth·기기·동의가 아직 연결되지 않은 v0 세션은 installation ID와 함께 `development_local_only`로 고정하고 후속 uploader가 전송하지 못하게 한다.
- Android app backup은 비활성화한다. iOS SQLite file protection·backup 제외는 native 검증 전 열린 보안 gate로 유지한다.
- 원본 좌표는 화면, 일반 오류 메시지와 개발 로그에 표시하지 않는다.

## 결과

- foreground 권한, 명시적 세션, WAL 저장, 불변 이벤트, 재시작 복구를 background lifecycle과 분리해 검증할 수 있다.
- 100m·5초·5m 값은 v0 실험 기준이며 Android·iPhone 실기기 결과에 따라 새 증거와 함께 바꾼다.
- 현재 slice는 화면 잠금·background·강제 종료 중 지속 수집을 보장하지 않는다.
- 서버 acknowledgment가 없으므로 outbox event는 pending으로 남는다.
- OS provider 오류, 빠른 중복 동작과 종료 직전 callback에 대해 watcher 오류 처리, operation lock과 callback gate가 필요하다.
