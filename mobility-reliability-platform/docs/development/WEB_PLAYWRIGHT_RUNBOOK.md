# Expo Web + Playwright 실행 가이드

## 목적

React Native 모바일과 복지관 콘솔을 브라우저에서 빠르게 확인하고 390×844 모바일·1440×1024 콘솔 시각 회귀 테스트를 실행한다. 웹 preview는 UI와 demo 상태 전이만 검증한다.

## 최초 설치

```bash
pnpm install --frozen-lockfile
pnpm exec playwright install chromium
```

WSL에서 CDN 다운로드가 중단됐지만 full Chromium이 설치된 경우 실행 파일을 지정할 수 있다.

```bash
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH="$HOME/.cache/ms-playwright/chromium-REVISION/chrome-linux64/chrome" pnpm test:e2e
```

revision은 설치 버전에 따라 달라지므로 저장소 설정에 고정하지 않는다.

## 웹 미리보기

```bash
pnpm --filter @mobility-reliability/mobile run web:e2e
```

브라우저에서 `http://localhost:19006`을 연다. 웹은 네이티브 DB가 아니라 결정론적 preview state를 사용한다.

복지관 콘솔은 별도 터미널에서 실행한다.

```bash
pnpm --filter @mobility-reliability/console run web:e2e
```

브라우저에서 `http://localhost:19007`을 연다.

## 테스트

```bash
pnpm test:e2e
```

의도한 디자인 변경 후 기준 이미지를 갱신할 때만 다음을 사용한다.

```bash
pnpm test:e2e:update
```

기준 이미지 갱신 전 모바일의 수리·지원금 우선순위와 콘솔의 오늘 할 일·수리 운영 화면을 직접 열어 잘림, 카피, 글자 크기, 핵심 버튼 위치를 확인한다.

## 네이티브 검증 전용

- Android/iPhone 위치 권한과 background GPS
- SQLite 영속 저장과 복구
- 화면 잠금·앱 종료·재부팅
- 실제 GPS 정확도와 배터리 사용량

웹 통과만으로 위 항목을 완료 처리하지 않는다.
