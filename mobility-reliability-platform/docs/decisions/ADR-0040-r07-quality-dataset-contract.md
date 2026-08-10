# ADR-0040: R07 이동 데이터 품질 데이터셋 계약과 benchmark 경계

- 상태: accepted
- 결정일: 2026-08-11
- 로드맵 위치: M4/R07-A (8월 데이터 계약·합성 데이터셋 게이트)
- 구현 기준점: [`a20a85b`](https://github.com/Jaemani/Surisuri-Masuri/commit/a20a85b)
- 관련 결정: [ADR-0006](./ADR-0006-model-and-llm-responsibility.md),
  [ADR-0002](./ADR-0002-mobile-gps-sessions.md)

## 맥락

R07-A의 목적은 모델을 학습하거나 현장 성능을 주장하는 것이 아니다. 이후의
feature extractor와 규칙 baseline이 같은 입력·라벨·split을 사용하도록 데이터
계약과 재현성 경계를 먼저 고정하는 것이다. 모바일 GPS의 wire 입력은 이미
`telemetry-batch.v2`로 정해져 있지만, 이동 유형 품질 라벨과 dataset 계보가
고정되지 않으면 다음 문제가 생긴다.

- `unknown_review_required`를 학습 class처럼 다루어 label 의미가 흔들린다.
- 같은 scenario의 trace가 train과 test에 섞여 성능이 부풀려진다.
- 개발 기기 lifecycle 데이터를 합성 또는 field benchmark 결과처럼 보고할 수 있다.
- manifest의 hash와 실제 batch·label·split이 서로 다른데도 파이프라인이 진행될 수 있다.

이 결정은 2026-08-11 기준의 합성 데이터 계약을 다룬다. 실제 사용자, 복지관
pilot, 모델 배포를 이 결정의 완료 조건으로 보지 않는다.

## 결정

### 1. 라벨과 review 상태를 분리한다

R07-A의 known training class는 다음 네 개로 고정한다.

- `mobility_aid_likely`
- `vehicle_likely`
- `stationary`
- `gps_noise_or_insufficient`

`unknown_review_required`는 class가 아니다. 모호하거나 사람이 확인해야 하는
상태는 `quality-label.v1`의 `labelState`인 `review_required` 또는 `abstained`로
표현하고, known label과 같은 confusion matrix class로 집계하지 않는다. 합성
generator가 만드는 label은 `labelSource=synthetic_generator`이며 label·trace·
scenario group·telemetry batch ID를 서로 연결한다.

### 2. R07-A benchmark 입력은 합성 데이터만 허용한다

generator의 기본 seed는 `20260811`이고 실제 서울 경로가 아닌 `(0, 0)` 주변의
가상 좌표를 사용한다. 이름, 전화번호, Firebase UID와 같은 PII를 생성하거나
로그에 출력하지 않는다. 각 trace의 batch는 repository-owned
`telemetry-batch.v2.schema.json`으로 검증하고, 위치 sample의 source는
`phone_gps`로 고정한다.

`sourceKind=developer_device`는 Android/iPhone lifecycle, 권한, 배터리와 같은
실기기 검증에서만 사용할 수 있다. benchmark loader는 이를 거부하며, 합성
manifest의 trace는 `sourceKind=synthetic`과 `benchmarkEligible=true`를 모두
만족해야 한다. synthetic 결과를 field 성능, 사용자 행동, 배터리 성능 또는
고장 예측 정확도로 보고하지 않는다.

### 3. group/time holdout을 데이터 builder의 권위 있는 검증으로 둔다

split 전략은 `group-time-holdout.v1`이다.

- 하나의 `scenarioGroupId`는 train/validation/test 중 하나에만 속한다.
- split 시간은 trace의 top-level 날짜가 아니라 batch의 모든 sample
  `capturedAt`에서 계산한다.
- trace `capturedAt`은 첫 sample과 일치해야 하고 sample 시간은 순서가 맞아야 한다.
- `sentAt`은 마지막 sample보다 늦어야 한다.
- train의 마지막 sample보다 validation이 늦고, validation보다 test가 늦어야 한다.
- validation과 test에는 네 known class가 모두 있어야 한다.

manifest JSON Schema는 field shape를 검증하지만 group leakage와 집계값의 의미를
대신 검증하지 않는다. 따라서 이 조건은 Python `validate_group_time_holdout()`와
`validate_benchmark_dataset()`에서 benchmark load 전에 fail-closed로 검사한다.

### 4. manifest는 계약·계보·hash를 함께 고정한다

`quality-dataset-manifest.v1`에는 dataset ID/version, telemetry·label·feature·
generator version, seed, split strategy, source kind, trace/label/split count와
각 trace의 telemetry hash가 들어간다. dataset과 manifest를 연결할 때 다음을
검증한다.

- canonical JSON(`sort_keys`, 고정 separator, UTF-8)으로 계산한 dataset SHA-256
- trace·label·telemetry batch ID의 linkage와 중복 여부
- trace별 sample count·capturedAt·telemetry hash
- root/trace provenance와 `benchmarkEligible`
- label count와 split count

generator는 고정된 JSON 표현과 UUID namespace를 사용하므로 같은 seed와 같은
코드에서 dataset·manifest byte와 hash가 동일해야 한다. 생성물은 repository의
`services/ml/artifacts/r07/` 또는 별도 임시 디렉터리에만 만들며 Git에 커밋하지
않는다.

### 5. 계약 validator가 없거나 계약을 읽지 못하면 통과시키지 않는다

Python 서비스는 runtime dependency인 `jsonschema[format]`으로
`telemetry-batch.v2`, `quality-label.v1`, `quality-dataset-manifest.v1`을
검증한다. schema를 찾지 못하거나 JSON을 읽지 못하거나 `jsonschema`가 설치되지
않으면 `ContractValidationError` 또는 `DatasetValidationError`를 반환한다.
조용한 fallback pass는 허용하지 않는다. 오류 문자열에는 raw payload와 좌표 대신
검증 경로와 keyword만 남긴다.

Python 의존성은 `services/ml/uv.lock`에 고정하고 CI와 clean handoff는 Python
3.12 및 uv 0.8.13 기준 `uv sync --locked --extra dev`를 사용한다.

## 결과와 제한

이 결정으로 R07-B가 재사용할 수 있는 deterministic input, label contract,
manifest, group/time split과 provenance 경계가 생긴다. 합성 trace는 pipeline과
계약을 검증하기 위한 fixture이지 사용자 데이터의 대체물이 아니다.

다음은 이 ADR의 범위에 포함하지 않는다.

- R07-B의 versioned feature extractor와 golden vector
- R07-C의 PyTorch TinyTemporalCNN 또는 다른 직접 학습 모델
- confusion matrix, calibration, field/generalization 성능 주장
- Android/iPhone 실기기 수집, 복지관 pilot, 수리데이터 이관
- ONNX export, Firebase/production 배포, 운영 SLO
- LLM report agent와 사람 대상 정책 보고서

이 항목들은 후속 결정과 실제 검증 증거에서 별도로 기록한다.

## 검증 경계

- 계약 fixture는 `packages/contracts/scripts/validate-fixtures.mjs`에서 Node/Ajv로
  검증한다.
- ML service는 locked Python 환경에서 Ruff와 pytest를 실행한다.
- generator를 두 번 실행해 `dataset.json`과 `manifest.json`을 byte 단위로 비교한다.
- 실제 사용자·기관·staging·production 성과는 이 ADR의 증거가 아니다.

## 관련 기록

- 구현: `services/ml/src/mobility_ml/manifest.py`,
  `services/ml/src/mobility_ml/generate_r07_dataset.py`
- 계약: `packages/contracts/schemas/quality-label.v1.schema.json`,
  `packages/contracts/schemas/quality-dataset-manifest.v1.schema.json`,
  `packages/contracts/schemas/telemetry-batch.v2.schema.json`
- 인계 절차: [ML R07 Runbook](../development/ML_R07_RUNBOOK.md)
- 제품 업데이트: [UPD-20260811-02](../product-updates/UPD-20260811-02-r07-synthetic-dataset-contract.md)
- 증거: [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002--r07-합성-품질-데이터셋-계약과-결정론적-split)
- 사람 대상 리포트: [HR-20260811-02](../reports/human/HR-20260811-02-r07-dataset-foundation.md)
- 개발 실패 기록: [DEVFAIL-20260811-04](../development/DEVELOPMENT_FAILURE_LOG.md#devfail-20260811-04--trace-상단-시간만-사용한-temporal-split-누수),
  [DEVFAIL-20260811-05](../development/DEVELOPMENT_FAILURE_LOG.md#devfail-20260811-05--datasetmanifest-provenance와-malformed-input-경계-누락)
