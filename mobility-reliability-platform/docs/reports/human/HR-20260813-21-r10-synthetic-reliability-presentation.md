# HR-20260813-21 — 합성 reliability 기준선 비교 화면 점검

- 발행일: 2026-08-13
- 상태: generated / 사람 검토 대기
- 대상 독자: 프로젝트 운영자·복지관 화면 검토자
- 로드맵 게이트: R10 Device Timeline & Reliability

## 현재 실제 상태

보고서 화면에서 고정 180일, 30일 예상 누적 1,000km, Kaplan–Meier 세 기준선을 합성 test aggregate로 비교할 수 있다. 배터리는 동일한 test 8건의 sensitivity·specificity·Brier를 보여주고, controller는 train 표본 3건이 최소 4건보다 적어 metric 없이 판단을 유보한다.

화면 상단은 전체 자료가 synthetic-only이고 배포가 보류됐음을 알린다. 학습 기준과 test 지표는 분리되며 개별 사람·기기 결과나 운영 실행 버튼은 없다.

## 사람 검토 요청

- 합성 결과와 실제 운영 결과의 구분이 첫 화면에서 충분히 분명한지
- train 기준선과 untouched test 지표의 차이를 비기술 독자가 이해할 수 있는지
- 판단 유보 사유와 고정 점검 일정 fallback이 과도한 안전 약속 없이 명확한지

## 한계

현재 수치와 화면은 deterministic synthetic snapshot이다. 실제 기관·이용자 테스트, 실제 수리·주행 데이터, field metric, 접근성 보조기기 검증, Firebase·production 배포가 아니다. 회의 일시·참석자·사진·지출은 사람 확인 전 기록하지 않는다.

근거: [ADR-0058](../../decisions/ADR-0058-r10-synthetic-reliability-presentation.md), [UPD-20260813-28](../../product-updates/UPD-20260813-28-r10-synthetic-reliability-presentation.md), [EVD-20260813-024](../../evidence/2026-08-product.md#evd-20260813-024--r10-합성-reliability-비교-presentation)
