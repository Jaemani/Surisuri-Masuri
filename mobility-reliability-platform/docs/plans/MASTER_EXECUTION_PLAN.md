# 2026년 5–12월 마스터 실행계획

## 1. 계획 상태와 사용법

- 상태: approved baseline
- 기간: 2026-05-01 ~ 2026-12-31
- 기준 로드맵: [2026년 5–12월 기술 로드맵](../ROADMAP.md)
- 보고 주기: 매월 15일·말일, 총 16회
- 실행 특성: 1인 개발을 기준으로 하며 프론트엔드·백엔드·모바일·데이터·ML 담당 구분을 의존성으로 두지 않음

이 계획은 월별 기술 성과의 순서와 검증 기준을 고정한다. 실제 개발 속도가 빠르거나 느려도 과거·미래 보고서를 실제 성취처럼 바꾸지 않는다. 일정 차이는 해당 정기리포트에 실제 상태, 이유, 다음 결정을 기록한다.

## 2. 실행 원칙

1. 얇은 end-to-end vertical slice를 먼저 만들고, 각 경계를 실기기·실패 시나리오까지 강화한다.
2. 클라이언트 입력, LLM 출력, 모델 확률은 신뢰하지 않고 서버 계약·권한·근거 검증을 둔다.
3. GPS sample은 Firestore 개별 문서로 저장하지 않는다.
4. BigQuery·PostGIS 등 확장은 실제 query·비용·SLA 요구가 증명될 때 ADR을 거쳐 활성화한다.
5. 모델은 baseline보다 이득이 없거나 데이터가 부족하면 배포하지 않고 규칙 또는 abstention을 유지한다.
6. 기능 완료와 발표 준비를 분리하지 않는다. 각 게이트는 코드와 함께 화면·그래프·테스트표 중 하나를 남긴다.
7. WSL 호스트에 없는 도구는 고정 Docker/CI 환경에서 검증하며, iOS native 검증은 EAS development build와 실기기를 사용한다.

## 3. 작업흐름

| ID | 작업흐름 | 범위 | 최종 산출물 |
| --- | --- | --- | --- |
| WS1 | Mobile Capture | 권한, 주행 세션, foreground/background GPS, SQLite outbox, sync, QR, 접근성 | Android/iOS 앱과 복구 가능한 수집 |
| WS2 | Trusted Telemetry & Commands | wire contract, Firebase identity/App Check, telemetry authorization, receipt/Storage, session·trip/domain command | fail-closed 수집·도메인 명령 경로 |
| WS3 | Domain & Reliability Twin | async worker, 수리 importer, event model, device/part projection, replay | 특정 시점 상태를 재구성하는 digital twin |
| WS4 | ML & Evaluation | labeling, rules baseline, PyTorch quality model, ONNX, survival/calibration | 재현 가능한 두 모델과 배포/유보 결정 |
| WS5 | Evidence-grounded AI | Fact Store, report schema, agent loop, validator, fallback | 근거 ID가 연결된 대상별 보고서 |
| WS6 | Console & Field Ops | tenant console, adapter, pilot, observability, security, accessibility | 복지관 운영 화면과 실증 운영 근거 |
| WS7 | EvidenceOps & Communication | ADR, update, incident, evidence, fixed report, demo/whitepaper | 계획·실제·증거가 연결된 기술 포트폴리오 |

## 4. 핵심 의존성

```text
WS1 local event log
  └─> WS2 authenticated session/trip command + batch ingest
        ├─> raw object/receipt
        └─> WS3 async privacy/quality + trip/repair event replay
              ├─> WS4 reliability features/models
              └─> WS5 evidence facts/reports
                    └─> WS6 console/pilot

WS7은 모든 단계의 결정·테스트·실패·화면을 병렬로 수집한다.
```

순서를 바꿔 프로토타입을 만들 수는 있지만, 다음을 건너뛰고 production-ready로 표시할 수 없다.

- 모바일 업로드 전: domain command로 발급한 trip 또는 명시적 offline reconciliation, consent revision, installation, immutable batch, retry identity
- 수집 활성화 전: ID token, App Check, membership/device/trip/consent authorization
- 모델 학습 전: dataset manifest, time/group split, baseline, leakage check
- AI 리포트 전: deterministic fact, report schema, claim-evidence validator, fallback
- pilot 전: 삭제·복구, 접근성, 최소 운영 대시보드, support/incident 경로

## 5. 월별·반월별 상세 계획

### 5월 — 신규 제품과 신뢰 경계 정의

**진입 조건**

- 기존 프로젝트를 신규 런타임으로 재사용하지 않는 원칙 승인
- 모바일 자체 GPS 사용과 Android/iPhone 실기기 가용

**5월 1차 / R01**

- 과거 제품·데이터·브랜드의 재사용 경계를 문서화한다.
- 사용자·수리사·복지관·정책 담당자의 과업과 데이터 접근을 정의한다.
- 8개월 기술 포트폴리오, 비목표, 증거 원칙을 확정한다.
- 모바일/Firebase/Go/Storage/Firestore/ML/agent의 context diagram을 만든다.

**5월 2차 / R02**

- 모바일 권한·동의·주행 세션 UX와 GPS event 최소 필드를 설계한다.
- 위치 원본, PII, 도메인 상태의 분리와 삭제 책임을 정의한다.
- app shell과 한 번의 foreground GPS vertical slice를 목표로 한다.

**월말 게이트**

- greenfield/IP 경계, 시스템 경계, telemetry event 초안, 위치 lifecycle이 문서화된다.
- 발표 카드: 이전/신규 경계도, 모바일 흐름, 첫 주행 지도 또는 합성 재생.

**진행이 막힌 경우의 독립 산출물**

- Android/iOS 위치 권한 상태표, 개인정보 threat model, GPS fixture generator 중 하나를 완결한다.

### 6월 — 복구 가능한 모바일 수집

**진입 조건**

- 명시적 주행 세션과 로컬 event identity가 정의됨
- 실제 위치를 Git·일반 로그에 남기지 않는 개발 규칙 확정

**6월 1차 / R03**

- foreground/background 권한 흐름, OS lifecycle, 수집 모드를 구현한다.
- SQLite WAL, schema migration, append-only payload와 mutable delivery state를 분리한다.
- 앱 재시작 후 열린 trip과 미전송 event를 복원한다.

**6월 2차 / R04**

- immutable batch assembler, idempotency identity, 재시도·backoff를 구현한다.
- 비행기 모드, 앱 background, 강제종료, 재부팅, 권한 철회 시나리오를 실기기에서 검증한다.
- sampling profile별 accuracy·수집 간격·배터리 측정 프로토콜을 고정한다.

**월말 게이트**

- Android/iPhone에서 지원 가능한 lifecycle 차이가 문서화된다.
- offline event가 재연결 후 논리 중복 없이 전달되는 테스트 경로가 있다.
- 발표 카드: 비행기 모드 수집→재연결 영상, queue 상태도, 배터리·정확도 그래프.

**진행이 막힌 경우의 독립 산출물**

- 앱 종료 복구, 권한 거부 UX, SQLite migration corruption test, 합성 trace replay 중 하나를 수행한다.

### 7월 — 인증된 텔레메트리 데이터면

**진입 조건**

- versioned telemetry batch와 mobile installation/session identity가 있음
- Firebase control plane과 Cloud Storage raw batch 경계가 accepted ADR로 고정됨

**7월 1차 / R05**

- strict telemetry decode, 요청 크기 제한, Firebase ID token/App Check 검증을 구성한다.
- membership, device assignment, server trip, installation, consent revision authorizer를 만든다.
- derived idempotency, client batch uniqueness, UUIDv7 server batch, receipt 상태 전이를 구현한다.

**7월 2차 / R06**

- deterministic gzip Cloud Storage object와 Firestore receipt transaction을 연결한다.
- partial failure, replay, conflict, concurrent retry, storage precondition을 검증한다.
- pending receipt에 request lease와 fencing token을 적용하고, stale finalizer·raw-only·manifest-only·stored-missing crash matrix를 검증한다.
- 만료 전 forward reconciliation과 만료 후 generation-pinned cleanup target을 분리한다. deadline cleanup은 별도 fenced transition으로 시작하고, receipt purge는 nested attempt·cleanup target·integrity finding을 먼저 비운 뒤 마지막 linkage transaction으로 끝낸다.
- GPS 품질 flag, 시작·도착 마스킹, lifecycle·deletion lineage, 비용 계측을 설계한다.
- BigQuery 활성화 여부는 실제 분석 query와 비용 실험으로만 판단한다.

**월말 게이트**

- 무인증·앱 무결성 실패·tenant 위반·동의 위반 batch가 write 전에 차단된다.
- sample별 Firestore write가 0이고 object/receipt 계보가 일치하며 stale fence mutation이 0이다.
- 발표 카드: 401/403/409/503 failure matrix, raw→receipt→projection 계보, 중복 10회 테스트.

**진행이 막힌 경우의 독립 산출물**

- header parser fuzz, receipt transaction emulator test, object conflict test, 로그 좌표 scan 중 하나를 수행한다.

### 8월 — 온디바이스 데이터 품질 ML

**진입 조건**

- 승인된 synthetic/field trace manifest와 feature 추출 계약이 있음
- 모델 출력이 안전 판단이 아니라 품질 보조 신호임이 확정됨

**8월 1차 / R07**

- 주행/차량 이동/GPS 오류/정지 등 라벨 정의와 검토 도구를 만든다.
- 속도·가속·정지·방향·accuracy·motion feature를 버전 관리한다.
- 규칙 baseline과 PyTorch 1D CNN/GRU 등 최소 후보를 같은 split에서 비교한다.

**8월 2차 / R08**

- 선택 모델을 ONNX로 export하고 Python/모바일 feature parity를 검증한다.
- float/quantized 모델을 Android/iPhone에서 크기·지연·메모리·정확도로 비교한다.
- 모델 누락·파일 손상·지원하지 않는 version에서 rules fallback을 제공한다.

**월말 게이트**

- 모델이 baseline과 동일 평가셋에서 비교되고 실패 사례가 분류된다.
- 앱 수집은 모델 장애와 독립적으로 동작한다.
- 발표 카드: confusion matrix, ablation, ONNX parity, 실기기 latency 화면.

**진행이 막힌 경우의 독립 산출물**

- labeling agreement test, feature parity property test, quantization benchmark, model-card 초안을 완결한다.

### 9월 — 이벤트 기반 기기 상태와 신뢰성 모델

**진입 조건**

- 적법한 수리 export 또는 명시적 synthetic dataset이 있음
- legacy crosswalk, quarantine, provenance 규칙이 정의됨

**9월 1차 / R09**

- RepairLogged, PartReplaced, InspectionCompleted, TripSummarized 등 event를 정규화한다.
- projection version과 checkpoint를 두고 기기·부품 상태를 replay한다.
- importer dry-run, ID crosswalk, count reconciliation, quarantine reason을 만든다.

**9월 2차 / R10**

- outcome, censoring, risk clock reset, time/group split을 정의한다.
- 고정 기간·누적거리 규칙, Cox/Weibull, tree 계열 후보를 비교한다.
- 부품별 sample·confidence·calibration·data_insufficient를 함께 보고한다.

**월말 게이트**

- 같은 event stream replay 결과의 canonical checksum이 일치한다.
- 모델이 baseline보다 가치가 없거나 표본이 부족하면 배포 유보 결정이 남는다.
- 발표 카드: 기기 타임라인, 부품 상태 replay, Kaplan–Meier/위험곡선, 실패 케이스.

**진행이 막힌 경우의 독립 산출물**

- synthetic event generator, replay checksum, censoring unit test, importer quarantine dashboard 중 하나를 완결한다.

### 10월 — 보정된 판단 지원과 근거형 AI

**진입 조건**

- 모델/규칙 결과가 versioned fact로 변환됨
- 사람 판단과 모델 예측이 다른 provenance를 가짐

**10월 1차 / R11**

- calibration curve, threshold, abstention, distribution shift 경보를 설계한다.
- 사용자·수리사·복지관별 정보 깊이와 금지 표현을 정의한다.
- 수리사 feedback은 원 예측을 수정하지 않고 별도 review event로 저장한다.

**10월 2차 / R12**

- Fact Store, report JSON schema, planner→writer→validator→fallback loop를 구현한다.
- 수치 일치·날짜 범위·Fact ID·금지 주장 검사를 만든다.
- timeout, invalid JSON, prompt injection, 근거 누락, 모순 fact 평가셋을 실행한다.

**월말 게이트**

- 핵심 문장은 유효한 Fact ID 또는 `확인 필요` 상태를 가진다.
- LLM 장애 시 결정론적 최소 리포트가 남는다.
- 발표 카드: 근거 열기 UI, 공격 전후 결과, agent receipt, 사용자/수리사/기관 리포트 비교.

**진행이 막힌 경우의 독립 산출물**

- report schema validator, claim-evidence test set, fallback renderer, prompt/version receipt 중 하나를 완결한다.

### 11월 — 기관 운영과 현장 검증

**진입 조건**

- tenant 격리, 최소 consent/deletion, support path가 staging에서 검증됨
- 실증 범위·참여·기관 협의가 실제 정보로 확정됨

**11월 1차 / R13**

- 신규 기관 콘솔에 기기·수리·점검·위험·근거·익명 공간 집계를 연결한다.
- 기관별 Excel/PDF adapter를 schema mapping으로 분리한다.
- 사용자·수리사·담당자 pilot onboarding, 동의, 도움 요청, 탈퇴 흐름을 검증한다.

**11월 2차 / R14**

- mobile batch→object/receipt→projection→fact→report correlation을 관측한다.
- timeout, Storage/Firestore 실패, Rules/App Check 오설정, 모델/LLM 장애를 주입한다.
- 큰 글씨·스크린리더·터치 영역·권한 안내를 실제 기기에서 검토한다.

**월말 게이트**

- pilot 핵심 흐름과 graceful degradation이 문서화된다.
- 심각한 오류는 incident 정책과 실제 영향에 따라 기록된다.
- 발표 카드: 운영 대시보드, 장애 타임라인, 접근성 전후, 기관 출력 sample.

**진행이 막힌 경우의 독립 산출물**

- synthetic tenant onboarding, restore drill, accessibility audit, adapter contract test 중 하나를 완결한다.

### 12월 — 재현 가능한 최종 증거

**진입 조건**

- 평가 artifact version freeze와 공개/비공개 경계가 있음
- 데모는 synthetic 또는 적법한 비식별 데이터로 재현 가능함

**12월 1차 / R15**

- time-split 최종 평가, end-to-end 데모, 복구 drill을 고정 환경에서 재실행한다.
- 시스템·모델·agent·접근성·운영 KPI를 한 evidence matrix로 연결한다.
- 대표 성공뿐 아니라 실패·abstention·미완료를 확정한다.

**12월 2차 / R16**

- 기술백서, 아키텍처 포스터, 데이터/모델 카드, runbook, changelog를 완성한다.
- 5분 데모와 포트폴리오 설명을 실제 증거 ID에 연결한다.
- 운영 유지·중단·후속 실증과 보존·폐기 계획을 결정한다.

**월말 게이트**

- 새 환경에서 문서 명령으로 데모·핵심 평가를 재현할 수 있다.
- 계획·실제·실패·미완료가 구분되고 개인정보가 공개 artifact에 없다.
- 발표 카드: 5분 데모, 포스터, benchmark appendix, 계획 대비 실제 표.

**진행이 막힌 경우의 독립 산출물**

- clean-room replay, claim audit, 공개 artifact privacy scan, demo failure fallback 중 하나를 완결한다.

## 6. 정기리포트 매핑

| ID | 날짜 | 기술 판단 | 기준 문서 |
| --- | --- | --- | --- |
| R01 | 05-15 | greenfield·IP·범위 | [2026-05-15](../reports/fixed/2026-05-15.md) |
| R02 | 05-31 | 모바일 GPS·권한·event | [2026-05-31](../reports/fixed/2026-05-31.md) |
| R03 | 06-15 | background GPS·SQLite | [2026-06-15](../reports/fixed/2026-06-15.md) |
| R04 | 06-30 | offline sync·배터리 | [2026-06-30](../reports/fixed/2026-06-30.md) |
| R05 | 07-15 | Auth/App Check·ingest·멱등성 | [2026-07-15](../reports/fixed/2026-07-15.md) |
| R06 | 07-31 | Storage·projection·privacy·cost | [2026-07-31](../reports/fixed/2026-07-31.md) |
| R07 | 08-15 | label·baseline·PyTorch | [2026-08-15](../reports/fixed/2026-08-15.md) |
| R08 | 08-31 | ONNX·quantization·device | [2026-08-31](../reports/fixed/2026-08-31.md) |
| R09 | 09-15 | event projection·import | [2026-09-15](../reports/fixed/2026-09-15.md) |
| R10 | 09-30 | survival·time split | [2026-09-30](../reports/fixed/2026-09-30.md) |
| R11 | 10-15 | calibration·abstention | [2026-10-15](../reports/fixed/2026-10-15.md) |
| R12 | 10-31 | fact·agent·validator | [2026-10-31](../reports/fixed/2026-10-31.md) |
| R13 | 11-15 | console·adapter·pilot | [2026-11-15](../reports/fixed/2026-11-15.md) |
| R14 | 11-30 | observability·security·accessibility | [2026-11-30](../reports/fixed/2026-11-30.md) |
| R15 | 12-15 | final evaluation·demo | [2026-12-15](../reports/fixed/2026-12-15.md) |
| R16 | 12-31 | whitepaper·portfolio·handoff | [2026-12-31](../reports/fixed/2026-12-31.md) |

## 7. 완료 정의

### 코드

- contract/권한/실패 경로 test가 있고 lint·typecheck·test가 clean runner에서 통과한다.
- 비밀·PII·원본 좌표가 Git과 일반 로그에 없다.
- runtime wiring과 production adapter가 없으면 구현 완료가 아닌 unit로 표시한다.

### 모바일

- Android/iPhone 장비·OS·앱 build·권한 상태를 기록한다.
- foreground, background, 화면 잠금, 강제종료, 재시작을 구분한다.
- static export를 실기기 background 동작 증거로 사용하지 않는다.

### 데이터·모델

- input manifest, schema/feature/model version, split, baseline, 실패 사례가 있다.
- synthetic·legacy·field 데이터를 구분한다.
- 성능이 낮거나 표본이 부족할 때 유보 조건을 적용한다.

### 제품·보고

- 검증된 변화는 Product Update와 Evidence ID를 가진다.
- 미래 계획, 실제 성취, 사람 검토, 실제 회의 정보를 분리한다.
- 발표 화면의 숫자는 계산식·기간·모수·version으로 추적된다.

## 8. 일정 변경과 의사결정

- 1주 이내 순서 변경은 정기리포트의 `계획과의 차이`에 기록한다.
- 월별 게이트, 데이터 저장소, 권한, 모델 역할을 바꾸면 ADR을 작성한다.
- 현장 데이터·외부 협의가 지연되면 synthetic/emulator 기술 산출물로 전환하되 field 성과로 표현하지 않는다.
- 필수 게이트가 미완료이면 다음 달 프로토타입은 진행할 수 있지만 production 승격은 차단한다.
- 계획에서 제거한 항목은 `cancelled` 또는 `superseded`와 이유를 남긴다.
