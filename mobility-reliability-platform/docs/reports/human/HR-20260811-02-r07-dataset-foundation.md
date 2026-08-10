---
id: HR-20260811-02
report_type: requested
status: draft
period_start: 2026-08-11
period_end: 2026-08-11
issued_at: TBD
roadmap_month: 2026-08 R07-A
technical_gate: R07 synthetic dataset and quality-label foundation
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: R07 합성 데이터셋 기반

## 한눈에 보기

- 8개월 로드맵상 위치: 2026년 8월 R07의 A단계.
- 계획: 모델 학습 전에 라벨 계약, deterministic synthetic trace, manifest hash,
  group/time split과 benchmark 경계를 고정한다.
- 실제 상태: commit `a20a85b`에서 계약·generator·검증·CI 기반을 완료했다.
- 핵심 결과: 48 synthetic trace, 576 samples, 네 known class, 각 split 16 trace,
  seed `20260811`, embedded dataset hash
  `7e9242ad2c01d4abf94fd4a6c62153ef735b351f66481b4239f1c7bf3833b24c`.
- 중요한 제한: 모델 성능·현장·실기기·배포·사용자 성과는 아직 검증하지 않았다.
- 사람에게 필요한 확인: synthetic 결과를 현장 성능으로 표현하지 않는지, 다음 R07-B
  feature/baseline 단계로 진행할지 검토한다.

## 1. 계획

> 아래는 8개월 로드맵상 계획이며, 실제 성과와 구분한다.

- 로드맵상 위치: 8월 R07-A — label·baseline·dataset foundation.
- 계획한 기술 주제:
  - `quality-label.v1`와 `quality-dataset-manifest.v1` 계약
  - `telemetry-batch.v2` 기반 synthetic trace generator
  - seed·hash·manifest·count·linkage 보존
  - group/time holdout, known-class coverage, developer-device exclusion
  - Python/contract CI 재현 gate
- 예상 산출물: 네 class 데이터셋, manifest, 검증기, fixture, runbook과 evidence record.
- 계획 완료 조건: 같은 seed에서 같은 dataset/manifest가 재생되고, benchmark loader가
  split leakage·developer-device·계약 위반을 차단한다.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| label contract | 검증됨 | 네 known class와 review/abstain 상태를 strict JSON Schema로 고정 | 계획 범위 충족 | WSL2 local / CI |
| synthetic generator | 검증됨 | 48 trace, 576 sample, seed `20260811` | 실제 데이터 대신 synthetic으로 수행 | WSL2 local / CI |
| split | 검증됨 | train/validation/test 각 16 trace, group/time 및 validation/test class coverage 통과 | 계획 범위 충족 | WSL2 local / CI |
| dataset manifest | 검증됨 | hash·count·trace/batch linkage·provenance 검증 | 계획 범위 충족 | WSL2 local / CI |
| Python tests | 검증됨 | 26 tests pass | 모델 학습은 포함하지 않음 | locked Python 3.12 / CI |
| contract fixtures | 검증됨 | 11 fixture cases pass | 모델 성능 평가는 포함하지 않음 | Node/Ajv / workspace |
| workspace gates | 검증됨 | workspace check/build/test pass | 배포 runtime은 없음 | workspace / CI |

### 실제 결과 상세

- `unknown_review_required`는 학습 class로 생성하지 않고 review/abstain 상태로 분리했다.
- synthetic 좌표는 `(0, 0)` 주변이며, 실제 서울 경로·실사용자 위치가 아니다.
- `developer_device` source는 benchmark loader에서 제외된다.
- manifest의 canonical dataset hash는
  `7e9242ad2c01d4abf94fd4a6c62153ef735b351f66481b4239f1c7bf3833b24c`이다.
- 결과 데이터 유형: `synthetic` only.
- 결과는 source commit `a20a85b`에 기록되어 있다.

### 확인하지 않은 것

- 모델 학습, 정확도·precision·recall·F1·calibration·ablation
- 실제 Android/iPhone GPS·배터리·background lifecycle
- 실제 사용자·복지관·수리사 현장 반응 및 운영 성과
- staging/production/앱 배포, 비용 절감, 정책·사업 성과

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| 48 trace/576 sample과 네 class 생성 | [EVD-20260811-002](../../evidence/2026-08.md#evd-20260811-002) | verified by code/CI; human review pending | 사람 확인 필요 |
| split 16/16/16, group/time holdout과 developer-device 차단 | [EVD-20260811-002](../../evidence/2026-08.md#evd-20260811-002) | verified by Python tests | 사람 확인 필요 |
| dataset hash와 manifest linkage | [EVD-20260811-002](../../evidence/2026-08.md#evd-20260811-002) | verified by generator/validator | 사람 확인 필요 |
| Python 26 tests, contract 11 fixture cases | [EVD-20260811-002](../../evidence/2026-08.md#evd-20260811-002) · [ML_R07_RUNBOOK](../../development/ML_R07_RUNBOOK.md) | verified by locked commands | 사람 확인 필요 |
| workspace check/build/test | [EVD-20260811-002](../../evidence/2026-08.md#evd-20260811-002) | pass | 사람 확인 필요 |

근거는 합성·계약·CI 결과만 증명한다. 성능·현장·사용자 성과의 근거로 사용하지 않는다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0040](../../decisions/ADR-0040-r07-quality-dataset-contract.md)
- 실제 제품 업데이트: [UPD-20260811-02](../../product-updates/UPD-20260811-02-r07-synthetic-dataset-contract.md)
- 인시던트: 해당 없음 — 미배포 synthetic/local 검증 단계
- 열린 위험: 실제 데이터와 synthetic 결과의 혼동, 향후 label/feature version 불일치,
  모델 성능을 측정하기 전 성능 표현이 앞서가는 위험

## 다음 회차

- 8개월 계획상 다음 주제: R07-B versioned feature extractor와 rules baseline.
- 실제 상태를 반영한 다음 검증: 동일 frozen manifest에서 feature parity·baseline
  evaluation을 실행하고, 성능 주장을 하기 전 평가셋·split·metric을 고정한다.
- 필요한 사람의 결정·지원: 다음 단계의 feature 범위와 synthetic/실제 데이터 사용
  권한을 확인한다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 사람 확인 필요

이 문서는 사람에게 전달하는 기술 결과 보고이며, 존재하지 않는 회의·참석자·사진·지출을
기록하지 않는다.

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] 실제 주장마다 증거 링크를 연결했다.
- [x] 수치에 데이터 유형과 모수를 표시했다.
- [x] 합성·테스트·현장 데이터를 구분했다.
- [x] 성능·현장·배포·사용자 성과를 미검증으로 표시했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 ADR·EVD·runbook 링크와 실제 주장·근거를 확인한다.
