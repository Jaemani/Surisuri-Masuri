# Field holdout admission protocol

## 현재 상태

- 코드 상태: coordinate-free manifest·field feature 계약과 validator 구현
- 검증 데이터: 무작위 UUID와 반복 hash로 만든 contract fixture only
- 실제 field data: 없음
- ONNX·모바일 inference: 유보

## 입장 흐름

```text
실기기 trace + 유효한 동의 + server-only artifact lineage
→ 원본은 보호된 저장소에서 별도 검증
→ 좌표 없는 holdout manifest 입장 검증
→ exact trace·batch·hash 연결 뒤 좌표 없는 field feature 생성
→ schema / 시간 / label / 가명 group / 보존 검증
→ frozen rules·PyTorch 후보의 평가 전용 입력
→ 별도 결과 계약·사람 판단
```

ML 서비스에 전달되는 manifest에는 raw 좌표, 주소, 경로, 이름·전화번호, Firebase UID, tenant/person/device ID, QR 공개코드, Storage object path가 없다. `pseudonymousGroupId`와 proof digest는 dataset namespace 밖에서 재사용하지 않으며 key와 역참조 원장은 server-only로 유지한다.

## 통과 조건

1. 기관과 사용 목적에 맞는 동의 정책·철회·삭제 절차가 실제로 승인돼 있다.
2. `field_pilot`, 평가 전용, 학습·배포 불가가 고정돼 있다.
3. field collection이 frozen training 종료 뒤에 시작한다.
4. capture, label finalize, holdout freeze, evaluation 시작·만료가 올바른 시간 순서다.
5. trace·batch identity가 중복되지 않고 모든 trace가 collection window 안에 있다.
6. known label만 `labelEligible=true`이며 review/abstain은 false다. 실제 metric 편입에는 별도 field feature 입장도 통과해야 한다.
7. 실제 consent state와 artifact lineage는 server admission에서 digest와 대조한다.

## Feature bridge 경계

`quality-field-features.v1`은 field manifest와 별도 산출물이다. holdout 입장 시점에는 아직 feature hash가 없으므로 manifest가 이를 미리 주장하지 않는다. extractor는 보호된 원본 batch를 일시적으로 읽지만 출력에는 raw sample·좌표·label·split·가명 group·동의 digest·Firebase 식별자·Storage path를 포함할 수 없다. `featureSha256`은 추출 후 canonical JSON으로 계산한다.

테스트가 사용하는 생성 batch는 계약과 연결 실패를 검증하기 위한 개발 입력일 뿐 field data 또는 현장 성능 증거가 아니다.

## 현재 미구현

- 실제 consent ledger 대조 adapter
- 보호된 원본 artifact resolver와 삭제 workflow
- 현장 label guide·복수 검토자 disagreement 처리
- field evaluation result contract와 confidence interval
- frozen rules·PyTorch inference artifact와 load-only predictor
- 작은 참가자 수에서 cohort 단위로 과장을 막는 metric

관련 결정: [ADR-0047](../decisions/ADR-0047-field-holdout-admission.md)
