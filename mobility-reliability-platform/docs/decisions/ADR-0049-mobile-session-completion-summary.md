# ADR-0049 — 주행 종료 결과를 사용자 확인 화면으로 남긴다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: React Native 사용자 홈, Expo Web preview, 로컬 telemetry session projection

## 맥락

주행을 종료하면 recorder가 `activeSession`을 비우고 대기 상태로 돌아간다. 내부 저장은 유지되지만 사용자는 방금 기록한 내용이 저장됐는지 확인할 수 없었다. 개발자용 샘플 수·큐 수치를 사용자 화면에 노출하는 것도 제품 목적과 맞지 않는다.

## 결정

- 종료 성공 시 마지막 `TripSessionSummary`를 `lastCompletedSession`으로 유지한다.
- 사용자 홈에는 `방금 이동 기록을 저장했어요`, 기록 시간, 휴대폰 저장 상태와 다음 전달 안내만 표시한다.
- raw coordinate, sample count, rejection count, session ID와 내부 delivery state는 사용자 화면에 표시하지 않는다.
- 실제 서버 전달 여부는 `pendingUploadCount`와 upload eligibility가 제공하는 범위에서만 안내하고, 연결되지 않은 데모에서는 과장하지 않는다.
- 앱을 새로 시작하면 완료 요약은 사라져야 하며, 영속적인 사용자 기록은 별도 기기 타임라인 projection이 담당한다.

## 결과와 제한

종료 직후 사용자가 기록 보존을 확인할 수 있고, 개발 진단 정보와 사용자 제품 언어가 분리된다. 거리·소요시간의 정밀 계산과 서버 동기화 상태는 후속 telemetry projection이 제공해야 한다. 현재 변경은 local/synthetic 및 native 코드 경계이며 실제 Android/iPhone lifecycle·Firebase upload를 증명하지 않는다.

관련: [ADR-0042](./ADR-0042-web-ui-preview-boundary.md), [HR-20260813-27](../reports/human/HR-20260813-27-mobile-session-summary.md)
