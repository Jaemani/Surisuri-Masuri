# ML R07 실행·인계 Runbook

이 문서는 2026년 8월 R07-A/R07-B 합성 데이터 계약·feature·rules baseline을 WSL에서 재현하고, 다른 환경에서
동일한 경계로 이어받기 위한 절차다. 이 문서의 명령은 local 또는 clean CI에서
실행하는 검증 절차이며, 실행하지 않은 결과를 성능·현장 성과로 보고하지 않는다.

## 1. 현재 범위와 사실 경계

R07-A의 결과는 다음이다.

- `telemetry-batch.v2`와 quality label/manifest JSON Schema의 연결
- 네 known label과 review/abstain 상태의 분리
- `(0, 0)` 주변 synthetic GPS trace generator
- deterministic canonical JSON dataset hash
- scenario group 및 sample 전체 시간 기반 holdout 검증
- `developer_device` benchmark 혼입 차단
- contract 또는 validator 부재 시 fail-closed 처리

R07-B의 현재 결과는 다음이다.

- `quality-features.v1`의 coordinate-free numeric feature와 nested trace/batch/dataset/feature lineage
- malformed sample·부족한 accuracy·비합성 source를 `review_required`로 닫는 extractor
- 동일 frozen manifest에서 동작하는 `r07-rules-baseline.v1`
- `quality-baseline-result.v1`의 synthetic-only provenance, split metric, confusion matrix와 prediction feature hash
- feature record와 baseline 결과를 Python에서 repository-owned JSON Schema로 재검증하는 fail-closed gate

아직 R07-A의 결과가 아닌 것은 다음이다.

- PyTorch 학습, 모델 성능, calibration 또는 confusion matrix
- 실제 Android/iPhone 위치 수집 성능과 배터리 측정
- 복지관 pilot, 실제 사용자 데이터, legacy 수리데이터 이관
- ONNX/mobile inference, Firebase/production 배포

R07-A 코드 기준점은 commit `a20a85b`이며 R07-B 코드 기준점은 commit `a9b20d9`다. 전체 인계는 이 Runbook을 포함한 최신
`main`의 clean commit을 사용하고 `rtk git log -1 --oneline`으로 확인한다. 계획
월(M4/R07-A)과 실제 실행일·검증 결과를 섞지 않는다.

## 2. WSL clean handoff

저장소는 WSL Linux filesystem의 다음 경로를 기준으로 한다.

```text
/home/jaeman/Codes/Surisuri-Masuri/mobility-reliability-platform
```

WSL과 Windows clone을 하나의 worktree처럼 동시에 수정하지 않는다. 다른 환경으로
넘길 때는 commit을 push하고, 이어받는 환경에서는 clean pull/clone 뒤 아래를 먼저
확인한다.

```bash
rtk pwd
rtk git status --short --branch
rtk git log -1 --oneline
rtk git show --stat --oneline a20a85b
rtk python3 --version
rtk uv --version
```

기대 환경은 Python `3.12.x`, uv `0.8.13`이다. `uv`가 없으면 WSL에 공식 uv를
설치한 후 다시 확인한다. 저장소에 `.venv`, `__pycache__`, `.ruff_cache`,
`services/ml/artifacts/r07/`가 보이는 것은 generated/local state일 수 있으므로
먼저 `git status`에서 추적 파일 변경과 구분한다. 실제 사용자 데이터나 raw GPS가
있는 상태에서 자동 삭제·uninstall을 수행하지 않는다.

## 3. Locked Python 환경

ML service 디렉터리에서 lock을 변경하지 않고 설치한다.

```bash
rtk uv --directory services/ml sync --locked --extra dev
```

`uv lock` 또는 lock을 갱신하는 명령은 의존성 변경을 검토하고 별도 작업 단위로
승인받은 경우에만 실행한다. `--locked` 실패는 임의로 제거하거나 재생성하지 말고
`pyproject.toml`과 `uv.lock`의 차이, Python minor version, uv version을 먼저
확인한다.

runtime의 핵심 검증기는 `jsonschema[format]`이다. 계약 파일을 읽지 못하거나
validator가 없으면 성공으로 처리하지 않고 명시적 unavailable 오류로 닫힌다.

## 4. 단계별 검증

### 4.1 Node 계약 fixture

프로젝트 루트(`mobility-reliability-platform`)에서 모든 계약 fixture를 먼저 검사한다.

```bash
rtk pnpm --filter @mobility-reliability/contracts test
```

이 단계는 `quality-label.v1`, `quality-dataset-manifest.v1`,
`telemetry-batch.v2`의 valid/invalid fixture를 Ajv로 확인한다. 이 단계의 통과가
Python dataset의 group leakage나 count 일치를 보장하는 것은 아니다.

### 4.2 Python format, lint, test

```bash
rtk uv --directory services/ml run --locked --extra dev ruff format --check src tests
rtk uv --directory services/ml run --locked --extra dev ruff check src tests
rtk uv --directory services/ml run --locked --extra dev pytest -q
```

테스트는 계약 validation, label/manifest linkage, deterministic hash, class coverage,
group leakage, sample timestamp holdout, timezone fail-closed, duplicate identity,
developer-device rejection을 확인한다. 이것은 모델 성능 테스트가 아니다.

### 4.3 두 번 생성해 재현성 확인

생성물은 repository 밖 임시 디렉터리에 둔다.

```bash
R07_TMP="$(rtk mktemp -d)"
rtk mkdir -p "$R07_TMP/first" "$R07_TMP/second"
rtk uv --directory services/ml run --locked --extra dev python -m mobility_ml.generate_r07_dataset \
  --output "$R07_TMP/first"
rtk uv --directory services/ml run --locked --extra dev python -m mobility_ml.generate_r07_dataset \
  --output "$R07_TMP/second"
rtk cmp "$R07_TMP/first/dataset.json" "$R07_TMP/second/dataset.json"
rtk cmp "$R07_TMP/first/manifest.json" "$R07_TMP/second/manifest.json"
rtk git status --short
```

두 `cmp`가 모두 성공해야 한다. manifest의 `datasetSha256`은 raw file whitespace가
아니라 canonical JSON으로 계산되지만, handoff gate에서는 파일 자체도 byte 동일해야
한다. `R07_TMP`는 종료 후 제거해도 되는 임시 디렉터리이며 repository artifact로
복사하지 않는다.

생성 결과의 기본 범위는 seed `20260811`, 네 known class, synthetic source,
`quality-dataset-manifest.v1`, `group-time-holdout.v1`이다. 좌표나 raw batch를
로그에 출력하지 말고 필요하면 manifest의 count/hash 같은 비민감 metadata만
확인한다.

## 5. 실패 해석과 안전한 로그

| 증상 | 해석과 조치 |
| --- | --- |
| `contract schema unavailable` | 현재 working directory, 저장소 checkout, `MOBILITY_CONTRACTS_ROOT`를 확인한다. 다른 임의 schema로 우회하지 않는다. |
| `validator unavailable` | `uv sync --locked --extra dev`와 Python 3.12를 확인한다. validator 없는 상태의 pass는 유효하지 않다. |
| `telemetry-batch.v2 invalid: <path>:<keyword>` | generator가 wire schema를 어겼거나 계약이 바뀐 것이다. 오류에 포함된 raw 값·좌표를 추가 출력하지 않는다. |
| `quality-label.v1 invalid` | known class와 review/abstain 상태를 섞었는지, label/trace/batch linkage가 맞는지 확인한다. |
| `unsafe group/time holdout: split_leakage` | 같은 `scenarioGroupId`가 두 split에 나타난다. split을 임의로 재배정하지 말고 generator 입력과 group 규칙을 확인한다. |
| `train_validation_time_leakage` 또는 `validation_test_time_leakage` | 모든 sample 시간이 시간 경계를 넘었다. top-level 날짜만 고쳐서 숨기지 않는다. |
| `benchmark_forbidden` | developer device·field·비합성 provenance가 benchmark에 들어갔다. 별도 lifecycle artifact로 격리한다. |
| `manifest ... mismatch` | dataset과 manifest가 다른 snapshot이다. manifest hash나 count를 수동 수정하지 말고 같은 입력으로 다시 생성해 원인을 비교한다. |
| 두 생성물 `cmp` 실패 | seed, Python/uv, generator 버전, JSON serialization 또는 작업 트리 변경을 확인한다. 성능 결과로 해석하지 않는다. |
| Ruff/pytest 실패 | 해당 commit의 코드·계약·lock 조합을 그대로 보존하고 실패 path만 기록한다. raw payload를 issue/log에 붙이지 않는다. |

validator 오류는 path, keyword, trace index와 같은 최소 metadata만 기록한다. 위도·
경도, raw JSON, Firebase UID, 이름·전화번호를 shell output, CI artifact, 일반
개발 로그에 넣지 않는다. 실제 사용자 데이터가 섞였다고 의심되면 실행을 중지하고
개발 실패 기록/인시던트 정책을 따른다.

## 6. Generated artifact와 Git 규칙

- `dataset.json`, `manifest.json`은 재현 가능한 generated artifact다.
- `services/ml/artifacts/r07/`와 local 임시 디렉터리는 Git에 커밋하지 않는다.
- 계약·generator·validator·테스트·문서만 review 대상이다.
- 생성물 hash를 사람에게 전달해야 할 때도 dataset 전체나 좌표를 보내지 않고
  snapshot ID, seed, schema version, SHA-256과 검증 명령만 전달한다.
- 작업 종료 전 `rtk git status --short --branch`로 의도하지 않은 artifact가
  남지 않았는지 확인한다.

## 7. R07-B feature와 rules baseline 재현

R07-A의 동일한 dataset/manifest를 사용해 feature와 rules 결과를 검증한다. 데이터셋을
다시 split하거나 feature 계산 함수에 label·split을 전달하지 않는다.

```bash
rtk uv --directory services/ml run --locked --extra dev pytest -q tests/test_features.py tests/test_rules_baseline.py
```

검증되는 안전 경계는 다음과 같다.

- feature output에 latitude/longitude·raw samples·label·prediction이 없다.
- feature 값 또는 nested lineage가 바뀌면 feature hash 검증이 실패한다.
- review 상태에는 numeric feature가 없고 단일 value-free reason code만 있다.
- baseline result는 `sourceKind=synthetic`, `benchmarkEligible=true`, frozen dataset hash,
  feature schema version, rule version, split strategy를 포함한다.
- schema 누락·feature record 변조·label leakage는 결과를 통과시키지 않는다.

2026-08-11 WSL 재현 결과는 Python 49 tests, contract 16 fixture cases이며, synthetic
48 trace를 train/validation/test 각 16건으로 평가했다. 각 split의 accuracy including
abstain과 macro-F1은 1.0, abstain rate는 0이다. 이 숫자는 synthetic rules baseline의
계약·평가 파이프라인만 증명하며 현장 성능 수치로 사용하지 않는다.

## 8. R07-C로 넘기는 입력

R07-C의 입력은 새로 resplit하거나 복사한 CSV가 아니다.

1. 같은 generator snapshot의 `dataset.json`
2. `quality-dataset-manifest.v1`와 `datasetSha256`
3. 각 trace의 valid `telemetry-batch.v2`와 `quality-label.v1`
4. `group-time-holdout.v1`의 train/validation/test assignment
5. feature contract의 현재 버전 `quality-features.v1`

R07-B에서 구현된 결과는 다음과 같다.

- GPS 원본 좌표가 아닌 허용된 속도·가속·정지·heading·accuracy·missingness 요약
  feature extractor
- 동일 입력에 대한 golden vector와 versioned feature output
- label 또는 test split 정보를 feature 계산에 누출하지 않는 규칙 baseline
- feature hash와 generator/manifest lineage 기록
- rules baseline prediction·metrics·abstain을 versioned result로 기록

R07-B가 dataset을 다시 섞거나 trace를 sample 단위로 임의 분할하면 이 ADR의
holdout 경계를 위반한다. feature extractor가 실패하거나 데이터가 부족하면
`unknown_review_required`/review 경계를 보존하며, R07-C 이전에 성능을 주장하지
않는다. R07-C는 그 이후 동일 split에서 직접 학습 후보와 평가 harness를 별도로
추가하는 작업이다.

## 9. 인계 체크리스트

- [ ] `a20a85b` 또는 그 후속 commit을 기준으로 clean checkout을 만들었다.
- [ ] Python 3.12.x와 uv 0.8.13을 확인했다.
- [ ] `uv sync --locked --extra dev`가 성공했다.
- [ ] Node contracts fixture test가 성공했다.
- [ ] Ruff format/check와 pytest가 성공했다.
- [ ] 두 번 생성한 dataset/manifest가 byte 동일하다.
- [ ] generated artifact와 raw/PII가 Git status에 없다.
- [ ] seed, schema version, split strategy, dataset SHA-256을 인계 메모에 남겼다.
- [ ] 실제 사용자·현장·모델 성능·배포 상태를 R07-A 완료로 표현하지 않았다.
- [ ] R07-B synthetic baseline 수치를 현장·실기기 성능으로 표현하지 않았다.

관련 결정: [ADR-0040](../decisions/ADR-0040-r07-quality-dataset-contract.md)
관련 결정: [ADR-0041](../decisions/ADR-0041-r07-feature-and-rules-contract.md)
