# ADR-0060 — R11 calibration estimability gate

- 상태: accepted
- 결정일: 2026-08-13
- 범위: local synthetic reliability evaluation

## 결정

R11은 현재 점수를 보정했다고 주장하지 않는다. 부품별 validation 표본 30건, 관측 사건 10건, 서로 다른 점수 3개를 모두 충족한 경우에만 calibration metric을 만들 수 있다. 하나라도 부족하면 `not_estimable`과 단일 사유를 기록하고 curve·확률·Brier를 내보내지 않는다.

현재 Kaplan–Meier 기준선은 부품 strata 안에서 동일한 aggregate 점수이며 validation은 배터리 8건, 브레이크 6건, 컨트롤러 3건뿐이다. 따라서 세 부품 모두 판단을 유보한다. fallback은 `fixed_interval_and_human_review`, 개별 행동과 배포 승인은 항상 false다.

## 데이터·사실 경계

- train은 위험 기준선을 고정하고 validation은 보정 가능성만 판단한다. test는 untouched 평가 전용이며 tuning에 사용하지 않는다.
- reset 근거는 명시적 `verified_synthetic` replacement fact만 센다. 자유문, category 추정, `center_verified`, 지원금 execution을 기계적 고장 outcome으로 사용하지 않는다.
- 실제 completed repair archive에는 component instance linkage가 없으므로 field calibration 입장을 허용하지 않는다.
- artifact는 aggregate count와 reason만 포함하고 사람·기관·기기 ID, raw GPS, 수리 자유문을 포함하지 않는다.

## 결과

작은 표본에서 그럴듯한 calibration curve를 만들 위험을 차단한다. 실제 보정으로 진입하려면 decision-time feature 기반 개별 risk score, 명시적 component linkage, 더 큰 validation cohort가 먼저 필요하다.

근거: [EVD-20260813-026](../evidence/2026-08-product.md#evd-20260813-026--r11-calibration-estimability와-판단-유보), [UPD-20260813-30](../product-updates/UPD-20260813-30-calibration-readiness-presentation.md)
