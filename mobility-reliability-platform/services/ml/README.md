# ML service

Python/PyTorch 기반 학습·평가 작업을 보관합니다. 현재 R07-A는 모델 학습이
아니라 **합성 데이터셋의 재현성과 학습 경계**를 고정하는 단계입니다.

모델 계열은 다음 두 가지로 분리합니다.

1. 모바일 센서·GPS 데이터 품질 및 이동 유형 보조 판별
2. 수리·점검·누적 사용량 기반 time-to-inspection 위험 추정

## R07 synthetic dataset

`src/mobility_ml/generate_r07_dataset.py`는 seed `20260811`을 기본값으로
사용해 다음 네 known class를 생성합니다.

- `mobility_aid_likely`
- `vehicle_likely`
- `stationary`
- `gps_noise_or_insufficient`

`unknown_review_required`는 학습 class가 아니라 abstain/review 상태이므로
합성 학습셋의 class로 넣지 않습니다. 각 trace는 기존
`telemetry-batch.v2` JSON Schema를 만족하며, 실제 위치가 아닌 `(0, 0)` 주변
가상 좌표만 사용합니다. 생성기는 이름·전화번호·Firebase UID 등의 PII를
만들지 않습니다.

```bash
rtk uv --directory services/ml sync --locked --extra dev
rtk uv --directory services/ml run --locked --extra dev python -m mobility_ml.generate_r07_dataset \
  --output artifacts/r07
rtk uv --directory services/ml run --locked --extra dev pytest
```

Python은 `3.12.x`, uv는 CI 기준 `0.8.13`을 사용한다. `uv.lock`을 바꾸지
않고 인계받은 환경에서 재현할 때는 항상 `--locked`를 붙인다. uv가 없는
WSL에서는 [공식 uv 설치 절차](https://docs.astral.sh/uv/getting-started/installation/)로
설치한 뒤 위 명령을 실행한다.

생성 결과는 `dataset.json`과 `manifest.json`입니다. manifest에는
`quality-dataset-manifest.v1`에 맞춰 generator/feature/telemetry 계약 버전,
seed, fixed `createdAt`, trace·label·split count, 각 telemetry hash가 들어갑니다.
`datasetSha256`은 공백·파일 순서에 영향을 받지 않는 canonical JSON hash입니다.
`artifacts/r07/`은 생성물이라 Git에 포함하지 않는다. CI도 runner 임시 폴더에
두 번 생성한 뒤 byte equality를 검사하고 폐기한다.

## R07-B feature contract와 rules baseline

R07-B에서는 R07-A의 동일한 frozen manifest와 split을 다시 사용해 좌표를 반환하지
않는 `quality-features.v1` feature record와 synthetic-only rules baseline을 연결한다.
feature 계산 함수의 입력은 telemetry batch 하나뿐이며 label·split은 계산 경계 밖에서
계보로만 붙인다. feature record에는 trace, telemetry batch, dataset, extractor의
SHA-256 lineage가 들어가고 원본 latitude/longitude·PII·label·prediction은 들어가지
않는다.

추출 실패는 raw payload를 출력하지 않고 `review_required`와 value-free
`reasonCode`로 닫는다. `developer_device`, `field_pilot`, `legacy_import`은
benchmark 결과가 아니므로 feature record에서 benchmark eligibility를 false로
표현한다.

rules baseline은 네 known class와 `unknown_review_required` abstain 상태를 사용한다.
결과는 `quality-baseline-result.v1`로 별도 검증하며 synthetic source와
`benchmarkEligible=true`를 반드시 포함한다. 2026-08-11 현재 재현 결과는 48 trace,
split별 16 trace, 전체 abstain 0, 전체 macro-F1 1.0이다. 이 수치는 생성 데이터의
규칙 기준선 재현성만 보여주며 실제 GPS, 실기기, 복지관, 수리데이터 또는 현장
일반화 성능을 의미하지 않는다.

```bash
rtk pnpm --filter @mobility-reliability/contracts test
rtk uv --directory services/ml run --locked --extra dev ruff format --check src tests
rtk uv --directory services/ml run --locked --extra dev ruff check src tests
rtk uv --directory services/ml run --locked --extra dev pytest -q
```

현재 Python test는 feature golden vector, malformed/coordinate-free output, feature
hash tamper, label leakage, frozen split evaluation, baseline result schema를 함께
검증한다.

## R07-C PyTorch 최소 후보

`torch_candidate.py`는 frozen manifest의 train 16 trace만 사용해 292 parameter
`TinyFeatureMLP`를 CPU에서 학습하고 validation/test 각 16 trace를 평가한다.
입력은 R07-B의 hash 검증된 coordinate-free feature 13개이며 label·split·좌표는
prediction tensor에 들어가지 않는다. seed, deterministic algorithm, CPU thread 수,
feature 순서, 모델 state SHA-256을 결과에 고정한다.

```bash
rtk uv --directory services/ml sync --locked --extra dev
rtk uv --directory services/ml run --locked --extra dev ruff format --check src tests
rtk uv --directory services/ml run --locked --extra dev ruff check src tests
rtk uv --directory services/ml run --locked --extra dev pytest -q
```

PyTorch는 `pytorch-cpu` explicit index에서 lock한다. CUDA wheel을 WSL 환경에
내려받지 않으며 `torch.cuda.is_available()`을 전제로 하지 않는다.

현재 synthetic generator는 규칙 분리가 명확해 rules test macro-F1이 이미 1.0이다.
따라서 후보 결과가 높아도 모델 우월성이나 field 일반화 증거가 아니다. 결과의
`deploymentDecision`은 항상 `defer`이며 실제 동의 trace와 고정 field evaluation이
생길 때까지 ONNX·모바일 추론 진입을 승인하지 않는다.

### Frozen load-only artifact

`export_frozen_artifact()`는 synthetic train split으로 학습한 가중치와 train-only mean/std를 `quality-model-artifact.v1` metadata와 `weights.pt`로 고정한다. `load_frozen_artifact()`는 CPU `weights_only` load, exact state key·shape·dtype, finite tensor, model/normalization/weights/artifact hash를 모두 확인하고 gradient를 끈다. `predict_frozen()`은 schema와 feature hash가 검증된 synthetic 또는 field feature만 받으며 review 상태는 model forward 없이 abstain한다.

artifact 생성은 학습 단계지만 그 이후 load/predict 경로에는 optimizer나 training API가 없다. 이 코드는 local synthetic 재현 증거이며 field 성능, ONNX 변환, 모바일 추론 또는 배포 승인이 아니다. 결정 경계는 [ADR-0048](../../docs/decisions/ADR-0048-frozen-field-inference-boundary.md)을 따른다.

## R08-A field holdout admission

`field_holdout.py`는 실제 동의 field data가 들어올 때 사용할 **평가 전용 manifest**를 검증한다. 입력에는 원본 좌표·경로·Firebase ID·Storage path가 없으며, 학습과 배포 eligibility는 false로 고정한다. frozen training 뒤 수집, label freeze, evaluation window, trace count·identity와 known/review 상태를 fail-closed로 확인한다.

`field_features.py`는 이 manifest에 정확히 연결된 `telemetry-batch.v2`만 공통 numeric extractor에 전달한다. 출력은 별도 `quality-field-features.v1` 계약을 사용하며 좌표·label·split·가명 group·consent digest를 복사하지 않는다. manifest, trace, batch와 canonical hash가 하나라도 다르면 추출을 거부하고, feature record 자체도 hash로 고정한다.

현재 테스트는 contract fixture와 field 계약 bridge 검증용 합성 batch만 사용하며 실제 field trace, 동의 state, 현장 성능을 증명하지 않는다. 합성 training loader도 바뀌지 않았고 field manifest는 `torch_candidate.py`의 학습 입력으로 사용할 수 없다. 세부 운영 경계는 [Field holdout protocol](../../docs/data/FIELD_HOLDOUT_PROTOCOL.md)을 따른다.

`field_evaluation.py`는 manifest의 평가 window와 frozen model/rules version을 확인한 뒤 label-ready이면서 feature-ready인 동일 trace set에 두 predictor를 실행한다. label review, feature review, missing feature를 별도 집계하고 total→label→feature→scored count를 재조정한다. 이 모듈은 optimizer나 fit API를 갖지 않으며 결과 계약은 항상 `trainingPerformed=false`, `deploymentAuthorized=false`, `deploymentDecision=defer`다.

현재 field evaluation 테스트 역시 contract bridge용 생성 batch로만 수행한다. 그 결과는 평가 코드의 경계 검증이지 실제 field metric이 아니다.

## R10 synthetic reliability baseline

`reliability_dataset.py`는 실제 수리 export가 준비되기 전에 time-to-inspection
평가 경계를 검증하기 위한 합성 episode를 생성한다. episode에는 명시적 부품 분류,
risk clock 시작 사유, decision time, 30일 관측 종료, 누적거리 요약과 점검 필요
outcome/censoring만 들어간다. raw GPS, Firebase·기관·사람 식별자와 수리 자유문은
생성하지 않는다.

`device-group-time-holdout.v1` 검증기는 같은 가명 기기 group이 split을 넘지 못하게
하고, 이전 split의 label availability가 다음 split의 decision time보다 늦으면
거부한다. 부품 교체가 명시된 episode만 risk clock reset event를 가질 수 있다.

`reliability_baseline.py`는 train split의 표본·사건 수로 부품별 평가 가능 여부를
동결하고 untouched test split에서 다음 세 기준선을 같은 cohort로 비교한다.
validation split은 tuning이나 metric에 사용하지 않는다.

- 180일 고정 점검주기
- 현재 누적거리와 과거 일평균 거리를 이용한 30일 내 1,000km 도달 규칙
- equal-horizon right censoring을 반영한 최소 Kaplan–Meier 기준선

표본이나 사건이 부족한 부품은 metric 없이 `data_insufficient`로 유보한다. 결과
runtime은 전체·부품·confusion count, 시간창 비중복, method별 동일 cohort를 schema
외 semantic invariant로 다시 확인한다. 결과는 언제나 `synthetic_only`,
`trainingPerformed=false`, `deploymentAuthorized=false`, `deploymentDecision=defer`다.
이는 실제 고장 예측, 현장 성능, 부품 안전 보증 또는 배포 증거가 아니다.
현재 기본 출력은 51개 합성 episode와 test 17개 observation이며, controller train
표본 3개는 최소 4개보다 작아 모든 method에서 metric 없이 abstain한다.

```bash
rtk uv --cache-dir /tmp/mobility-ml-uv-cache --directory services/ml run --locked --extra dev pytest -q
rtk uv --cache-dir /tmp/mobility-ml-uv-cache --directory services/ml run --locked --extra dev ruff check src tests
rtk uv --cache-dir /tmp/mobility-ml-uv-cache --directory services/ml run --locked --extra dev ruff format --check src tests
```

## Split and provenance rules

- `group-time-holdout.v1`: 같은 `scenarioGroupId`가 train/validation/test를
  넘나들면 benchmark loader가 거부합니다.
- 시간 순서는 train < validation < test이며 validation과 test 모두 네 known
  class를 가져야 합니다.
- `sourceKind=developer_device`는 실기기 lifecycle 테스트용일 뿐 학습·평가
  benchmark에 혼입할 수 없습니다. loader가 명시적으로 거부합니다.
- 로그와 validation 오류에는 좌표·원본 payload 값을 넣지 않습니다.
- 합성 결과는 계약·파이프라인·UI·초기 모델 코드 검증용이며 현장 성능이나
  실제 사용자 행동의 근거로 보고하지 않습니다.

## Responsibility boundary

LLM 보고서 생성은 이 디렉터리의 모델 책임이 아닙니다. 모델이 만드는 값은
검증된 feature와 평가 결과를 통해서만 전달하며, 모든 이후 모델 산출물은
dataset version, feature version, split strategy, metric, calibration,
abstention을 함께 기록해야 합니다.
