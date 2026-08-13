# UPD-20260813-28 — R10 합성 reliability 비교 보고서

- 기준일: 2026-08-13
- 상태: implemented / local synthetic web visual 검증
- 로드맵 위치: R10 Device Timeline & Reliability

## 제품 변화

복지관 콘솔 보고서 화면에 세 reliability 기준선의 비교를 추가했다. train에서 정한 규칙·곡선과 untouched synthetic test 지표를 별도 패널로 구분하고, 배터리 aggregate 비교와 표본 부족 controller의 판단 유보를 표시한다.

표시값은 Python evaluator가 만든 `reliability-comparison-artifact.v1` snapshot을 직접 읽는다. 화면에는 합성 데이터 전용, 배포 보류, 개별 이용자·기기 판단 금지 경계를 고정하고 새 보고서 생성·내보내기 같은 운영 action을 두지 않았다.

## 검증

- contracts 전체 38 fixture cases 통과
- ML presentation 신규 9 tests 포함 전체 suite 통과
- console typecheck, 16 tests, production web build 통과
- Playwright console 5 flows와 신규 1440×1024 snapshot 통과·시각 검토

이는 local synthetic aggregate presentation이다. 실제 field 성능·calibration, 개별 기기 추론, 기관 이해도, Firebase 또는 production 배포를 증명하지 않는다.

관련: [ADR-0058](../decisions/ADR-0058-r10-synthetic-reliability-presentation.md), [EVD-20260813-024](../evidence/2026-08-product.md#evd-20260813-024--r10-합성-reliability-비교-presentation), [HR-20260813-21](../reports/human/HR-20260813-21-r10-synthetic-reliability-presentation.md), [R10](../reports/fixed/2026-09-30.md)
