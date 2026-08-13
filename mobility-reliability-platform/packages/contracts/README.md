# Shared contracts

모바일, 게이트웨이, 데이터 처리 작업이 같은 의미를 공유하도록 버전이 고정된 wire contract를 보관합니다.

## 규칙

- 배포된 schema는 같은 버전 안에서 의미를 바꾸지 않습니다.
- 필드 제거·타입 변경·enum 축소는 새 major contract를 만듭니다.
- database row나 ORM model을 wire contract로 직접 노출하지 않습니다.
- tenant는 payload가 아니라 인증 membership으로 검증합니다.
- 날짜는 UTC ISO 8601, 거리는 meter, 속도는 meter/second를 사용합니다.
- 위치 sample에는 이름, 전화번호, 장애정보를 포함하지 않습니다.

## 초기 계약

- `telemetry-batch.v2.schema.json`: 신규 모바일에서 게이트웨이로 보내는 GPS batch. 실제 동의 revision과 installation·trip을 참조하며 현재 ingest 대상이다.
- `telemetry-batch.v1.schema.json`: 초기 설계 compatibility 기록. 사용자별 동의 revision을 식별하지 못하므로 production ingest 대상이 아니다.
- `domain-event.v1.schema.json`: 검증 이후 내부 event log에 기록하는 공통 envelope
- `quality-label.v1.schema.json`: 텔레메트리 trace의 이동 유형·품질 검토 라벨. `unknown_review_required`는 학습 class가 아니라 `review_required`/`abstained` 상태로 표현한다.
- `quality-dataset-manifest.v1.schema.json`: 합성·개발기기·현장 trace의 계보, hash, seed, group/time split 및 benchmark eligibility를 기록하는 ML dataset manifest. `developer_device` trace는 기록할 수 있지만 benchmark loader에서 제외한다.
- `quality-field-holdout.v1.schema.json`: 동의 기반 field trace의 coordinate-free 평가 입장 manifest. 학습·배포 불가와 좌표 미포함을 고정하며 실제 consent·artifact 대조는 server-only admission 책임으로 남긴다.
- `quality-field-features.v1.schema.json`: 입장된 field trace에서 생성한 평가 전용 numeric feature. holdout manifest·trace·telemetry hash를 정확히 연결하며 좌표, label, split, 가명 group, consent digest를 허용하지 않는다. 단독 컴파일 가능한 계약이며 공통 extractor 숫자 필드가 `quality-features.v1`과 달라지면 Python 회귀 테스트가 실패한다.
- `quality-model-artifact.v1.schema.json`: synthetic train split에서 한 번 학습한 PyTorch 후보의 load-only 평가 metadata. feature/class 순서, train-only normalization, architecture와 state·weights hash를 고정하며 배포 결정은 항상 `defer`다. 실제 weights는 trusted `weights.pt`로 분리하고 loader가 두 파일을 함께 검증한다.
- `quality-field-evaluation-result.v1.schema.json`: 동일한 적격 field feature cohort에서 frozen rules와 frozen PyTorch 후보를 비교한 평가 전용 결과. cohort reconciliation, 두 predictor의 count, abstain, trace별 label/prediction을 기록하되 가명 group·동의 digest·feature 값·원본 위치를 허용하지 않고 학습·배포는 false/defer로 고정한다.
- `quality-features.v1.schema.json`: 하나의 trace에서 추출한 coordinate-free numeric feature와 trace/batch/dataset/feature hash lineage. strict named fields만 허용하며 raw latitude/longitude, PII, label, prediction은 계약상 허용하지 않는다. 추출 실패는 value-free `reasonCode`와 `review_required` 상태로 표현한다.
- `quality-baseline-result.v1.schema.json`: `quality-features.v1`와 분리된 synthetic-only rules baseline 결과. split별/전체 metric, 네 known class와 `unknown_review_required` confusion matrix, prediction·abstain·feature hash를 strict하게 기록한다.
- `reliability-baseline-result.v1.schema.json`: R10 time-to-inspection baseline 결과의 synthetic-only contract. `device-group-time-holdout.v1` 시간창·leakage flag·counts reconciliation, fixed interval·cumulative distance·Kaplan–Meier 방법과 component별 `data_insufficient` abstention을 기록하며 `deploymentAuthorized=false`와 `deploymentDecision=defer`를 고정한다. 실제 event/censoring 데이터·성능·field/production 배포를 의미하지 않는다.
- `reliability-comparison-artifact.v1.schema.json`: R10 baseline 결과에서 파생한 aggregate-only presentation wire contract. train curve와 test metric 출처, identity-free component 비교, read-only internal synthetic demo와 deployment defer를 고정한다. 산술 합계·component 중복·metric lineage는 Python semantic validator가 추가 검증한다.
- `reliability-calibration-assessment.v1.schema.json`: R11 calibration estimability·abstention 평가 계약. validation 표본·사건·distinct score 자격을 먼저 검사하고, 부족하면 calibration metric을 금지한 채 고정주기·사람 검토 fallback으로 닫는다. 합성 aggregate-only, explicit verified-synthetic reset fact, untouched test, no individual action과 deployment defer를 고정한다.

R07-B1에서는 규칙 baseline 결과를 이 feature contract에 넣지 않는다. baseline output이
추가될 때는 별도 versioned result schema와 lineage/evaluation contract로 분리한다.

R07 합성 데이터셋은 각 trace를 `telemetry-batch.v2`로 별도 검증하고, manifest의 `telemetryBatchId`와 `telemetrySha256`로 연결한다. manifest schema는 split 간 `scenarioGroupId` leakage나 집계값 일치를 대신 검증하지 않으며, 이는 dataset builder의 결정론적 검증 단계에서 실패시켜야 한다.
