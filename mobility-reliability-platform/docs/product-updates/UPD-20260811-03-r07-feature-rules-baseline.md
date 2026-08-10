---
id: UPD-20260811-03
date: 2026-08-11
status: draft
version_or_deployment: r07-feature-rules-baseline-a9b20d9
roadmap_month: 2026-08 R07-B
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: R07 feature contract와 rules baseline

## 요약

R07-A의 frozen synthetic dataset을 재사용해 coordinate-free feature record와
synthetic-only rules baseline을 연결했다. feature 값과 lineage를 hash로 고정하고,
baseline 결과를 별도 versioned schema로 검증해 모델·현장 성능과 계약 검증을 분리했다.

## 변경 후 동작

- telemetry batch 하나만 입력받는 deterministic feature extractor가 허용된 numeric
  feature만 반환한다.
- raw latitude/longitude, raw samples, PII, label, prediction은 feature output에 없다.
- malformed input·부족한 accuracy·비합성 source는 `review_required`와 reason code로
  닫힌다.
- rules baseline은 feature hash와 frozen dataset lineage를 확인한 뒤 prediction한다.
- result에는 `sourceKind=synthetic`, `benchmarkEligible=true`, split metric,
  confusion matrix, prediction feature hash가 포함된다.

## 검증 결과

| 항목 | 결과 |
| --- | --- |
| Contract fixture | 16 cases pass |
| Python tests | 49 pass |
| Synthetic evaluation | 48 trace; train/validation/test 각각 16 |
| Split metric | 각 split macro-F1 1.0, abstain rate 0 |
| Dataset hash | `7e9242ad2c01d4abf94fd4a6c62153ef735b351f66481b4239f1c7bf3833b24c` |
| 기준 commit | [`a9b20d9`](https://github.com/Jaemani/Surisuri-Masuri/commit/a9b20d9) |

## 범위와 제한

- 환경: WSL2 local, Python 3.12 `.venv`, Node/Ajv contract fixtures
- 데이터: synthetic only; `(0, 0)` 주변 가상 trace
- 포함하지 않음: PyTorch 학습, ONNX/mobile inference, Android/iPhone field trace,
  수리데이터 이관, 복지관 pilot, Firebase/production 배포, 실제 성능·비용·정책 성과
- 위 metric은 생성 규칙과 동일한 synthetic distribution의 기준선이며 현장 일반화
  수치로 보고하지 않는다.

## 롤백

production이나 사용자 데이터에 배포하지 않았으므로 runtime rollback은 수행하지
않았다. 소스 롤백이 필요하면 기준 commit 이전의 clean commit으로 별도 검토한다.

## 관련 기록

- 결정: [ADR-0041](../decisions/ADR-0041-r07-feature-and-rules-contract.md)
- 증거: [EVD-20260811-003](../evidence/2026-08.md#evd-20260811-003--r07-feature-contract와-synthetic-rules-baseline)
- 사람 리포트: [HR-20260811-03](../reports/human/HR-20260811-03-r07-feature-rules-baseline.md)
