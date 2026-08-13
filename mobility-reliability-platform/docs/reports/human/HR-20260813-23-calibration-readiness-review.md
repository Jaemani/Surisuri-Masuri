# HR-20260813-23 — 보정 가능성 화면 사람 검토

- 대상 기간: 2026-08-13 local increment
- 작성자: Codex 구현 초안
- 검토자: 프로젝트 책임자 확인 대기
- 로드맵: 10월 R11 calibration·abstention gate
- 상태: generated / 사람 검토 대기

## 계획

모델 숫자를 보여주기 전에 그 숫자를 평가할 표본·사건·점수 다양성이 충분한지 확인하고, 부족하면 판단 유보와 fallback을 보여준다.

## 실제 구현

합성 validation에서 배터리 8건·브레이크 6건·컨트롤러 3건을 확인했다. 최소 30건을 충족하지 못해 모든 부품의 calibration metric과 curve를 숨겼다. 컨트롤러는 그보다 앞선 reliability train 근거도 부족하다. 화면은 `전체 판단 유보`, 사유, 고정주기+사람 검토를 표시한다.

## 사람 검토 요청

- `보정 불가`와 `전체 판단 유보`가 기술 실패가 아니라 안전한 데이터 경계로 이해되는지
- 표본·사건·점수 종류 세 조건이 비기술 독자에게 설명 가능한지
- 고정주기+사람 검토 fallback이 복지관 운영 언어에 맞는지

## 근거와 한계

근거는 [ADR-0060](../../decisions/ADR-0060-r11-calibration-estimability-gate.md), [UPD-20260813-30](../../product-updates/UPD-20260813-30-calibration-readiness-presentation.md), [EVD-20260813-026](../../evidence/2026-08-product.md#evd-20260813-026--r11-calibration-estimability와-판단-유보)이다. 모든 수치는 deterministic synthetic data다. 실제 기관·사용자·기기·수리 결과, 현장 calibration, 배포, 회의·참석자·사진·지출을 증명하지 않는다.
