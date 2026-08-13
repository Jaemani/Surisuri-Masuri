---
id: UPD-20260813-26
date: 2026-08-13
status: implemented-local-synthetic
version_or_deployment: r10-synthetic-reliability-baseline-v1
roadmap_month: 2026-09 R10
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: R10 합성 신뢰성 기준선

## 요약

부품별 점검 필요도 실험의 데이터·분할·유보 경계를 실행 가능한 코드로 만들었다. 개인정보 없는 합성 episode에서 고정주기, 누적거리, Kaplan–Meier 기준선을 동일 test cohort로 평가하며, 표본이 부족하면 숫자를 만들지 않는다.

## 변경 전 문제

- R10 결과 계약은 있었지만 outcome/censoring episode와 group/time leakage 검증 runtime이 없었다.
- JSON Schema만으로 count 산술, 시간창 비중복, method 간 동일 cohort를 보장할 수 없었다.

## 변경 후 동작

- seed `20260813`으로 split별 17개, 총 51개 deterministic synthetic episode와 canonical dataset hash를 생성한다.
- 명시적 부품 교체만 risk clock을 reset한다.
- 기기 group 중복과 future-label leakage를 거부한다.
- train evidence로 component sufficiency를 고정한 뒤 untouched test에서 세 기준선을 비교한다. validation은 tuning에 사용하지 않는다.
- 전체·부품·confusion count, 시간창, cohort를 schema 이후에도 재검증한다.
- 결과에는 원 record와 식별자를 넣지 않고 배포는 `defer`로 고정한다.

## 검증

| 완료 조건 | 결과 |
| --- | --- |
| 신규 reliability test | 14 passed |
| 전체 ML 회귀 | 87 passed / local CPU |
| Ruff format·lint | pass |
| 실제 field·배포 | 수행하지 않음 |

## 알려진 제한과 후속 작업

- 합성 episode만 사용했으므로 표시된 metric은 현장 성능이 아니다.
- 실제 데이터에는 outcome taxonomy, component linkage, 누락 거리, 관측 종료 사유를 별도 manifest로 확정해야 한다.
- 실제 event 수가 부족하면 Cox/Weibull/tree 후보를 만들지 않고 규칙 기반 점검과 `data_insufficient`를 유지한다.

관련: [ADR-0056](../decisions/ADR-0056-r10-synthetic-reliability-baseline.md), [EVD-20260813-022](../evidence/2026-08-product.md#evd-20260813-022--r10-local-synthetic-reliability-baseline), [HR-20260813-19](../reports/human/HR-20260813-19-r10-synthetic-reliability-baseline.md), [R10](../reports/fixed/2026-09-30.md)
