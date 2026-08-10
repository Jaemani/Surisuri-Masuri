---
id: ADR-0041
status: accepted
date: 2026-08-11
roadmap_month: 2026-08 R07-B
implementation_commit: a9b20d9
---

# ADR-0041: R07 feature contract와 synthetic rules baseline 경계

## 맥락

R07-A에서 synthetic telemetry, label, manifest, split과 hash를 고정했다. 다음 단계는
같은 frozen input에서 feature를 만들고 baseline을 평가하는 것이다. 이 경계가 없으면
feature 계산에 label이 섞이거나 raw 좌표가 모델 입력·로그·보고서로 새어 나가고, 개발
기기 결과가 synthetic benchmark로 오인될 수 있다.

## 결정

1. feature 계산 함수는 telemetry batch 하나만 받는다. label·split·expected outcome은
   계산에 전달하지 않고, 계산 후 nested lineage로만 붙인다.
2. feature output은 `quality-features.v1`로 고정한다. named numeric fields만 허용하고
   latitude, longitude, raw sample, PII, label, prediction은 허용하지 않는다.
3. feature record는 trace, telemetry batch, dataset, feature extractor의 version과
   SHA-256을 포함한다. 값이나 lineage가 바뀌면 hash 검증이 실패한다.
4. malformed input과 부족한 데이터는 raw payload를 반환하지 않고 `review_required`와
   단일 value-free `reasonCode`로 닫는다. developer device와 비합성 source는
   benchmark eligible이 아니다.
5. rules baseline은 feature record만 읽고 네 known class와
   `unknown_review_required` abstain 상태를 사용한다. 결과는 feature contract와
   분리된 `quality-baseline-result.v1`로 기록하며 synthetic source/provenance와
   frozen dataset hash를 필수로 한다.
6. Python evaluator는 각 feature record와 최종 baseline result를 repository-owned
   JSON Schema로 검증한다. schema·validator·hash·lineage가 없으면 fail-closed한다.

## 대안과 선택 이유

| 대안 | 판단 |
| --- | --- |
| feature에 label·split을 함께 전달 | 계산 편의는 있지만 leakage를 구조적으로 막을 수 없어 거부 |
| raw GPS를 feature artifact에 보존 | 디버깅은 쉽지만 coordinate/PII 경계와 발표 자료 재사용 위험이 커 거부 |
| baseline 결과를 feature record에 포함 | 계약 의미가 섞이고 학습·평가 경계가 흐려져 거부 |
| 실제·개발기기 데이터를 synthetic benchmark에 포함 | 일반화 수치를 과장할 수 있어 source/eligibility로 분리 |
| schema 검증 없이 Python dict만 반환 | contract drift를 놓칠 수 있어 fail-closed validator를 선택 |

## 결과

commit `a9b20d9`에서 feature extractor, golden/malformed tests, rules baseline, baseline
result schema/fixtures와 Python validator를 연결했다. 동일 synthetic generator를
사용한 48 trace 평가에서 train/validation/test 각 16건, 각 split macro-F1 1.0,
abstain rate 0을 재현했다.

이 결과는 synthetic rules baseline의 결정론적 계약·평가를 증명할 뿐이며 실제 GPS,
Android/iPhone, 현장 사용자, 복지관, 수리데이터 일반화나 고장 예측 성능을 증명하지
않는다.

## 후속 작업

- R07-C에서 동일 split으로 PyTorch 후보를 비교한다.
- 모델 선택 후에만 ONNX export와 모바일 parity/지연시간을 측정한다.
- 실기기와 field source는 별도 `sourceKind`와 별도 증거로 관리한다.

## 관련 기록

- 선행 결정: [ADR-0040](./ADR-0040-r07-quality-dataset-contract.md)
- 제품 업데이트: [UPD-20260811-03](../product-updates/UPD-20260811-03-r07-feature-rules-baseline.md)
- 증거: [EVD-20260811-003](../evidence/2026-08.md#evd-20260811-003--r07-feature-contract와-synthetic-rules-baseline)
- 사람 대상 리포트: [HR-20260811-03](../reports/human/HR-20260811-03-r07-feature-rules-baseline.md)
