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
