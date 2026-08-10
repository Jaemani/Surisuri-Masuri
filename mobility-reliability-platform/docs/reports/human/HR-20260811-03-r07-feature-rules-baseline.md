---
id: HR-20260811-03
report_type: requested
status: draft
period_start: 2026-08-11
period_end: 2026-08-11
issued_at: TBD
roadmap_month: 2026-08 R07-B
technical_gate: coordinate-free feature contract and synthetic rules baseline
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: R07 feature contract·rules baseline

## 1. 계획

8개월 로드맵의 8월 R07-B에서는 R07-A의 frozen manifest를 재사용해 GPS 원본을
반환하지 않는 feature extractor와 규칙 baseline을 고정하고, 이후 PyTorch/ONNX 단계의
입력·평가 경계를 만든다.

## 2. 실제

commit `a9b20d9`에서 다음을 구현·검증했다.

- `quality-features.v1` contract, valid/review/invalid fixtures
- trace/batch/dataset/feature nested lineage와 feature SHA-256
- malformed sample, missing accuracy, non-synthetic source의 review 경계
- `r07-rules-baseline.v1` prediction과 `quality-baseline-result.v1` schema validator
- frozen synthetic dataset의 48 trace 평가

| 구분 | 실제 결과 | 범위 제한 |
| --- | --- | --- |
| Contract fixtures | 16 pass | JSON contract만 검증 |
| Python tests | 49 pass | local `.venv` 실행 |
| Dataset | 48 synthetic trace; split별 16 | 실제·field data 아님 |
| Baseline | 각 split macro-F1 1.0; abstain rate 0 | synthetic 규칙 기준선일 뿐 |
| Coordinate safety | feature/prediction에 raw coordinate 없음 | 원본 telemetry는 입력 단계에 존재할 수 있음 |

## 3. 계획 대비 차이

- 계획한 feature/rules baseline 게이트는 구현했다.
- PyTorch 모델, ONNX 변환, 모바일 inference는 아직 다음 단계다.
- Android/iPhone 실기기와 복지관·수리데이터는 이번 결과에 사용하지 않았다.
- production Firebase/Cloud Run 배포와 현장 성과는 검증하지 않았다.

## 4. 근거

| 주장 | 근거 | 상태 |
| --- | --- | --- |
| feature contract와 lineage/hash | [EVD-20260811-003](../../evidence/2026-08.md#evd-20260811-003--r07-feature-contract와-synthetic-rules-baseline) | generated; 사람 검토 대기 |
| rules result schema와 synthetic provenance | [EVD-20260811-003](../../evidence/2026-08.md#evd-20260811-003--r07-feature-contract와-synthetic-rules-baseline) | generated; 사람 검토 대기 |
| 49 Python tests / 16 fixture cases | [ML R07 Runbook](../../development/ML_R07_RUNBOOK.md) | local pass; 사람 검토 대기 |

## 5. 결정·제품 변화·인시던트

- 결정: [ADR-0041](../../decisions/ADR-0041-r07-feature-and-rules-contract.md)
- 제품 업데이트: [UPD-20260811-03](../../product-updates/UPD-20260811-03-r07-feature-rules-baseline.md)
- 인시던트: 해당 없음 — local synthetic 검증이며 외부 영향 없음
- 다음 결정: R07-C PyTorch 후보를 동일 split에서 비교할지, rules fallback을 유지할지

## 6. 사람에게 필요한 확인

- synthetic macro-F1을 현장 성능으로 표현하지 않는지 확인
- raw 좌표·PII가 feature/report artifact에 들어가지 않는지 확인
- R07-C 모델 학습과 ONNX 진입 조건을 승인

## 회의·행정 증빙

- 실제 회의 여부: 이 리포트에는 기록하지 않음
- 실제 참석자·일시·사진·지출: 해당 없음 / 사람 확인 필요
- 이 문서는 회의록이나 참석 증빙을 대체하지 않는다.
