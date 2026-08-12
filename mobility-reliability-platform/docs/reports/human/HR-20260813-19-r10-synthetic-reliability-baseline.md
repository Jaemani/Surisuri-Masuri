# HR-20260813-19 — 점검 필요도 기준선 기술 검토

- 발행일: 2026-08-13
- 상태: generated / 사람 검토 대기
- 대상 독자: 프로젝트 운영자·기술 검토자
- 로드맵 게이트: R10 Device Timeline & Reliability

## 사람 대상 리포트 필요성 판단

필요하다. 이번 결과는 사용자에게 보여줄 고장 예측 기능이 아니라 실제 데이터를 안전하게 평가하기 전의 합성 검증 도구다. 운영자가 합성 metric을 실제 복지관 성과로 오인하지 않도록 범위와 다음 승인 조건을 분리한다.

## 현재 실제 상태

local CPU 환경에서 개인정보 없는 합성 부품 episode와 세 기준선 평가가 재현된다. 기기 group/time leakage, risk clock reset, censoring, 데이터 부족 유보, count reconciliation을 테스트한다. controller 합성 strata처럼 train evidence가 기준 미만이면 metric 없이 판단을 유보한다.

## 운영자에게 필요한 결정

- 실제 수리 데이터를 고장, 예방교체, 단순수리, 상담으로 구분할 수 있는지 확인한다.
- 부품 교체와 누적거리의 명시적 linkage가 있는 항목만 component lifecycle에 포함한다.
- 실제 cohort가 최소 표본·event 기준을 충족하지 않으면 모델 비교를 보류한다.
- 사용자 문구는 고장 예언이 아니라 “점검 권장 근거”로 제한한다.

## 현재 증명하지 않는 것

실제 사용자·복지관·수리사 데이터, 실제 위험곡선·성능·절감효과, 모델 학습, 모바일 추론, Firebase/production 배포는 확인하지 않았다. 실제 회의 일시·참석자·사진·지출도 이 문서로 생성하거나 증명하지 않는다.

근거: [ADR-0056](../../decisions/ADR-0056-r10-synthetic-reliability-baseline.md), [UPD-20260813-26](../../product-updates/UPD-20260813-26-r10-synthetic-reliability-baseline.md), [EVD-20260813-022](../../evidence/2026-08-product.md#evd-20260813-022--r10-local-synthetic-reliability-baseline)

