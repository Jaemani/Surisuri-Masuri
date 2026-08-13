# UPD-20260813-30 — calibration readiness presentation

- 기준일: 2026-08-13
- 상태: implemented-local / synthetic-only
- version_or_deployment: `r11-calibration-estimability.v1` / production 배포 없음
- 대상 사용자: 기술 검토자·복지관 콘솔 데모 검토자
- 로드맵 위치: 9월 R11 calibration·abstention gate

## 변경 전 문제

R10 화면은 합성 기준선의 test metric을 비교했지만, 그 점수가 보정 가능한지와 데이터 부족 시 어떤 숫자를 숨겨야 하는지 별도 gate가 없었다.

## 제품 변화

보고서에 R11 readiness section을 추가했다. 부품별 validation 표본·사건·점수 다양성을 evaluator가 검사하고, 현재 세 부품 모두 `보정 불가`로 표시한다. calibration metric은 생성하지 않으며 고정주기와 사람 검토 fallback을 명시한다. 콘솔은 evaluator의 valid contract fixture와 byte-equivalent인 JSON을 직접 읽는다.

## 검증·배포 경계

- strict JSON Schema valid/invalid fixture
- Python deterministic evaluator·self-hash·dataset/result lineage
- test label이 eligibility를 바꾸지 않는 regression
- console JSON과 evaluator 산출물 exact equality
- Playwright synthetic console 6 flows와 시각 snapshot

실제 현장 calibration, 기기별 위험도, 접근성 사용자 검증, production 배포는 아니다. 롤백은 R11 보고서 section과 fixture import를 제거하는 source rollback이며 durable data migration은 없다.

관련: [ADR-0060](../decisions/ADR-0060-r11-calibration-estimability-gate.md), [EVD-20260813-026](../evidence/2026-08-product.md#evd-20260813-026--r11-calibration-estimability와-판단-유보), [HR-20260813-23](../reports/human/HR-20260813-23-calibration-readiness-review.md)
