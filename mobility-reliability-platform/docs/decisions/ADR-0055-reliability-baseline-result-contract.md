# ADR-0055 — 신뢰성 baseline 결과를 synthetic-only 계약으로 고정한다

- 상태: accepted / contract-only local fixture 검증
- 결정일: 2026-08-13
- 영향 범위: R10 신뢰성 baseline 결과 계약, time-to-inspection 평가 경계, 배포 차단
- 선행 결정: [ADR-0052](./ADR-0052-device-current-state-deterministic-replay.md), [ADR-0054](./ADR-0054-legacy-repair-device-state-event-dry-run.md)

## 맥락

R10은 수리·주행 이력으로 부품별 time-to-inspection을 평가할 수 있는지 검토하는 회차다. 실제 수리 export, 관측 기간, outcome과 censoring 데이터는 아직 이 저장소에 없으므로 평가 결과를 제품 기능이나 현장 성과로 표현할 수 없다. 먼저 결과의 계보·누출 방지·데이터 부족 유보·배포 차단을 기계적으로 확인할 수 있는 contract-only 경계를 고정한다.

## 결정

- 결과 wire contract는 `reliability-baseline-result.v1`로 고정한다.
- 현재 계약의 입력 범위는 `evaluationScope=synthetic_only`, `sourceKind=synthetic`, `trainingPerformed=false`, `datasetVersion=reliability-dataset.r10.synthetic.v1`이다.
- 결과는 `outcomeDefinitionVersion`, `splitStrategy=device-group-time-holdout.v1`, dataset hash, train/validation/test 시간창과 `groupLeakageDetected=false`를 함께 기록한다.
- 전체 집계는 device·observation·observed outcome·censored·abstained 수와 `countsReconciled=true`를 포함한다. 이 플래그는 runtime에서 산술·입력 total reconciliation을 수행한 결과여야 하며 schema만으로 산술식을 대신하지 않는다.
- 비교 방법은 `fixed_interval`, `cumulative_distance`, `kaplan_meier` 세 종류로 계약한다. 이 ADR은 Cox, Weibull, random survival forest 또는 실제 모델 결과를 승인하지 않는다.
- 각 component 결과는 `evaluated`와 `data_insufficient`를 구분한다. `data_insufficient`이면 `abstention=true`와 reason만 허용하고 due count, confusion, sensitivity, specificity, Brier score, survival probability를 함께 기록하지 않는다.
- 모든 결과는 `deploymentAuthorized=false`, `deploymentDecision=defer`로 닫힌다. 사용자에게 고장 시점을 확정하거나 안전을 보증하는 표현을 허용하지 않는다.
- 결과에는 raw GPS·좌표·이용자/기기 식별자·PII를 넣지 않는다. 명시적 component linkage가 없는 수리 항목으로 부품 상태나 사건을 추정하지 않는다.

## 대안과 기각 이유

1. **먼저 실제 지표를 생성하고 계약을 나중에 고정** — 실제 데이터와 합성 fixture를 혼합하고 근거 없는 성능 주장으로 이어질 수 있어 기각한다.
2. **모든 component에 숫자 결과를 채움** — 표본·사건 부족을 숨기므로 `data_insufficient` abstention을 유지한다.
3. **schema만으로 count와 시간창 정합성을 보장** — JSON Schema가 산술 합계나 시간창 비중복을 충분히 표현하지 못하므로 runtime builder/reconciliation 책임으로 남긴다.
4. **contract 추가를 ML·현장 배포 승인으로 해석** — synthetic-only 결과와 deployment defer를 분리해야 하므로 기각한다.

## 검증 계획

- valid/invalid fixture가 `reliability-baseline-result.v1`의 required field와 strict additional-property 경계를 통과·거부한다.
- evaluated component의 필수 metric과 data-insufficient component의 metric 부재·abstention을 각각 검증한다.
- field scope, training 수행, deployment authorization, raw coordinate가 포함된 fixture가 거부된다.
- 계약 테스트는 local synthetic/contract fixture 환경에서만 실행하며 실제 event/censoring 생성, 성능 측정, field 또는 production 배포를 포함하지 않는다.

## 구현 결과와 한계

schema와 valid/invalid fixture를 추가하고 contracts validator에 연결했다. 2026-08-13 현재 `rtk pnpm --filter @mobility-reliability/contracts test`는 전체 36개 fixture case를 통과하며, 이 중 신뢰성 baseline 신규 case는 valid 1개·invalid 1개다.

이는 결과 형식과 차단 조건의 local contract 검증일 뿐이다. 실제 수리·주행 export, event/censoring 산출, Kaplan–Meier 곡선, calibration·confidence interval, 부품별 성능, field/production 사용과 배포를 증명하지 않는다.

## 관련 기록

- 제품 업데이트: [UPD-20260813-25](../product-updates/UPD-20260813-25-reliability-baseline-result-contract.md)
- 증거: [EVD-20260813-021](../evidence/2026-08-product.md#evd-20260813-021--reliability-baseline-time-splitcensoringabstention-contract)
- 정기리포트: [R10](../reports/fixed/2026-09-30.md)
