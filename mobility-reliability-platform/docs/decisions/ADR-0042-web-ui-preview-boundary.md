# ADR-0042: 모바일 웹 미리보기와 네이티브 검증 경계를 분리한다

- 상태: Accepted
- 결정일: 2026-08-13

## 맥락

React Native 모바일 앱의 화면을 Android 에뮬레이터에서만 확인하면 작은 UI 수정에도 빌드·부팅·ADB 조작이 필요하다. 반대로 브라우저는 백그라운드 위치, 모바일 OS 권한 수명주기, 네이티브 SQLite 복구를 동일하게 재현하지 못한다.

## 결정

1. Expo Web과 React Native Web을 모바일 UI의 빠른 시각 미리보기로 사용한다.
2. 웹에서는 `useTripRecorder.web.ts`가 결정론적 preview state를 제공한다.
3. 네이티브의 telemetry·SQLite·위치 권한·Task Manager 코드는 그대로 유지한다.
4. Playwright는 390×844 렌더링, 주요 버튼, 대기→기록 중→종료 상태 전이, 시각 회귀를 검증한다.
5. GPS 정확도, OS 권한, SQLite 영속성, cold recovery, 앱 종료·잠금·재부팅, 배터리는 Android/iPhone에서만 완료 판정을 내린다.

## 결과

웹 시각 검사는 UI 회귀 시간을 줄이지만 네이티브 telemetry 완료 증거를 대체하지 않는다. 보고서에는 `web preview`, `emulator`, `physical Android`, `physical iPhone` 증거 출처를 구분한다.

## 대안

- Android 에뮬레이터만 사용: UI 반복 속도가 느리다.
- 웹에서 Expo Location·SQLite를 그대로 사용: 브라우저 차이로 잘못된 완료 판단을 만들 수 있다.
- 모바일과 별도 웹 복제본 유지: 두 UI가 쉽게 달라진다.
