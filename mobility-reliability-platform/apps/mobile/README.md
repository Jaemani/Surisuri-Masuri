# Mobile app

React Native와 Expo 기반의 사용자·수리사 모바일 앱입니다. Android와 iOS를 함께 대상으로 하며, 기존 웹앱이나 외부 IoT/GPS 서비스에 의존하지 않는 신규 코드베이스입니다.

## 현재 상태

- foreground 위치 권한 상태와 명시적 주행 시작·종료 구현
- `watchPositionAsync` 위치 sample을 SQLite WAL event log에 append
- event payload와 delivery 상태를 분리한 local outbox 구현
- SQLite schema v3와 v1→v2→v3 atomic migration 구현. 기존 v1 session·event는 보존하되 모두 `local_only/not_applicable`로 고정
- server-bound session scope, append-only GPS event, canonical batch body·item membership, pending/lease/retry/ACK 상태의 DB 무결성 구현
- 같은 server-bound session의 pending GPS를 최대 500개 canonical body와 SHA-256으로 원자 저장하고 기존 active batch를 재발견하는 single-flight materializer 구현
- 앱 재시작 시 종료되지 않은 주행을 찾아 사용자가 재개·종료 가능
- Auth·기기·동의가 없는 현재 세션은 `development_local_only`로 저장해 후속 업로드 대상에서 제외
- Android application backup 비활성화
- Android 11 emulator에서 Expo Go 57로 권한 요청, 합성 foreground GPS 4건 저장,
  강제종료 뒤 active session·sample count 복구를 실제 확인
- Stored body exact SHA 재검증, canonical retry/lease metadata와 safe attempt gate,
  pending·expired lease 및 corruption parent/child hold 구현
- 전역 TaskManager, 2단계 위치 권한, foreground fallback, bounded callback queue와
  SQLite 단조 timestamp gate를 포함한 background 위치 수집 코드를 development
  build용으로 구현. Android/iPhone native lifecycle은 미검증
- Retry·ACK terminal store와 HTTP transport는 미착수

화면에는 원본 좌표를 표시하지 않고 저장된 sample 수와 **실제 server-bound upload 대기 수**만 보여줍니다. 현재 UI는 local-only session만 만들므로 이 값은 0이며, 개발 로그에도 좌표를 출력하지 않습니다.

## 모바일 수집 게이트

Foreground native smoke와 background source/static gate까지 진행했습니다. 다음
게이트에서 development build와 실제 장비로 아래 시나리오를 검증합니다.

- Android/iOS foreground 위치 권한
- background 위치 권한과 OS 설정 안내
- 화면 잠금, 앱 background, 프로세스 종료
- 네트워크 단절과 재연결
- GPS가 부정확하거나 권한이 철회된 상태
- 큰 글씨, 스크린리더, 최소 터치 영역

## 명령어

```sh
pnpm start
pnpm android
pnpm ios
pnpm typecheck
pnpm check
pnpm test
```

정적 검사와 Node SQLite schema 테스트는 Expo native SQLite나 실기기 background
GPS 동작을 증명하지 않습니다. `pnpm android`는 native development build이므로
Android SDK·ADB가 필요합니다. iOS native build는 macOS/Xcode 또는 승인된 EAS
development build에서 별도로 검증합니다.

WSL 저장소와 Windows Android emulator를 연결하는 재현 절차와 화면 근거는
[WSL Runbook](../../docs/development/WSL_RUNBOOK.md#android-에뮬레이터-빠른-데모)과
[EVD-20260723-048](../../docs/evidence/2026-07.md#evd-20260723-048--android-foreground-gps와-sqlite-재시작-복구-smoke)에 있습니다.

이 native smoke는 fresh SQLite v3 open과 현재 local-only foreground 흐름을
검증합니다. 기존 v1/v2 실제 파일 migration, background lifecycle, offline HTTP
reconnect, server ACK와 iPhone 동작은 검증하지 않습니다.

Background source gate는 global task 등록, Android foreground-service 권한, iOS
`UIBackgroundModes=location`, callback 직렬화·최대 100개, session 전 cached fix
차단, DB transaction의 timestamp 단조 증가와 좌표 없는 durable failure marker를
검증합니다. 이는 화면 잠금·앱 종료 뒤 native callback이나 배터리 결과가 아닙니다.
자세한 경계는 [EVD-20260723-050](../../docs/evidence/2026-07.md#evd-20260723-050--백그라운드-gps-정적-구현-경계)에 있습니다.

Upload lease는 현재 UI에서 호출되지 않습니다. Node SQLite test는 exact-body
SHA, pending·expired lease와 fail-closed hold를 검증하지만, 실제 두 Expo native
connection의 `BEGIN IMMEDIATE` 경쟁과 `busy_timeout` 동작은 development build에서
별도로 검증해야 합니다.

커밋 전의 unversioned SQLite prototype을 실기기에서 실행한 적이 있다면 현재 앱은 이를 자동 변환하지 않고 안전하게 중단합니다. Version 1 database는 v2를 거쳐 v3로 transaction migration하며 session·event·installation metadata를 보존하고 기존 delivery metadata는 non-deliverable state로 대체합니다. V2 batch body에 canonical scope 결함이 있으면 자동 수정·삭제하지 않고 migration을 중단합니다. Android/iPhone의 실제 v1/v2 파일 migration과 앱 재시작은 아직 별도 검증이 필요합니다.
