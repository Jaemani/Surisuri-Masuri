# 릴리스·파일럿·최종 데모 계획

## 1. 목적

이 계획은 로컬 프로토타입을 실제 복지관 환경으로 승격하는 조건과 최종 발표에서 한 흐름으로 보여줄 결과를 정의한다. 기능 완성도와 현장 배포 권한을 분리하며, field 검증 전 수치나 효과를 만들지 않는다.

## 2. 환경과 승격 단계

| 단계 | 환경 | 데이터 | 목적 | 다음 단계 조건 |
| --- | --- | --- | --- | --- |
| E0 | WSL + unit/fixture | synthetic only | 계약·state·model·validator 개발 | clean runner 통과 |
| E1 | Firebase Emulator + Docker | synthetic only | Rules·transaction·retry·failure 통합 | allow/deny·reconciliation 통과 |
| E2 | dev device build | 개발자 자체 GPS | Android/iPhone lifecycle·offline·accessibility | 기기별 제한 문서화 |
| E3 | staging Firebase/GCP | synthetic + 승인된 비식별 sample | 실제 token/App Check, Storage, Cloud Run, trace, cost | 보안·삭제·복구 gate |
| E4 | controlled pilot | 동의된 field data | 제한 사용자·기관 운영 검증 | support·incident·exit criteria |
| E5 | final evidence freeze | 승인된 비식별 snapshot | 평가·데모·백서 재현 | claim-evidence 승인 |

production Firebase 프로젝트를 emulator 접속 문제의 임시 개발 환경으로 사용하지 않는다.

## 3. 릴리스 단위

### Mobile

- internal development build
- Android/iPhone device validation build
- controlled pilot build
- final demo build

각 build는 app version, native build ID, commit, Firebase project class, model version, Remote Config snapshot을 기록한다.

### Telemetry gateway

- immutable container image digest
- service account와 최소 IAM
- min instances 0, timeout/memory/concurrency 상한
- readiness는 verifier·authorizer·receipt·object adapter가 모두 초기화된 경우에만 200
- 실패 시 이전 revision으로 traffic rollback

### Domain Command API

- session/trip 시작·종료, 수리·점검·동의 command와 제한된 본인정보 조회를 담당
- telemetry gateway와 별도 배포·readiness·rollback 단위
- Firebase Functions v2 callable/HTTPS를 초기 후보로 두되 runtime은 [ADR-0011](../decisions/ADR-0011-domain-command-worker-boundaries.md)의 경계를 따름
- ID token, App Check, membership, role, assignment, purpose를 검사하고 immutable domain event를 생성

### Async workers

- projector, importer, feature, fact, report worker를 trigger·service account·version별로 기록
- Cloud Tasks/Pub/Sub retry, idempotency, checkpoint, DLQ와 replay side-effect 차단
- worker release 전 shadow projection checksum과 rollback/replay runbook 확인

### Firebase/GCP

- Rules/index/config 변경은 emulator test artifact와 함께 승격
- App Check enforcement는 등록 앱·debug provider·rollout 계획을 확인한 뒤 활성화
- Storage lifecycle·CORS·IAM·retention과 Firestore Rules를 별도 점검
- budget alert와 사용량 dashboard를 pilot 전에 활성화

### Model/agent

- model package와 threshold를 앱/service version에서 참조
- staged rollout과 rules fallback을 유지
- agent prompt/model/schema/validator version을 하나의 run receipt로 기록
- 모델·LLM 장애가 수집·수리 기록을 차단하지 않음

## 4. 승격 체크리스트

### E1 → E2

- [ ] PII 없는 fixture와 demo Firebase project ID만 사용
- [ ] mobile local migration·outbox recovery test 통과
- [ ] 실제 기기 로그에 좌표/token이 출력되지 않음
- [ ] WSL→Android/iPhone 네트워크 경로 문서화

### E2 → E3

- [ ] Android/iPhone 권한·background·offline 시나리오 결과 존재
- [ ] Firebase ID token + App Check + app allowlist 연결
- [ ] inactive membership·타 tenant·무효 consent가 write 전에 거부됨
- [ ] Firestore receipt·Storage object partial failure 복구
- [ ] 삭제·보존·로그 privacy scan 통과

### E3 → E4

- [ ] pilot 목적·동의·지원·탈퇴·삭제 문서 승인
- [ ] 최소 운영 dashboard와 alert owner·연락 경로
- [ ] 접근성 핵심 흐름과 QR fallback 확인
- [ ] 데이터 export/restore와 service rollback 연습
- [ ] 알려진 중대 위험과 graceful degradation을 담당자에게 전달
- [ ] 실제 pilot 기관·기간·참여 범위를 사람 검토로 확정

### E4 → E5

- [ ] 실제 참여·사용 기간·버전·결측을 reconciliation
- [ ] field와 synthetic 결과를 분리
- [ ] 모델·agent final evaluation snapshot freeze
- [ ] 실제 사용자·기관 정보 제거 또는 접근 제한
- [ ] 발표 claim별 Evidence ID와 limitation 승인

## 5. 파일럿 운영 흐름

### 준비

- 기관 tenant, 담당자 membership, 기기·사용자 연결을 server-managed 절차로 생성한다.
- QR은 추측 불가능한 public code만 포함하고 내부 path·UID·PII를 넣지 않는다.
- 사용자에게 위치 수집 목적, 시점, 철회, 보존, 도움 요청 방법을 낮은 인지 부담으로 안내한다.
- 수리사·복지관 담당자의 최소 교육 자료와 fallback 업무를 준비한다.

### 운영

- 앱 설치·로그인·동의·trip·offline sync·QR 수리·알림 과업 상태를 집계한다.
- 개인 정밀 경로를 일반 운영 화면에 노출하지 않는다.
- queue age, batch rejects, projection lag, model abstention, report failures, support 요청을 본다.
- 장애 시 위치 수집 중단, 서버 업로드 중단, 규칙 기반 보고서 등 최소 완화 모드를 적용한다.

### 종료

- 동의 철회, 앱 제거, raw location retention, 계정·기기 연결 해제 절차를 수행한다.
- 실제 count와 상태를 `accepted + rejected + pending`으로 reconciliation한다.
- 기술 오류, 사용성 문제, 운영 부담, 모델 한계를 분리해 정리한다.
- pilot 종료가 자동으로 production 일반 공개를 의미하지 않는다.

pilot 인원 목표는 공식 승인 문서와 실제 협의 값을 source로 확정한다. 기술 문서가 임의 숫자를 공식 KPI로 만들지 않으며, 최종 보고에는 실제 참여·완료·중도종료를 구분한다.

## 6. 운영·지원·인시던트

### 최소 SLI

- mobile unacked queue age
- upload accepted/replay/rejected rate
- auth/App Check/authorization failure class
- receipt reserved age와 object/receipt mismatch
- projector lag·DLQ
- model input missingness·abstention
- report schema/evidence validation failure
- notification delivery와 support backlog

### incident trigger

- 다른 tenant의 데이터 접근 또는 raw location 노출
- 설명되지 않은 위치 batch 손실·중복
- 동의 철회 후 수집 또는 삭제 실패
- 사용자를 오도할 수 있는 근거 없는 안전·고장 주장
- 서비스 전체 사용 불가 또는 장기 queue 적체

실제 사용자 영향이 없는 로컬 unit failure는 보통 Incident가 아니라 test/evidence다. 심각도 기준은 [인시던트 정책](../incidents/README.md)을 따른다.

## 7. 최종 5분 데모

### 데모 데이터

- 기본은 공개 가능한 synthetic persona·route·repair event를 사용한다.
- field 자료가 필요하면 동의·비식별·표본 기준을 검토하고 원본 좌표를 사용하지 않는다.
- 모든 수치의 dataset/model/app version을 고정한다.

### 시나리오

1. 사용자가 모바일 앱에서 trip을 시작한다.
2. 네트워크를 끊어도 sample과 event가 SQLite에 남는다.
3. 재연결하면 같은 immutable batch가 중복 없이 receipt를 받는다.
4. raw 경로가 품질 필터와 민감 위치 마스킹을 거쳐 trip summary가 된다.
5. QR로 수리·부품 교체 event를 기록하면 device twin이 replay된다.
6. 규칙과 신뢰성 모델이 점검 필요 위험·confidence·abstention을 계산한다.
7. 사용자 화면은 이유와 다음 행동을, 수리사 화면은 상세 fact를 보여준다.
8. AI report의 문장을 눌러 Fact ID와 계산 기준일을 연다.
9. 기관 콘솔은 개인 경로가 아닌 익명 집계·운영 상태·양식 출력을 보여준다.
10. 마지막에 대표 실패·fallback·현재 한계를 한 장으로 공개한다.

### 네트워크·외부 서비스 실패 대비

- prerecorded 30~60초 backup clip
- 고정 synthetic dataset과 local replay mode
- LLM 실패 시 deterministic report
- 지도 tile 실패 시 사전 생성된 비식별 경로 image
- 각 backup 사용 여부를 발표 후 기록

## 8. 발표 시각 자산

| 자산 | 보여주는 주장 | 생성 근거 |
| --- | --- | --- |
| 아키텍처 포스터 | 여러 신뢰 경계가 연결됨 | accepted ADR·runtime manifest |
| offline timeline | 단절 후 복구 | device evidence |
| raw/filtered/masked map | 품질과 privacy | versioned trace manifest |
| identity/failure matrix | fail-closed ingest | integration test |
| event twin timeline | 상태 replay | projection checksum |
| confusion/ablation | 직접 ML의 실질 이득 | frozen evaluation |
| survival/calibration curve | 위험·불확실성 | time-split dataset |
| claim→Fact UI | 근거형 agent | report run receipt |
| 운영 dashboard | field readiness | staging/pilot metrics |
| 계획 대비 실제 | 정직한 프로젝트 관리 | roadmap·updates·evidence |

## 9. 기술 포트폴리오 패키지

최종 설명은 “또 다른 모바일 앱”이 아니라 다음 시스템 경험을 실제 근거와 함께 보여준다.

- cross-platform native location lifecycle과 offline-first event sync
- Firebase/GCP control/data plane, dual-token trust, tenant authorization
- immutable batch·receipt·replay·recovery가 있는 telemetry pipeline
- event-sourced device/part reliability twin
- PyTorch→ONNX on-device model과 survival/calibration evaluation
- claim-evidence provenance와 fallback이 있는 report agent
- device/staging/field observability, privacy, accessibility, incident drill

포트폴리오 문장마다 공개 가능한 commit, benchmark, diagram, demo, failure analysis 중 하나를 연결한다. 실제 현장 사용자·기관 수는 승인된 관측값만 사용한다.

## 10. 종료·후속 결정

12월 말 다음을 각각 `continue`, `pilot-only`, `pause`, `retire`로 결정한다.

- 모바일 GPS 수집 profile
- telemetry raw retention과 분석 경로
- 품질·신뢰성 모델과 threshold
- report agent와 기관별 adapter
- pilot tenant와 지원 체계

운영을 멈추는 구성에는 FCM/App Check/Cloud Run/Storage/Firestore cleanup, 데이터 보존·삭제, credential 폐기, 사용자 안내 계획을 둔다.
