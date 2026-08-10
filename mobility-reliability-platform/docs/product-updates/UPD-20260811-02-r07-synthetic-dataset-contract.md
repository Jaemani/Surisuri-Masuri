---
id: UPD-20260811-02
date: 2026-08-11
status: draft
version_or_deployment: r07-dataset-foundation-a20a85b
roadmap_month: 2026-08 R07-A
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: R07 합성 텔레메트리 데이터셋 계약

## 요약

8개월 로드맵의 8월 R07-A 단계에서, 데이터 품질 모델 학습 전에 필요한 라벨·데이터셋
manifest·group/time split·hash 계약을 고정했다. `quality-label.v1`과
`quality-dataset-manifest.v1` JSON Schema, `telemetry-batch.v2` 검증, seed 기반 합성
trace generator와 Python 검증기를 하나의 재현 가능한 기반으로 연결했다.

이번 업데이트는 모델 성능이나 현장 성과를 추가한 것이 아니다. 실제 결과는 합성 데이터
계약과 검증 파이프라인이 commit `a20a85b`에서 재현된다는 범위로 한정한다.

## 변경 전 문제

- R07의 네 가지 이동·품질 라벨과 review/abstain 상태의 wire contract가 고정되어 있지
  않았다.
- 합성 trace의 seed, source, split, 데이터 계보와 dataset hash를 동일한 방식으로
  재생할 기준이 없었다.
- `developer_device` trace가 benchmark에 섞이지 않는지, scenario group과 시간축이
  split을 넘지 않는지 자동 검증할 경계가 없었다.
- 합성 데이터가 실제 사용자·현장 데이터와 혼동되지 않도록 하는 CI·문서 경계가
  부족했다.

## 변경 후 동작

- 네 known class를 고정했다.
  - `mobility_aid_likely`
  - `vehicle_likely`
  - `stationary`
  - `gps_noise_or_insufficient`
- `unknown_review_required`는 학습 class가 아니라 `review_required`/`abstained`
  상태로만 표현한다.
- seed `20260811`로 48개의 synthetic trace와 576개의 GPS sample을 생성한다.
- train/validation/test에 각각 16 trace를 배치하고, group/time holdout·validation/test
  known-class coverage·sample chronology를 검증한다.
- dataset manifest에 포함된 canonical dataset hash는 다음과 같다.

  `7e9242ad2c01d4abf94fd4a6c62153ef735b351f66481b4239f1c7bf3833b24c`

- 합성 좌표는 실제 서울 경로가 아닌 `(0, 0)` 주변 virtual coordinate이며, generator와
  validation 오류는 이름·전화번호·Firebase UID·원본 좌표를 로그로 출력하지 않는다.
- `sourceKind=developer_device`는 기록 대상으로는 허용할 수 있지만 benchmark loader가
  거부한다.

## 범위

| 구분 | 내용 |
| --- | --- |
| 포함 | quality label/manifest JSON Schema, telemetry-batch.v2 검증, deterministic synthetic generator, hash·count·linkage·split 검증, CI 재현 gate |
| 데이터 유형 | `synthetic` only |
| 환경 | WSL2 local source와 GitHub Actions locked Python environment |
| 로드맵 위치 | 2026년 8월 R07-A: label·dataset contract foundation |
| 제외 | 모델 학습·성능평가·PyTorch/ONNX 배포, 실제 GPS·실기기, 현장·복지관 사용자, staging/production 배포 |

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| 네 known class와 review/abstain 계약 | JSON Schema valid/invalid/review fixture | pass | [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002) |
| deterministic synthetic trace 생성 | seed `20260811`, trace/sample count 비교 | 48 trace / 576 sample, pass | [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002) |
| split·class coverage·leakage 경계 | group/time holdout와 validation/test coverage test | train/validation/test 각 16, pass | [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002) |
| dataset hash·manifest linkage | manifest hash/count/trace·batch metadata 비교 | `7e9242ad2c01d4abf94fd4a6c62153ef735b351f66481b4239f1c7bf3833b24c`, pass | [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002) |
| Python foundation tests | locked test suite | 26 tests, pass | [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002) · [ML_R07_RUNBOOK](../development/ML_R07_RUNBOOK.md) |
| Contract fixtures | contracts validator | 11 fixture cases, pass | [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002) · [ML_R07_RUNBOOK](../development/ML_R07_RUNBOOK.md) |
| Workspace integration | workspace check/build/test | pass | [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002) |

수치와 결과는 commit `a20a85b`의 합성·계약 검증 범위에 대한 것이다. 모델 정확도,
recall/precision, calibration, 배터리, GPS 품질, 실제 사용자 행동 또는 비용 절감은
측정하지 않았다.

## 배포와 롤백

- 배포: 수행하지 않음. commit `a20a85b`의 source·test·CI artifact만 확정했다.
- staging/production/field runtime: 해당 없음.
- rollback: 실행하지 않음. 실제 데이터베이스·사용자 데이터·기관 운영에 적용된 변경이
  없으므로 runtime rollback 대상은 없다.

## 알려진 제한과 후속 작업

- 현재 데이터는 전부 synthetic이며 실제 위치나 실제 사용자 표본을 대표하지 않는다.
- 모델 학습·모델 성능·ONNX 변환·Android/iPhone 추론은 아직 검증하지 않았다.
- `developer_device` 차단은 benchmark 경계 검증이며, 실기기 lifecycle 결과를 의미하지
  않는다.
- 다음 R07-B 단계에서 versioned feature extractor와 rules baseline을 같은 manifest와
  split에 연결한다.
- 이후 R07-C/R08에서만 PyTorch 후보·ONNX·모바일 parity·성능 평가를 다룬다.

## 관련 기록

- 결정: [ADR-0040](../decisions/ADR-0040-r07-quality-dataset-contract.md)
- 증거: [EVD-20260811-002](../evidence/2026-08.md#evd-20260811-002)
- 실행 runbook: [ML_R07_RUNBOOK](../development/ML_R07_RUNBOOK.md)
- 사람 대상 리포트: [HR-20260811-02](../reports/human/HR-20260811-02-r07-dataset-foundation.md)
- 인시던트: 해당 없음 — 미배포 synthetic/local 검증 단계

## 검토

- 검토자: human review required
- 실제 주장과 근거 일치 여부: 사람이 확인하기 전까지 `draft`
- 회의·참석자·사진·지출: 이번 결과에는 없음
