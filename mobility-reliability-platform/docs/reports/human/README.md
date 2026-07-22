# 사람 대상 요청 리포트

이 디렉터리는 착수·인시던트·외부 검토 등 별도 요청에 따라 발행하는 사람 대상 리포트를 보관한다. 5월부터 12월까지 월 2회 사용하는 16개의 계획 기준 사전작성본은 [`../fixed/`](../fixed/)에 있다. 두 종류 모두 **8개월 로드맵의 계획상 위치**와 보고 시점의 실제 진행·근거를 분리한다.

## 식별자와 작성 시점

- 요청 리포트 ID: `HR-YYYYMMDD-NN`
- 파일명: `HR-YYYYMMDD-NN-short-title.md`
- 발행 주기: 착수·외부 검토·기술 점검·인시던트 요약 등 실제 요청이 있을 때
- 같은 날짜의 `NN`은 `01`부터 증가시키며 기존 ID를 재사용하지 않는다.
- 자동 초안은 `draft`, 사람 검토 전 증거는 `generated`로 표시한다.

## 작성 트리거

- 별도 착수·상태·기술 점검 요청
- SEV-0/1 상황처럼 즉시 사람에게 알려야 하는 사건
- 외부 검토·발표·복지관 공유를 위한 별도 요청

월 2회 고정 발행은 [`../fixed/`](../fixed/)에서만 관리한다. 같은 내용을 human 리포트로 다시 만들어 서로 다른 실제 상태를 만들지 않는다. 요청 리포트는 필요한 독자·목적에 맞게 원문 ADR·UPD·INC·EVD를 요약하고 링크한다.

## 반드시 분리할 세 구획

### 1. 계획

8개월 로드맵에서 이 회차가 다루도록 사전에 설계한 기술 주제, 예상 산출물, 검토 질문을 적는다. 미래 내용을 미리 작성할 수 있으나 상태는 `planned`다.

### 2. 실제

보고 기준일 현재 확인된 결과만 적는다. 완료되지 않았더라도 진행률을 꾸미지 않고 상태, 차이, 이유, 위험을 기록한다.

### 3. 근거

각 실제 주장에 `EVD`, `ADR`, `UPD`, `INC` 링크를 연결한다. 증거가 없으면 성과가 아니라 `확인 필요` 또는 `관찰`로 표현한다.

## 필수 필드

- 리포트 ID, 대상 기간, 발행일, 상태, 작성자·검토자, 대상 독자
- 8개월 로드맵상 월·기술 게이트와 이번 회차 목적
- 사전 계획, 실제 상태, 계획 대비 차이
- 실제 결과별 증거 ID와 검증 환경
- 중요한 결정, 제품 변화, 인시던트, 위험과 다음 회차 계획
- 회의가 실제로 있었다면 실제 일시·참석자·증빙의 수동 확인 상태

## 회의·행정 증빙 규칙

- 참석자, 일시, 사진, 영수증, 지출액, 서명은 **자동 생성하지 않는다**.
- 실제 회의가 없었다면 회의가 있었다고 쓰지 않는다. 리포트 작성 자체와 회의 수행은 별개다.
- 실제 회의가 있었다면 참석자와 증빙은 사람이 확인한 뒤 기록한다.
- 사진·영수증 원본은 승인된 저장소에 두고, 이 문서에는 민감정보를 제거한 링크와 확인 상태만 남긴다.
- 온라인 회의의 지출 여부 등 행정 판단을 기술 리포트가 대신하지 않는다.
- 미리 작성한 안건·결정 후보·예상 산출물은 `사전 계획`으로만 표시한다.

## 금지사항

- 미래 계획을 실제 수행 결과로 표현하지 않는다.
- 참석자·사진·지출·사용자 수·성능 수치를 생성하거나 추정하지 않는다.
- 합성 데이터 결과를 현장 실증으로 표현하지 않는다.
- 실제 상태를 맞추기 위해 Git 기록, 파일 날짜, 회의 시각을 소급 조작하지 않는다.
- 기술적 세부 의사결정, 장애 사후분석, 제품 변경 기록을 본문에 중복 작성하지 않고 원문을 링크한다.

## 상호 링크

- 의사결정: [`../../decisions/`](../../decisions/)
- 제품 업데이트: [`../../product-updates/`](../../product-updates/)
- 인시던트: [`../../incidents/`](../../incidents/)
- 증거: [`../../evidence/`](../../evidence/)

실제 결과 항목은 최소 하나의 검증 가능한 링크를 가져야 한다. 사전 계획에는 근거 링크가 없어도 되지만, 예상 산출물 ID를 실제 ID처럼 꾸미지 않는다.

## 현재 요청 리포트

| ID | 주제 | 상태 |
| --- | --- | --- |
| [HR-20260721-01](./HR-20260721-01-project-initiation.md) | 프로젝트 착수 | `draft` |
| [HR-20260721-02](./HR-20260721-02-foreground-telemetry.md) | foreground telemetry | `draft` |
| [HR-20260721-03](./HR-20260721-03-ingest-kernel.md) | fail-closed ingest kernel | `draft` |
| [HR-20260721-04](./HR-20260721-04-telemetry-v2-contract.md) | telemetry v2 계약 | `draft` |
| [HR-20260721-05](./HR-20260721-05-firebase-dual-token-verifier.md) | Firebase dual-token verifier | `draft` |
| [HR-20260721-06](./HR-20260721-06-firestore-batch-authorization.md) | Firestore batch authorization | `draft` |
| [HR-20260721-07](./HR-20260721-07-atomic-telemetry-admission.md) | atomic telemetry admission | `draft` |
| [HR-20260721-08](./HR-20260721-08-immutable-artifact-lineage.md) | immutable artifact lineage | `draft` |
| [HR-20260721-09](./HR-20260721-09-fenced-forward-admission.md) | fenced forward admission | `draft` |
| [HR-20260721-10](./HR-20260721-10-recovery-claims-cleanup-transition.md) | recovery claim·cleanup transition | `draft` |
| [HR-20260721-11](./HR-20260721-11-read-only-artifact-classifier-contract.md) | read-only artifact classifier 계약 | `draft` |
| [HR-20260721-12](./HR-20260721-12-http-gcs-artifact-reader.md) | HTTP GCS artifact inventory reader | `draft` |
| [HR-20260721-13](./HR-20260721-13-artifact-content-validator.md) | telemetry artifact content lineage validator | `draft` |
| [HR-20260721-14](./HR-20260721-14-read-only-artifact-classifier.md) | generation-pinned read-only artifact classifier | `draft` |
| [HR-20260721-15](./HR-20260721-15-current-forward-recovery-authorization.md) | current-state forward recovery authorization | `draft` |
| [HR-20260722-16](./HR-20260722-16-manifest-only-forward-repair.md) | manifest-only forward repair boundary | `draft` |
| [HR-20260722-17](./HR-20260722-17-forward-recovery-outcome.md) | forward recovery outcome·attempt failure boundary | `draft` |
| [HR-20260722-18](./HR-20260722-18-authorization-disposition.md) | current authorization disposition boundary | `draft` |
