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
