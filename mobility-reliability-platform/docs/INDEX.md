# 프로젝트 문서 인덱스

이 문서는 Mobility Reliability Platform의 문서 진입점이자 기준 문서 지도다. 같은 사실을 여러 문서에 복제하지 않고, 질문별 원문과 현재 상태를 연결한다.

## 1. 프로젝트를 처음 읽는 순서

1. [프로젝트 상세 개요](./PROJECT_OVERVIEW.md) — 배경, 사용자, 제품 흐름, 기술 목표, 현재 사실
2. [프로젝트 헌장](./PROJECT_CHARTER.md) — 승인된 범위, 비목표, 성공 조건, 재사용 경계
3. [시스템 아키텍처](./architecture/SYSTEM_ARCHITECTURE.md) — 런타임 경계와 신뢰 경계
4. [8개월 로드맵](./ROADMAP.md) — 2026년 5월~12월 월별 기술 게이트
5. [마스터 실행계획](./plans/MASTER_EXECUTION_PLAN.md) — 작업흐름, 의존성, 반월 단위 실행과 완료 조건
6. [검증·증거 계획](./plans/VALIDATION_AND_EVIDENCE_PLAN.md) — 테스트 수준, KPI, 실기기·현장 증거 규칙
7. [정기보고·회의 운영계획](./plans/REPORTING_AND_MEETING_PLAN.md) — 월 2회, 총 16회 계획 보고와 실제 기록 방법
8. [데이터·ML·AI 실행계획](./plans/DATA_ML_AI_PLAN.md) — 데이터 계보, 두 모델, 근거형 보고서 에이전트
9. [릴리스·파일럿·최종 데모 계획](./plans/RELEASE_PILOT_DEMO_PLAN.md) — 환경 승격, 현장 도입, 발표·포트폴리오 증거
10. [위험 등록부](./plans/RISK_REGISTER.md) — 현재 위험, 탐지 신호, 대응과 차단 조건

## 2. 질문별 기준 문서

| 질문 | 기준 문서 | 보조 문서 |
| --- | --- | --- |
| 왜 새로 만드는가 | [상세 개요](./PROJECT_OVERVIEW.md) | [ADR-0001](./decisions/ADR-0001-greenfield-boundary.md) |
| 무엇을 만들고 무엇을 만들지 않는가 | [프로젝트 헌장](./PROJECT_CHARTER.md) | [로드맵](./ROADMAP.md) |
| 5~12월에 언제 무엇을 판단하는가 | [마스터 실행계획](./plans/MASTER_EXECUTION_PLAN.md) | [정기리포트 16개](./reports/fixed/README.md) |
| Firebase, Go, domain command와 worker의 책임은 무엇인가 | [시스템 아키텍처](./architecture/SYSTEM_ARCHITECTURE.md) | [ADR-0007](./decisions/ADR-0007-firebase-first-hybrid.md), [ADR-0011](./decisions/ADR-0011-domain-command-worker-boundaries.md) |
| 데이터 구조와 이관 기준은 무엇인가 | [Target Domain Model](./data/TARGET_DOMAIN_MODEL.md) | [Legacy Inventory](./data/LEGACY_DATA_INVENTORY.md), [Migration Gates](./data/MIGRATION_GATES.md) |
| ML·AI가 무엇을 판단하고 무엇을 하지 않는가 | [데이터·ML·AI 계획](./plans/DATA_ML_AI_PLAN.md) | [ADR-0006](./decisions/ADR-0006-model-and-llm-responsibility.md) |
| 어떤 결과를 완료로 인정하는가 | [검증·증거 계획](./plans/VALIDATION_AND_EVIDENCE_PLAN.md) | [증거 인덱스](./evidence/README.md) |
| 정기보고와 회의록을 어떻게 작성하는가 | [보고·회의 계획](./plans/REPORTING_AND_MEETING_PLAN.md) | [문서 운영 정책](./DOCUMENTATION_POLICY.md) |
| 실제 제품에서 무엇이 바뀌었는가 | [제품 업데이트](./product-updates/README.md) | [월별 증거](./evidence/2026-07.md) |
| 심각한 장애가 있었는가 | [인시던트 정책](./incidents/README.md) | 해당 `INC-*` 문서 |
| WSL과 실기기에서 어떻게 실행하는가 | [WSL Runbook](./development/WSL_RUNBOOK.md) | 앱·서비스별 README |

## 3. 계획과 실제의 분리

다음 세 층을 섞지 않는다.

| 층 | 의미 | 기록 위치 |
| --- | --- | --- |
| 기준 계획 | 8개월 동안 검토할 순서와 기대 산출물 | `ROADMAP.md`, `plans/`, `reports/fixed/` |
| 실제 변경 | 코드·제품에서 검증된 변화 | `product-updates/`, 커밋, 배포 기록 |
| 실제 증거 | 테스트·화면·측정·사람 확인 결과 | `evidence/`, `reports/human/`, 필요 시 `incidents/` |

계획이 실제보다 앞서거나 뒤처져도 계획 문서를 성취 기록처럼 고쳐 쓰지 않는다. 차이는 해당 회차 정기리포트의 `실제 진행 입력란`과 증거 링크에 남긴다.

## 4. 2026-07-21 현재 검증된 구현 경계

다음은 문서 작성 시점에 로컬·클린 러너 증거가 연결된 범위다.

- 신규 monorepo, 문서 스트림, 계약·Firebase Rules 테스트 기반
- React Native 앱의 foreground 위치 수집 코드와 SQLite outbox 구현, JS 정적 export·순수 policy 검증. native SQLite/GPS callback과 실기기 동작은 미검증
- `telemetry-batch.v2` 계약과 raw telemetry에서 Firebase UID를 분리한 identity 경계
- Go telemetry ingest kernel의 strict decode, 멱등성·receipt·object 저장 인터페이스, fail-closed HTTP 경계
- Firebase Admin SDK dual-token verifier·App ID allowlist·production emulator guard factory의 local synthetic 검증. executable에는 미연결
- active tenant·beneficiary·installation·trip·assignment·current consent를 교차 검사하는 pure authorization policy와 Firestore exact-read adapter의 local synthetic 검증. executable에는 미연결
- 위 authorization을 replay·conflict 조회보다 먼저 재평가하고 두 uniqueness index와 최초 receipt를 같은 Firestore transaction에서 생성하는 admission adapter의 local fake-seam 및 Firestore Emulator concurrent same-batch 검증. ADC/IAM·production은 미검증이며 executable에는 미연결
- server-only current consent projection의 Firebase client direct read/write 차단
- Firestore client read를 own-person 또는 `case_worker`·`tenant_admin` 운영 범위로 제한하고 tenant/person query constraint를 고정한 local Rules Emulator 검증. production Rules에는 미배포
- adapter 미구성 상태에서 `/healthz=200`, `/readyz`와 ingest는 `503`

다음은 아직 운영 완료로 주장하지 않는다.

- background GPS 실기기 검증과 모바일 업로드
- 실제 Firebase ID token/App Check가 연결된 실행 경로
- production Firebase Rules 배포와 실제 mobile/admin query·index 검증
- Firestore Emulator 철회 경쟁·손상 fixture와 production transaction·ADC/IAM 검증, Cloud Storage production adapter와 receipt/object 복구
- 수리데이터 실제 이관, ML 학습, ONNX 배포, 생존분석
- 기관 콘솔, field pilot, 운영 SLO, AI report agent

최신 실제 상태는 [제품 업데이트](./product-updates/README.md)와 [증거 인덱스](./evidence/2026-07.md)를 우선한다.

## 5. 변경 규칙

- 범위 또는 성공 조건을 바꾸면 `PROJECT_CHARTER.md`와 ADR을 함께 갱신한다.
- 월별 순서를 바꾸면 `ROADMAP.md`와 `MASTER_EXECUTION_PLAN.md`의 차이를 기록한다.
- 저장·권한·신뢰 경계를 바꾸면 아키텍처, Target Domain Model, 관련 ADR을 갱신한다.
- 검증 기준을 바꾸면 Validation Plan에 이유와 적용 시작 버전을 남긴다.
- 미래 정기리포트의 계획 문구는 바꿀 수 있지만 실제 수행처럼 표현하지 않는다.
