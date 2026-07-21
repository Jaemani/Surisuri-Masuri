# Mobile app

React Native와 Expo 기반의 사용자·수리사 모바일 앱입니다. Android와 iOS를 함께 대상으로 하며, 기존 웹앱이나 외부 IoT/GPS 서비스에 의존하지 않는 신규 코드베이스입니다.

## 현재 상태

- 신규 모바일 기반 준비
- GPS 수집 미착수
- 오프라인 동기화 미착수

현재 화면은 위 상태를 그대로 표시합니다. expo-location과 expo-sqlite는 다음 vertical slice를 위한 호환 버전 의존성으로만 설치되어 있으며, 위치 권한 요청·좌표 수집·SQLite 저장은 아직 실행하지 않습니다.

## 첫 vertical slice

첫 구현 게이트는 명시적 주행 세션, 모바일 자체 GPS, SQLite outbox, 재연결 동기화입니다. 구현 전 다음 실기기 시나리오를 확인합니다.

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
```

실제 위치 수집은 권한·보존·삭제 정책과 검증 시나리오가 확정된 뒤 별도 변경으로 시작합니다.
