# ADR-0048 — field 평가는 고정된 load-only inference artifact만 사용한다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: R07 PyTorch 후보, R08 field 평가 준비, ONNX·모바일 배포 gate

## 맥락

R07 PyTorch 후보는 합성 train split에서 학습되지만 기존 결과에는 state hash만 있고 실제 가중치, train-only mean/std, feature와 class 순서를 다시 불러올 artifact가 없었다. 이 상태에서 field trace를 평가하면 평가 시점에 재학습하거나 서로 다른 정규화를 사용할 위험이 있다.

## 결정

- `quality-model-artifact.v1` metadata와 `weights.pt`를 한 디렉터리에 동결한다.
- metadata는 synthetic training dataset·manifest hash, extractor와 feature 순서, class 순서, train-only mean/std와 hash, architecture, parameter count, state·weights·artifact hash를 포함한다.
- load는 CPU, `weights_only=true`, strict state key·shape·dtype·finite 검증으로만 수행한다.
- load 뒤 모든 parameter의 gradient를 끄고 optimizer, refit, fine-tuning, calibration 변경을 허용하지 않는다.
- synthetic `quality-features.v1`와 field `quality-field-features.v1`는 각자의 schema·hash를 다시 통과한 뒤 동일한 frozen predictor에 들어간다.
- review-required feature는 model forward를 실행하지 않고 `unknown_review_required`로 abstain한다.
- artifact는 `trainingSourceKind=synthetic`, `deploymentDecision=defer`를 고정한다.
- 실제 field holdout의 `trainingBoundary.frozenModelStateSha256`는 load한 artifact state hash와 정확히 일치해야 한다. 이 대조는 후속 field evaluation result 경계에서 수행한다.

## 결과

평가 시점의 학습 누수와 normalization drift를 막고 같은 후보를 반복 로드할 수 있다. 그러나 이 artifact는 실제 field 성능, 동의 적법성, ONNX 변환, Android/iPhone 추론, 모델 배포를 증명하거나 승인하지 않는다. ONNX 재개에는 실제 적격 field evaluation, rules와 동일 cohort 비교, 별도 의사결정이 필요하다.

관련 결정: [ADR-0046](./ADR-0046-defer-r07-onnx-after-synthetic-candidate.md), [ADR-0047](./ADR-0047-field-holdout-admission.md)
