# ADR-0056 — R10은 먼저 합성 episode와 세 가지 신뢰성 기준선을 재현한다

- 상태: accepted / local synthetic 구현 검증
- 결정일: 2026-08-13
- 영향 범위: R10 time-to-inspection dataset, 기준선 평가, 유보 정책
- 선행 결정: [ADR-0055](./ADR-0055-reliability-baseline-result-contract.md)

## 맥락

실제 수리 export에는 outcome, censoring, 부품 교체와 누적 주행량의 명시적 연결을 사람과 함께 확정해야 한다. 그 전에 실제 고장 예측이나 field metric을 만들면 레거시 분류를 사실처럼 해석할 위험이 있다. 따라서 첫 실행 가능한 R10 increment는 개인정보 없는 합성 episode로 split·risk clock·평가 산술을 검증한다.

## 결정

- R07 이동 품질 데이터셋과 분리된 `reliability-dataset.r10.synthetic.v1`을 사용한다.
- seed `20260813`에서 train/validation/test 각 17개, 총 51개 합성 episode를 메모리에서 결정적으로 생성한다.
- episode는 가명 기기 group, 명시적 component, risk start, decision time, 30일 관측 종료, 누적거리·과거 일평균거리 요약, 점검 필요 outcome 또는 censoring만 가진다.
- 거리 요약의 `asOf`는 decision time과 같아야 하며 NaN·Infinity를 거부한다. 거리 결측 record는 현재 dataset admission 이전 검토 대상이며 distance method에 0으로 대입하지 않는다.
- `component_replaced`가 명시되고 동일 가명 기기·부품·시각의 verified synthetic replacement event가 있는 경우에만 risk clock reset을 허용한다. 일반 수리나 component linkage가 없는 레거시 기록으로 reset을 추정하지 않는다.
- 같은 기기 group은 split을 넘지 못한다. 이전 split의 label availability가 다음 split의 decision time과 겹치면 future-label leakage로 거부한다.
- train split의 부품별 표본·event sufficiency를 동결하고 untouched test split에서 180일 고정주기, 30일 내 누적 1,000km 도달 규칙, 최소 Kaplan–Meier 기준선을 동일 cohort로 비교한다. validation은 tuning이나 metric 산출에 사용하지 않는다.
- 표본 또는 event가 기준 미만인 component는 metric을 만들지 않고 `data_insufficient`로 유보한다.
- 결과 runtime은 schema에 더해 전체/부품/confusion count 산술, 시간창 비중복, method 간 동일 cohort를 검증한다.
- top-level `abstained`는 세 method 중 하나라도 판단을 유보한 component에 속하는 unique test observation 수다. method별 유보 횟수의 합이 아니다.
- 결과는 local synthetic 평가이며 학습·배포를 수행하지 않는다. `deploymentAuthorized=false`, `deploymentDecision=defer`를 유지한다.

## 대안과 기각 이유

1. **실제 레거시 수리 category를 곧바로 고장 outcome으로 사용** — 예방교체·상담·단순수리를 구분하지 못하므로 기각한다.
2. **Cox/Weibull/tree부터 구현** — outcome 경계와 baseline 산술보다 모델 복잡도가 먼저 생기므로 보류한다.
3. **누적거리 현재값만으로 30일 위험을 표시** — 평가 horizon과 불일치하므로 과거 일평균거리로 30일 도달량을 투영한다.
4. **표본 부족 component에도 확률을 표시** — 숫자가 근거로 오인되므로 abstention한다.

## 검증

- 동일 seed는 같은 canonical dataset/hash와 평가 결과를 만든다.
- group overlap, 미래 label, 잘못된 risk reset과 chronology를 fail-closed한다.
- test label 변경이 train에서 산출한 Kaplan–Meier probability를 바꾸지 않는다.
- 전체·component·confusion count와 method cohort 변조를 semantic validator가 거부한다.
- synthetic 결과에는 episode/group ID, outcome time, raw GPS·좌표·기관/사람 ID가 포함되지 않는다.

## 구현 결과와 한계

합성 episode generator, device-group/time holdout validator, 세 기준선 evaluator와 결과 semantic validator를 구현했다. 기본 합성 데이터에서 controller train 표본은 3개로 최소 4개보다 작아 세 method 모두 판단을 유보한다. 신규 10개 테스트를 포함한 ML test 83개가 local CPU 환경에서 통과했다.

실제 수리·주행 export, field holdout, 실제 부품별 sample·metric, 모델 학습, confidence interval, 사용자 알림, Firebase/production 배포는 수행하지 않았다. 합성 수치는 제품 효과나 안전 성능으로 사용할 수 없다.
