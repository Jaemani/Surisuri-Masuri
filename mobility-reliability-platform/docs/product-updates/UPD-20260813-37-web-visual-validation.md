# 제품 업데이트 — 모바일 웹 시각 검증 기반

- 업데이트 ID: `UPD-20260813-37`
- 업데이트일: 2026-08-13
- 상태: 개발 기반 완료, 현재 디자인은 재설계 대상
- 결정: [ADR-0042](../decisions/ADR-0042-web-ui-preview-boundary.md)
- 감사 근거: [모바일 앱 현재 UX 감사](../reports/human/HR-20260813-04-mobile-ui-audit.md)
- 증거: [EVD-20260813-004·005](../evidence/2026-08.md#evd-20260813-004--현재-모바일-실제-흐름-ux-baseline)

## 변경 내용

- Expo Web과 React Native Web 의존성을 추가했다.
- 네이티브 telemetry와 분리된 결정론적 웹 preview state를 추가했다.
- Playwright가 390×844에서 홈과 기록 중 화면을 캡처하고 대기→기록→종료 전이를 확인한다.
- Android 앱의 실제 다섯 단계 흐름을 캡처하고 UX·접근성 감사 보고서를 작성했다.

## 확인된 제품 문제

현재 UI는 GPS sample·upload queue·build mode를 사용자 과업보다 앞세운다. 시작 버튼은 첫 viewport 밖에 있고 종료 후 저장 확인이나 주행 요약이 없다. 현 snapshot은 유지할 디자인 기준이 아니라 재설계 전 baseline 증거다.

## 검증 결과

- Mobile unit: 15 files, 229 tests 통과
- Mobile TypeScript: 통과
- Expo Web export: 172 modules, 376KB JS bundle 성공
- Playwright Chromium: 홈·기록 중 390×844 snapshot 2장과 상태 전이 1 test 통과
- Android emulator: 대기·시작·합성 위치 3건·종료 캡처 완료
- 현장 사용자 검증: 미실시
- Android/iPhone background GPS: 이번 변경 범위에서 미검증

## 다음 제품 단계

세 가지 시각 방향 중 하나를 선택한 뒤 모바일을 `오늘`, `기록 중`, `주행 완료` 흐름으로 재구성한다. 같은 제품 언어를 복지관의 `오늘의 운영` 대시보드에 확장한다.
