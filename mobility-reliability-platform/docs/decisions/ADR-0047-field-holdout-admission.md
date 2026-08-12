# ADR-0047 — field holdout은 학습·배포와 분리된 평가 전용 계약으로 입장시킨다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: R08 field data, R07 후보 재평가, 동의·보존·모델 배포 경계

## 맥락

R07은 합성 데이터에서 규칙 기준선이 test macro-F1 1.0이어서 PyTorch 후보의 현장 효용을 판단할 수 없다. 기존 `quality-dataset-manifest.v1`는 `field_pilot`을 benchmark 불가로 닫으며, 이를 느슨하게 바꾸면 합성 학습과 현장 평가가 섞인다.

## 결정

- 기존 synthetic manifest와 training loader를 변경하지 않는다.
- field metadata는 `quality-field-holdout.v1` 별도 계약으로 받는다.
- 계약은 `evaluationOnly=true`, `trainingEligible=false`, `deploymentEligible=false`, `rawCoordinatesIncluded=false`를 고정한다.
- consent proof는 실제 revision·UID·Storage path가 아닌 server-only 원장과 대조할 digest만 담는다.
- 참가자·기기 identifier 대신 holdout namespace의 가명 group ID만 허용한다.
- frozen training 이후에 수집되고 label freeze와 보존 기한이 시간 순서를 만족하는 manifest만 입장시킨다.
- known label만 metric evaluation에 들어가며 review/abstain trace는 보존하되 평가 대상에서 제외한다.
- field holdout 입장은 ONNX·양자화·모바일 배포 허가가 아니다. 별도 evaluation result와 효용 판단이 필요하다.

## 결과

실제 동의 데이터가 없더라도 fixture로 계약과 fail-closed 경계를 검증할 수 있다. 하지만 계약 통과는 동의 문구의 적법성, 실제 consent state, 원본 artifact, 현장 성능을 증명하지 않으며 server admission 단계의 대조가 추가로 필요하다.
