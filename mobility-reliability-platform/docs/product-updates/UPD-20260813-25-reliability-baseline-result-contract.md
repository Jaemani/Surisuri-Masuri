---
id: UPD-20260813-25
date: 2026-08-13
status: draft
version_or_deployment: reliability-baseline-result-v1-contract-b69dd01
roadmap_month: 2026-09 R10
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: R10 reliability baseline 결과 계약

## 요약

부품별 time-to-inspection 실험을 실제 성능 결과로 진행하기 전에, synthetic-only 결과의 계보·time split·censoring 집계·component abstention·배포 차단을 `reliability-baseline-result.v1` 계약으로 고정했다. 이번 업데이트는 결과 형식과 fixture 검증만 다루며 실제 reliability metric이나 ML/field 배포를 제공하지 않는다.

## 변경 전 문제

- R10의 outcome·censoring·time split·abstention 계획에 strict 결과 wire contract가 없었다.
- 합성 fixture, 실제 event/censoring 데이터, field 결과와 배포 가능성을 혼동할 여지가 있었다.

## 변경 후 동작

- 결과는 synthetic-only, `trainingPerformed=false`, `device-group-time-holdout.v1`, 세 시간창과 leakage flag를 요구한다.
- `fixed_interval`, `cumulative_distance`, `kaplan_meier` 비교 구조와 부품별 `evaluated`/`data_insufficient`를 strict decode한다.
- 표본·사건 부족 component는 abstain하고 metric을 생략한다.
- 모든 결과는 `deploymentAuthorized=false`, `deploymentDecision=defer`로 닫힌다.
- raw coordinate와 field scope/training/deployment 주장 fixture는 거부된다.

## 범위

- 포함: JSON Schema, synthetic valid/invalid fixture, contracts validator 등록
- 제외: 실제 수리·주행 export, event/censoring 생성, risk curve·calibration 측정, ML 학습, field/production 배포
- 배포 환경: `local`
- 데이터 유형: `synthetic` / `test`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| reliability result valid fixture | `rtk pnpm --filter @mobility-reliability/contracts test` | pass | [EVD-20260813-021](../evidence/2026-08-product.md#evd-20260813-021--reliability-baseline-time-splitcensoringabstention-contract) |
| invalid field/training/deployment/raw-coordinate/abstention-metric fixture 거부 | 같은 contracts validator 실행 | pass | [EVD-20260813-021](../evidence/2026-08-product.md#evd-20260813-021--reliability-baseline-time-splitcensoringabstention-contract) |
| 전체 contract fixture 회귀 | 같은 contracts validator 실행 | pass — 36 cases | [EVD-20260813-021](../evidence/2026-08-product.md#evd-20260813-021--reliability-baseline-time-splitcensoringabstention-contract) |

수치와 fixture 값은 계약 예시이며 실제 cohort·성능·현장 관측값이 아니다.

## 배포와 롤백

- 배포 방식·식별자: 배포 없음. local repository contract commit `b69dd01`.
- 기능 플래그 또는 점진 배포: 해당 없음.
- 롤백 조건: contract 의미가 잘못되었거나 fixture 검증 경계가 변경되면 후속 versioned contract와 ADR로 대체한다.
- 롤백 절차: production 데이터·서비스에 적용된 변경이 없으므로 배포 롤백은 없다. 문서·계약을 대체할 때는 이전 문서를 삭제하지 않고 `superseded` 관계를 기록한다.

## 알려진 제한과 후속 작업

- 제한: schema는 counts의 산술 합계와 시간창 비중복을 직접 보장하지 않으며, 실제 event/censoring·component sample도 없다.
- 제한: valid fixture의 sensitivity, specificity, Brier score, survival probability는 합성 예시다.
- 후속 작업: 승인된 manifest와 실제 export가 존재할 때 별도 dataset builder가 outcome/censoring/reconciliation/time split을 계산하고 사람 검토를 거친다.
- 후속 검증 시점: R10 실제 회의·데이터 검토 후. 현재 일정이나 결과를 미리 확정하지 않는다.

## 관련 기록

- 결정: [ADR-0055](../decisions/ADR-0055-reliability-baseline-result-contract.md)
- 증거: [EVD-20260813-021](../evidence/2026-08-product.md#evd-20260813-021--reliability-baseline-time-splitcensoringabstention-contract)
- 인시던트: 해당 없음
- 사람 대상 리포트: 아직 없음
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: TBD
- 실제 주장과 근거 일치 여부: 사람 검토 대기
- 검토 메모: contract-only local fixture 범위를 field·production 결과로 확대하지 않는다.
