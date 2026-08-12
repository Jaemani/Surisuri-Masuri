# HR-20260813-08 — 수리 접수·복지관 action 화면 보고

- 기준일: 2026-08-13
- 상태: draft / 사람 검토 대기
- 환경: WSL2 / synthetic demo / web visual regression
- 실제 회의·production 배포·현장 사용자: 없음

## 결과

이용자가 증상과 지원금 의사를 직접 검토해 접수하고, 복지관은 수리소 배정과 제출 검증을 단계별 입력으로 수행하는 시연 가능한 화면을 구현했다.

## 검증

| 대상 | 결과 |
| --- | --- |
| 모바일 typecheck | 통과 |
| 모바일 test | 18 files / 246 tests 통과 |
| Android/iOS export | 양 플랫폼 bundle 통과 |
| 콘솔 typecheck/test/build | 10 tests 포함 통과 |
| Playwright | 모바일 2개, 콘솔 2개 통과 |
| 시각 증거 | 모바일 작성·검토, 콘솔 수리 배정 snapshot |

## 확인 가능한 시연

1. 모바일 수리 탭에서 기존 진행 요청을 보고 새 요청 작성을 연다.
2. 증상·설명·지원 여부·예상 금액을 입력하고 최종 검토한다.
3. 콘솔 수리 운영에서 합성 수리소·수리사를 골라 새 요청을 배정한다.
4. 배정 뒤 복지관 화면은 수리사 처리 대기로 바뀌며 수리사 단계를 대신 실행하지 않는다.

## 주장 제한

화면 데이터와 action 결과는 deterministic synthetic demo다. 실제 Firebase command 연결, 복지관 직원 처리, 수리사 제출, 보조금 검증 성과를 의미하지 않는다.

