# Field holdout admission protocol

## 현재 상태

- 코드 상태: 계약·coordinate-free manifest validator 구현
- 검증 데이터: 무작위 UUID와 반복 hash로 만든 contract fixture only
- 실제 field data: 없음
- ONNX·모바일 inference: 유보

## 입장 흐름

```text
실기기 trace + 유효한 동의 + server-only artifact lineage
→ 원본은 보호된 저장소에서 별도 검증
→ 좌표 없는 feature와 holdout manifest 생성
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

## 현재 미구현

- 실제 consent ledger 대조 adapter
- 보호된 원본 artifact resolver와 삭제 workflow
- 현장 label guide·복수 검토자 disagreement 처리
- field evaluation result contract와 confidence interval
- coordinate-free field feature contract와 frozen inference artifact
- 작은 참가자 수에서 cohort 단위로 과장을 막는 metric

관련 결정: [ADR-0047](../decisions/ADR-0047-field-holdout-admission.md)
