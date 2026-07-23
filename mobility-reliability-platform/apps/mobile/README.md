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
- lease·backoff·ACK store, HTTP transport와 background 위치 수집은 미착수

화면에는 원본 좌표를 표시하지 않고 저장된 sample 수와 **실제 server-bound upload 대기 수**만 보여줍니다. 현재 UI는 local-only session만 만들므로 이 값은 0이며, 개발 로그에도 좌표를 출력하지 않습니다.

## 첫 vertical slice

현재 구현은 foreground vertical slice입니다. 다음 게이트에서 실제 장비와 server sync를 아래 시나리오로 검증합니다.

- Android/iOS foreground 위치 권한
- background 위치 권한과 OS 설정 안내(후속 개발 빌드)
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

현재 정적 검사와 Node SQLite schema 테스트는 Expo native SQLite나 실기기 GPS 동작을 증명하지 않습니다. Android는 WSL2와 Windows ADB를 연결하고, iPhone background 기능은 EAS development build에서 별도로 검증합니다.

커밋 전의 unversioned SQLite prototype을 실기기에서 실행한 적이 있다면 현재 앱은 이를 자동 변환하지 않고 안전하게 중단합니다. Version 1 database는 v2를 거쳐 v3로 transaction migration하며 session·event·installation metadata를 보존하고 기존 delivery metadata는 non-deliverable state로 대체합니다. V2 batch body에 canonical scope 결함이 있으면 자동 수정·삭제하지 않고 migration을 중단합니다. Android/iPhone의 실제 v1/v2 파일 migration과 앱 재시작은 아직 별도 검증이 필요합니다.
