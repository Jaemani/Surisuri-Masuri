# 프로젝트 상세 개요

## 1. 문서 상태

- 문서 성격: 프로젝트 정보 기준 문서
- 기준일: 2026-07-22
- 프로젝트 기간: 2026-05-01 ~ 2026-12-31
- 내부 개발명: Mobility Reliability Platform
- 외부 제품명: 상표 검토 전까지 미정
- 구현 책임: 1인 풀스택·모바일·데이터·ML 개발을 기준으로 계획하며 형식적 직책 분배를 기술 의존성으로 두지 않음

## 2. 프로젝트 계보와 신규 구축 경계

과거 프로젝트는 QR 기반 전동보장구 수리 이력, 사용자·수리사 화면, 복지관 관리자 기능, 지원금·수리 데이터 처리와 현장 연결 가능성을 탐색했다. 참고 경로는 다음과 같다.

- archive: [`techforimpact-archive/TFI_CAMPUS_25Spring_Soori-soori`](https://github.com/techforimpact-archive/TFI_CAMPUS_25Spring_Soori-soori)
- 로컬 참고 코드: `soo-ri`, `soo-ri-admin`, `power_assist_device_helper_backend`
- 사용 가능한 자산: 적법하게 사용할 수 있는 DB 형식, 수리데이터, 현장 업무 지식, 복지관 관계
- 사용하지 않는 자산: 기존 상표, UI, 배포 런타임, 기존 앱·백엔드 코드 의존성, 외부 IoT GPS 센서 경로

archive 경로는 사용자가 제공한 참조 식별자다. 2026-07-21 현재 이 개발 환경의 GitHub 인증으로 저장소 존재·접근 권한을 확인하지 못했으므로, 접근 가능성 자체를 신규 시스템의 요구사항이나 증거로 사용하지 않는다.

신규 제품은 과거 제품의 리팩터링이나 포크가 아니다. 과거 자산은 요구사항과 데이터 이관 입력으로만 사용하고, 모바일 앱·관리 콘솔·수집 서비스·데이터 계약을 새로 만든다.

과거 backend는 AWS+MongoDB에서 Firebase 중심 구조로 이전한 이력이 있지만, 그 Firebase 프로젝트와 Cloud Functions도 신규 runtime으로 재사용하지 않는다. `soo-ri`와 `soo-ri-admin` 역시 신규 제품에서는 실행하지 않고 구조·업무 참고로만 취급한다. 신규 Firebase-first 선택은 레거시 배포를 이어 쓰기 위한 선택이 아니라 비용·관리·App Check·Rules·모바일 통합을 고려한 별도 greenfield 결정이다.

### 제공된 과거 baseline과 검증 상태

아래 내용은 사용자 대화와 로컬 `attached` 발표·지원 자료에서 과거 성과로 제공된 주장이다. `attached`는 Git에 포함하지 않으며, 외부 발표에서 수치를 재사용하기 전 원문·페이지·데이터 manifest를 사람이 다시 확인한다.

| 제공된 baseline | 신규 계획에 주는 의미 | 현재 검증 상태 |
| --- | --- | --- |
| QR 기반 사용자·수리사·관리자 흐름 | QR은 기기 진입점으로 유지하되 신규 public code로 설계 | 과거 자료 주장, 신규 구현과 별개 |
| 실제 사용자 20명 수준의 현장 PoC | 복지관-first 운영과 낮은 입력 부담의 근거 후보 | 원문·기간·완료 기준 재확인 필요 |
| 약 5년치·550건 수리 데이터 분석 | 이관·범주 정규화·생존분석 후보 source | 실제 export가 작업공간에 없어 건수·품질 미검증 |
| 기관 양식 기반 Excel 출력 | canonical model + 기관별 adapter 요구 | 신규 adapter는 미구현 |
| 관리자 dashboard와 수리·지원금 관리 | 복지관 운영 과업의 참고 | 기존 코드·UI는 재사용하지 않음 |

이 표는 과거 주장을 신규 제품의 실제 성과로 승격하지 않는다.

## 3. 해결하려는 문제

전동보장구의 실제 사용량, 점검, 수리, 부품 교체 정보가 서로 연결되지 않아 다음 문제가 생긴다.

- 사용자는 고장 전 확인할 근거가 적고 반복 입력 부담이 크다.
- 수리사는 기기의 사용량과 과거 수리 맥락 없이 진단할 수 있다.
- 복지관은 서로 다른 양식과 수기·엑셀 중심 업무로 운영 근거를 만들기 어렵다.
- 행정·정책 담당자는 개인의 정밀 동선이 아니라 예산·점검·수리 패턴의 안전한 집계를 필요로 한다.

제품이 해결하려는 핵심은 “고장을 정확히 예언”하는 것이 아니다. 주행·수리·점검 사실을 연결하고, 데이터가 충분한 범위에서 점검 필요 위험과 그 근거를 제공하며, 부족한 경우 판단을 유보하는 것이다.

## 4. 한 문장 제품 정의

> 스마트폰 자체 GPS와 수리·점검 이벤트를 연결해 전동보장구의 상태를 재구성하고, 사용자·수리사·복지관에 근거와 불확실성이 표시된 예방점검 정보를 제공하는 신뢰성 관리 플랫폼

## 5. 주요 사용자와 핵심 과업

| 사용자 | 핵심 과업 | 제품 경험 | 다루지 않는 기대 |
| --- | --- | --- | --- |
| 사용자·보호자 | 주행 시작·종료, 기기 확인, 알림 확인, 점검 연결 | 큰 글씨, 최소 입력, 오프라인 보존, 이해 가능한 근거 | 앱이 안전을 보증하거나 고장을 확정하지 않음 |
| 수리사 | QR로 기기 확인, 수리·교체 기록, 모델 신호 검토 | 과거 이력·누적 사용량·유사 사례, 빠른 구조화 기록 | 모델이 전문 판단을 덮어쓰지 않음 |
| 복지관 담당자 | 사용자·기기·수리·점검·동의·실증 운영 | 기관별 권한, 익명 집계, 예외·미처리 항목, 양식 출력 | 개인 정밀 동선을 기본 제공하지 않음 |
| 정책·행정 담당자 | 예산·지원·점검 정책 검토 | 검증된 익명 집계와 데이터 품질·한계 | 개인 원자료나 근거 없는 절감 추정치를 제공하지 않음 |

### 도입 순서

1. **복지관**을 사용자·기기·수리사·행정 데이터가 만나는 운영 허브로 먼저 검증한다.
2. **수리사**는 실제 고장·교체 label과 전문 판단을 제공하는 Human-in-the-loop 검토자다.
3. **사용자·보호자**에게는 앱 사용을 강제하기보다 QR, 명확한 알림, 최소 입력과 지원 경로를 제공한다.
4. **지자체·정책 담당자**에게는 개인 원본이 아닌 검증된 익명 집계·예산·점검 지표를 제공한다.

복지관 연결은 제품 도입의 출발점이지 모든 기관에 동일한 양식을 강제한다는 뜻이 아니다. core model은 표준화하고 기관별 출력은 adapter로 분리한다.

## 6. 최종 사용자 흐름

1. 사용자가 앱에서 주행을 시작하고, OS 권한과 동의 상태가 확인된다.
2. 스마트폰 위치 sample이 로컬 SQLite event log에 저장된다.
3. 네트워크가 끊겨도 event는 남고, 재연결 후 immutable batch로 업로드된다.
4. Go gateway가 Firebase ID token, App Check, 기관 membership, 기기 배정, trip, 동의를 확인한다.
5. 원본 GPS batch는 압축해 Cloud Storage에 저장하고 Firestore에는 receipt와 작은 projection만 둔다.
6. 품질 파이프라인이 GPS 노이즈·비정상 이동·민감 시작·도착 지점을 처리한다.
7. 수리·점검·부품 교체 이벤트와 누적 주행량으로 기기와 부품의 현재 상태를 재구성한다.
8. 규칙 baseline과 모델이 점검 필요 위험을 계산하고 confidence·abstention을 함께 남긴다.
9. 수리사가 결과를 검토하고 새 판단을 이벤트로 기록한다.
10. 보고서 에이전트는 계산된 Fact ID만 사용해 대상별 설명을 만들고 validator가 근거를 검사한다.
11. 복지관은 개인 동선이 아닌 운영 상태와 익명 집계를 보고, 필요한 기관 양식으로 내보낸다.

## 7. 기술적으로 증명할 변화

단순 웹 CRUD와 LLM API 호출의 반복을 피하고 다음 기술 레이어를 직접 증명한다.

| 레이어 | 증명할 내용 | 남길 증거 |
| --- | --- | --- |
| 모바일 시스템 | Android/iOS 위치 권한, background lifecycle, SQLite outbox, 재시도 | 실기기 영상, 상태 전이표, 배터리·정확도 측정 |
| 신뢰성 있는 수집 | strict contract, dual token, tenant authorization, 멱등 receipt, immutable object | contract test, race test, replay/conflict 결과, trace |
| 공간·프라이버시 | GPS 필터, 시작·도착 마스킹, 최소 집계, 삭제 계보 | 전후 지도, 왜곡 지표, 삭제·접근 테스트 |
| 이벤트 기반 상태 | 수리·점검·부품·주행 event replay와 current projection | 타임라인, replay checksum, failure recovery |
| 직접 ML | PyTorch 시계열 품질 모델, baseline, ONNX·양자화 | dataset/model card, ablation, parity, device benchmark |
| 신뢰성 모델 | censoring을 포함한 time-to-inspection, calibration, abstention | 위험곡선, time-split 평가, 실패 분석 |
| AI 시스템 | Fact Store, claim-evidence validation, fallback, provenance receipt | 공격 평가셋, groundedness, 실행 receipt |
| 운영 | Firebase/GCP 비용 경계, observability, 장애 주입, 접근성·보안 | 대시보드, incident drill, 권한·접근성 결과 |

## 8. 기술·제품 구성

- 모바일: Expo/React Native, 필요 시 Swift·Kotlin native module, SQLite, ONNX Runtime
- 제어면: Firebase Auth, App Check, Firestore, FCM, Crashlytics, Remote Config, Hosting
- 수집면: Go Cloud Run telemetry gateway
- 도메인 명령면: session/trip, 수리·점검·동의 command를 처리하는 server-managed API
- 비동기 처리면: projection·importer·feature·fact·report worker와 DLQ
- 원본 데이터: Cloud Storage deterministic compressed batch
- 분석: 승인된 Storage manifest를 기본으로 사용하며 필요가 증명될 때만 BigQuery 활성화
- ML: Python/PyTorch, 재현 가능한 dataset/model manifest, ONNX 모바일 배포
- 콘솔: 신규 복지관 운영 웹 콘솔
- 보고서: 결정론적 Fact Store 위의 근거 제한형 LLM agent

PostgreSQL/PostGIS, Kafka, Kubernetes는 필요성이 측정되기 전에는 초기 의존성으로 두지 않는다.

## 9. 8개월 결과물 묶음

### 실제 제품

- Android/iOS 앱, 수리사 QR 흐름, 복지관 콘솔
- GPS 수집·동기화·정제·상태 projection 파이프라인
- 수리 이관 도구와 기관별 문서 adapter
- 데이터 품질·신뢰성 모델과 근거형 리포트

### 기술 증거

- ADR, 계약·Rules·race·실기기·부하·복구 테스트
- 데이터셋·모델·프라이버시 카드와 실패 분석
- 운영 대시보드, trace, 장애 훈련, 비용 경계
- 재현 가능한 명령과 artifact manifest

### 발표 결과

- 한 흐름으로 이어지는 5분 데모
- 원본/정제/마스킹 지도, offline recovery, 기기 타임라인, 위험곡선
- 근거를 열어볼 수 있는 대상별 리포트
- 아키텍처 포스터, 기술백서, 계획 대비 실제 표

## 10. 프로그램 운영 방식

- 매월 15일과 말일에 계획 기준 정기 기술리포트 1개씩, 총 16개를 발행한다.
- 보고일에는 실제 증거와 계획 차이를 갱신하며, 미수행 기능을 완료로 쓰지 않는다.
- 기술 선택 검토와 데모 확인을 실제 짧은 회의로 운영할 수 있지만, 참석자·일시·사진·지출은 실제 정보만 기록한다.
- 영업·행정 진척이 없는 기간에는 하나의 기술 가설을 실험해 영상·그래프·지도·테스트표 중 최소 하나를 남긴다.
- 자세한 방식은 [정기보고·회의 운영계획](./plans/REPORTING_AND_MEETING_PLAN.md)을 따른다.

## 11. 현재 실제 상태

2026-07-22 기준 신규 저장소 기반, Firebase Rules, foreground GPS/SQLite outbox 코드와 정적·순수 policy 검사, telemetry v2 계약, fail-closed Go ingest kernel까지 로컬·클린 러너 증거가 있다. 그 뒤의 telemetry authorization, atomic admission, immutable artifact 계보도 다음 범위에서만 검증됐다.

- active tenant·beneficiary·installation·trip·assignment·현재 동의를 함께 검사하는 authorization policy와 Firestore exact-read adapter는 [EVD-20260721-012](./evidence/2026-07.md#evd-20260721-012--firestore-텔레메트리-권한-snapshot)의 local synthetic test 범위에서 확인됐다. client용 Firestore read matrix는 [EVD-20260721-013](./evidence/2026-07.md#evd-20260721-013--firestore-client-최소권한-read-matrix)의 Rules Emulator에서 확인됐지만 production Rules 배포와 실제 앱 query는 미검증이다.
- authorization 재평가와 두 uniqueness index·최초 receipt 생성을 한 Firestore transaction에 묶은 admission adapter는 [EVD-20260721-014](./evidence/2026-07.md#evd-20260721-014--원자적-telemetry-admission과-receipt-lineage)의 local fake seam과 [EVD-20260721-015](./evidence/2026-07.md#evd-20260721-015--firestore-admission-transaction-emulator-integration)의 concurrent same-batch에서 확인됐다. production ADC/IAM과 실제 철회 transaction 경쟁은 미검증이다.
- deterministic gzip raw object, canonical manifest, exact hash·CRC·size·generation 계보와 Firestore finalizer는 [EVD-20260721-016](./evidence/2026-07.md#evd-20260721-016--immutable-telemetry-objectmanifest-lineage)의 local race test와 pinned official Storage testbench에서 확인됐다. staging bucket IAM·lifecycle·retention은 미검증이다.
- ADR-0017의 recovery 설계 중 R1 immutable reservation input, R2 lease/fence domain contract와 R3의 **최초 lease·active replay·expired takeover·fenced finalizer forward path**가 구현됐다. 최초 reservation은 lease와 함께 생성되고, active replay는 artifact 작업에 들어가지 않으며, 만료 lease takeover는 fencing token을 증가시킨다. `MarkStored`·`MarkRejected`와 safe release는 현재 owner/token/deadline과 receipt server read time을 확인한다. 이 범위는 [EVD-20260721-017](./evidence/2026-07.md)의 local unit·Firestore Emulator와 clean CI에서 `verified`됐지만 staging·runtime 운영 성과는 아니다.
- 다음 증분으로 `RenewLease`, sweeper 전용 `ClaimRecoveryLease`와 reserved-origin `BeginCleanupTransition`이 `main`에 구현됐다. HTTP replay takeover와 sweeper claim은 receipt token·revision을 증가시키는 transaction 안에서 `started` recovery attempt 생성과 attempt count 증가를 함께 commit하며, renew 대 takeover·동시 sweeper claim·deadline cleanup 대 recovery/finalizer 경쟁을 Firestore Emulator에서 재현한다. HTTP replay는 authorization snapshot과 receipt read time의 coherence를 확인하고 더 늦은 receipt server time으로 같은 snapshot을 다시 평가한다. forward acceptance에는 app/server 시각의 큰 값을, 조기 cleanup 방지에는 작은 값을 사용하며 극단값·허용 skew 초과·revision overflow는 mutation 없이 fail-closed한다. Reserved-origin `cleanup_pending` receipt의 accepted lineage field는 비어 있지만 Storage에는 partial artifact가 있을 수 있다. `expired`는 quiet period만으로 유효하지 않고 future delete completion 또는 version-aware verified-empty completion 뒤에만 허용된다. 이 범위는 [EVD-20260721-018](./evidence/2026-07.md)의 최신 전체 local gate와 clean CI에서 `verified`됐지만 staging·runtime 운영 성과는 아니다.
- R8 선행 검토에서 deadline cleanup이 forward lease를 제거하면서 exact `started` attempt를 영구 고아로 남길 수 있는 원장 결함을 확인했다. [ADR-0022](./decisions/ADR-0022-atomic-cleanup-transition-attempt-closure.md)에 따라 `BeginCleanupTransition`은 recovery attempt count가 있으면 nested attempt를 같은 transaction에서 읽고 exact started를 `failed/lease_expired`로 닫은 뒤 receipt를 전환한다. Missing·foreign-token·completed attempt는 receipt write도 0이며, app·receipt·attempt read clock 전체의 최솟값이 deadline 전이면 `not_ready`다. Local/Emulator 근거는 [EVD-20260722-030](./evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료)이다.
- 이어서 [ADR-0023](./decisions/ADR-0023-fenced-cleanup-lease-claim.md)의 R8a를 구현했다. Transition time·mode·origin·policy와 exact 11분 quiescence를 불변으로 고정하고, raw+manifest 전체 `StoreBatch`에 최대 5분 context를 강제했다. Quiet boundary 뒤 cleanup owner/version만 lease와 `started` attempt를 한 transaction으로 claim하며 expired takeover는 prior attempt를 `failed/lease_expired`로 닫는다. Concurrent first/takeover winner, three-clock, duplicate-attempt rollback과 GCS cancellation 경계의 local/Emulator/testbench 근거는 [EVD-20260722-031](./evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim)이다. Claim 자체는 artifact read·target create·delete 권한이 아니다.
- [ADR-0024](./decisions/ADR-0024-immutable-cleanup-dry-run-target.md)의 R8b도 구현했다. Cleanup 전용 read purpose와 exact fence·started attempt authorizer, request와 분류 결과 전체를 묶는 evidence seal, exact path·generation·hash와 inventory를 한 attempt ID에 create-once로 고정하는 Firestore target을 추가했다. Concurrent creator는 target 1개와 created/replayed 각 1개로 수렴하고 conflict는 receipt·attempt를 포함해 write 0이다. [EVD-20260722-032](./evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target)는 target 생성까지의 local/Emulator 근거이며 delete 자체의 증거는 아래 R8c와 분리한다.
- [ADR-0025](./decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md)의 R8c local executor도 구현했다. Concrete Firestore store만 current receipt·exact started attempt·persisted target을 다시 읽어 30초 이하 delete grant를 만들고, GCS adapter는 exact generation+metageneration conditional delete와 complete regular/soft-deleted empty audit만 수행한다. Raw unknown/error 뒤 manifest delete 0, soft-deleted/late generation·incomplete inventory fail-closed, inspect/delete 404 분리와 raw-only/manifest-only missing-path 감사를 고정했다. [EVD-20260722-033](./evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit)의 local/Emulator/pinned testbench/clean CI 근거이며 해당 시점 success observation은 completion capability가 아니다.
- [ADR-0026](./decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)의 pure execution ledger와 Firestore progress persistence foundation을 구현했다. Immutable target은 그대로 두고 exact cleanup attempt에 target/plan hash, fence와 receipt revision을 결박한다. Pure domain은 raw·manifest dispatch·delete outcome·audit phase를 단조적으로 검증하고, [EVD-20260722-034](./evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence)는 planned와 non-audit dispatch/delete outcome persistence 및 generic absence phase 차단까지의 증분을 기록한다.
- [ADR-0027](./decisions/ADR-0027-paired-read-only-cleanup-absence-attestation.md)의 R8e signed absence persistence도 구현했다. Firestore fresh current-state grant와 exact-path inventory-only GCS auditor를 분리하고, auditor 내부 Ed25519 private key가 request·concrete grant binding·artifact·`ObservedAt`에 결합한 opaque evidence만 paired verifier가 승인한다. Raw·manifest absence phase와 exact replay write-zero, wrong key/binding 및 stale receipt/fence/ledger drift write-zero는 [EVD-20260722-035](./evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence)의 local/Emulator/clean CI 범위다. Regular와 soft-deleted listing은 순차 provider call이므로 atomic snapshot이나 point-in-time proof가 아니다. Post-quiescence application fencing과 staging IAM/write exclusion을 검증하기 전 production readiness로 해석하지 않는다.
- [ADR-0028](./decisions/ADR-0028-progress-aware-expired-cleanup-takeover.md)의 R8f progress-aware expired cleanup takeover도 구현했다. Live provider authority와 historical binding reconstruction을 분리하고, old ledger를 7개 nonterminal phase의 마지막 persisted time에서 검증한다. Prior progress를 보존한 `failed/lease_expired` closure, receipt token·revision·attempt count +1과 pristine new attempt create는 같은 transaction으로 commit 또는 rollback되며 immutable target과 두 uniqueness index는 바뀌지 않는다. [EVD-20260722-036](./evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover)의 local/Emulator/clean CI 근거이고 old outcome·absence는 새 fence의 provider 권한이 아니다. Phase-specific executor, retry·hold, terminal completion·receipt `expired`·response-loss correlation·purge와 runtime은 아직 구현·연결하지 않았다.
- 다음 R5의 결정은 [ADR-0018](./decisions/ADR-0018-generation-pinned-read-only-classifier.md)에 고정했다. 그중 provider-neutral request/result/inventory 계약, forward와 accepted purpose별 request shape, request 전체에 결합된 opaque read grant, strict manifest decoder까지 local synthetic code로 구현했다. grant가 invalid 또는 만료되면 reader call 전에 닫히고, manifest decoder는 64KiB 상한·UTF-8·전체 duplicate key·unknown field·trailing JSON·version/content/timestamp shape를 검사한다. 이 범위는 [EVD-20260721-019](./evidence/2026-07.md)의 local·clean CI에서 `verified`됐다.
- 그 다음 HTTP-only GCS reader 증분도 `main`에 구현됐다. 일반 version과 soft-deleted generation을 exact `Prefix`·`MatchGlob` query로 분리하고, `limit+1` bounded inventory, exact generation+metageneration precondition, manifest/raw compressed flag 분리, `maxBytes+1` read와 direct/list 404·provider 오류의 typed fail-closed 경계를 둔다. local synthetic 계약은 [EVD-20260721-020](./evidence/2026-07.md), exact raw·manifest HTTP read와 metageneration precondition은 [EVD-20260721-021](./evidence/2026-07.md)의 pinned official Storage testbench에서 `verified`됐다. version·soft-delete staging 의미와 runtime은 미검증이다.
- 이어서 pure artifact content validator와 registry를 `main`에 구현했다. strict canonical manifest·receipt/accepted lineage, exact compressed SHA-256·CRC32C·size, 2MiB bounded single-stream gzip, decompressed body hash, strict telemetry v2 identity·count·time bounds와 literal codec golden을 독립 검증한다. duplicate validator version과 compressor drift는 추정 대체하지 않고 fail-closed하며 combined reason은 unavailable→metadata→manifest→raw 순서다. [EVD-20260721-022](./evidence/2026-07.md)은 local 전체 Go gate·독립 재리뷰·clean CI에서 `verified`됐지만 GCS orchestration·열 classification·log privacy scan·runtime 증거는 아니다.
- generation-pinned read-only classifier도 `main`에 구현했다. purpose-bound opaque grant와 forward fence의 만료를 모든 provider 경계에서 다시 확인하고, manifest/raw 각각의 complete inventory·pre-inspect·exact generation read·post-inspect를 거쳐 열 classification과 bounded reason으로 닫는다. accepted audit은 receipt-pinned generation만 권위값으로 사용하고 두 artifact가 모두 있으면 combined validator로 unavailable→metadata→manifest→raw precedence를 적용한다. [EVD-20260721-023](./evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier)은 WSL2 Docker 전체 gate, official Storage testbench, 독립 재리뷰와 clean CI에서 `verified`됐지만 current authorizer·worker·runtime wiring 또는 staging lifecycle 검증은 아니다.
- Current-state forward recovery authorization도 `main`에 구현했다. Claim을 artifact permission으로 간주하지 않고, authoritative receipt와 current tenant·beneficiary membership·installation·trip·assignment·precise-location consent를 같은 read-only Firestore transaction snapshot에서 확인한 뒤 `ingest` domain authorizer만 30초 이하 opaque grant를 발급한다. Claim 뒤 consent-state 철회 시 새 grant가 발급되지 않는 경로와 기존 admission 경쟁 suite를 Firestore Emulator race로 확인했다. [EVD-20260721-024](./evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization)의 local/Emulator 증거이며 startup·worker·Storage runtime과 staging IAM은 아직 미검증이다.
- 다음 R6의 설계는 [ADR-0020](./decisions/ADR-0020-two-pass-forward-reconciliation.md)에 고정했다. Phase-aware pure planner, pass-1 `valid_raw_only` evidence·exact raw pin·fresh current request에 결합된 manifest write capability와 raw surface가 없는 GCS `DoesNotExist` create가 `main`에 구현됐다. Unit-level exact replay, renewal·consent withdrawal, provider cancellation과 capability expiry 경쟁은 [EVD-20260722-025](./evidence/2026-07.md#evd-20260722-025--two-pass-forward-recovery-planner와-manifest-only-repair-boundary)의 local/testbench 범위에서 확인했다. Official testbench는 soft-delete inventory를 지원하지 않아 exact replay success의 staging GCS 검증이 남아 있다.
- R6의 Firestore terminal action·attempt ledger도 `main`에 구현됐다. stored·rejected·hold·release는 transaction 내부 current relationship·consent, exact receipt revision·fence와 started attempt를 다시 확인한 뒤 receipt action과 attempt completion을 원자 commit한다. 별도 opaque capability로 receipt를 건드리지 않는 bounded attempt failure를 기록하고, 다음 claim은 expired prior started attempt를 `lease_expired`로 닫는다. commit 응답 유실 뒤에는 exact prior fence·expected action hash·revision·lineage를 봉인한 read-only capability가 `committed|not_committed|unverifiable`을 구분하며 mutation을 replay하지 않는다. [EVD-20260722-026](./evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계)의 local/Firestore Emulator/clean CI 증거이고 runtime wiring·staging 증거는 아니다.
- Current authorization disposition 경계도 `main`에 구현됐다. Coherent current denial은 `recovery_hold/current_authorization_denied`, receipt·fence를 읽을 수 있지만 relation이 의미상 malformed인 상태는 `reserved/authorization_unavailable` release로만 처리한다. 별도 opaque capability와 `decision_domain=current_authorization` attempt marker를 사용하며 transaction 시점에 current 관계와 exact started attempt를 다시 평가한다. Denied/unavailable 상태 변화와 missing attempt는 write 0이고 fresh outcome read도 mutation 0이다. [EVD-20260722-027](./evidence/2026-07.md#evd-20260722-027--current-authorization-disposition-원자-경계)의 local/Firestore Emulator/clean CI 증거이며 runtime wiring·staging 증거는 아니다.
- R6 component를 이미 claim된 receipt 하나에 대해 bounded 순서로 실행하는 `ForwardRecoveryReconciler`도 `main`에 구현됐다. Production constructor는 private classifier·validator·authorizer를 내부 합성하고 Storage에는 manifest-only port만 허용한다. Complete/raw-only 2-pass, renewal hard epoch, operational/finalizer budget 분리, exact outcome과 attempt-failure barrier, late-commit old-query 재확인, caller cancellation failure/disposition을 12개 orchestration test로 고정했다. [EVD-20260722-028](./evidence/2026-07.md#evd-20260722-028--bounded-forward-reconciler-composition)의 local/Emulator/testbench/clean CI 근거이며 candidate query·claim outer worker, startup·scheduler·staging 증거는 아니다.
- R7의 bounded outer worker component도 `main`에 구현됐다. Tenant별 `reserved + next_recovery_at <= fixed cutoff` query를 `next_recovery_at, document ID` 순서로 page하고, advisory checkpoint에 cursor와 같은 scan cutoff를 CAS 저장한다. Candidate/checkpoint는 권한이 아니며 fresh `ClaimRecoveryLease`가 검증된 `Acquired`를 반환한 항목만 R6에 전달한다. Malformed advisory item, claim 응답 불명확, item 오류·panic은 다음 후보와 격리하고 page/item/run budget과 low-cardinality observation으로 닫는다. [ADR-0021](./decisions/ADR-0021-bounded-forward-recovery-worker.md)과 [EVD-20260722-029](./evidence/2026-07.md#evd-20260722-029--bounded-forward-recovery-worker와-cross-run-checkpoint)의 local/Firestore Emulator 범위이며 executable startup·scheduler·metrics exporter·staging index/IAM은 아직 연결·검증하지 않았다.

native SQLite/GPS callback과 실기기 동작은 아직 검증하지 않았고, production adapter가 executable에 연결되지 않아 gateway는 의도적으로 ingest를 받지 않는다. 현재 `/healthz` 외 readiness와 ingest는 계속 fail-closed여야 한다.

현재 상태를 다음 단계로 과장하지 않는다.

- background GPS와 Android/iPhone 실기기 결과는 아직 별도 증거가 필요하다.
- Firebase verifier 구현은 runtime wiring·실제 token 검증·Cloud Run 배포 전에는 운영 인증 완료가 아니다.
- recovery attempt의 started·completed·failed protocol, stale closure, authorization disposition과 이를 호출하는 bounded candidate worker component는 local/Emulator에서 구현됐다. Cleanup-only claim, reservation-expiry dry-run target, generation-pinned delete/audit, cleanup progress persistence와 paired signed read-only absence persistence에 더해 progress-bearing expired takeover도 local component로 구현됐다. Takeover는 old ledger를 마지막 persisted phase time에서 검증하고 prior progress를 보존한 채 새 fence의 pristine attempt를 원자 생성하며 old target·outcome·absence를 상속하지 않는다. Scheduler·startup·metrics exporter가 없어 실행 중인 background worker는 아니다. `BeginHeldCleanup`, `BeginAcceptedDeletion`, `BeginRejectedArtifactCleanup`, artifact별 phase executor, retry·hold, terminal completion·`expired`·response-loss correlation·purge 구현은 아직 남아 있다. GCS regular/soft-deleted sequential listing은 staging IAM writer-exclusion 검증 전에는 원자적 부재 증명이 아니다.
- generation-pinned classifier, current system forward recovery authorizer, denied/unavailable disposition, single-receipt reconciler와 bounded outer worker는 local/Emulator 범위에서 구현·검증됐다. 그러나 accepted-integrity authorizer와 production startup composition은 없다. `ClaimRecoveryLease`만 control-plane 처리 소유권을 부여하며 candidate query와 checkpoint는 권한이 아니다. Current authorizer 성공 없이 artifact read/write grant를 얻지 못한다. Staging에서 version·soft-delete·IAM·retention, composite index `READY`와 실제 authorization→Storage race를 검증하기 전에는 scheduler에 연결하거나 readiness를 열지 않는다.
- `status` 또는 `next_recovery_at`이 누락되거나 query 비호환 type인 receipt는 Firestore due query에 나타나지 않는다. R7 worker가 이를 자동 탐지한다고 주장하지 않으며 별도 bounded control-integrity audit 전에는 누락 탐지 coverage가 완전하지 않다.
- `worker_version`은 forward `telemetry-recovery.v1`과 cleanup claim `telemetry-cleanup.v1`을 owner kind별로 분리해 허용한다. Cleanup late-write grace 11분은 최대 lease 5분과 전체 initial `StoreBatch` 5분을 strict하게 덮는 local 실행 계약이지만, 승인된 보존기간·actual delete 정책이나 운영 SLO는 아니다.
- `ErrIngestInProgress`의 외부 HTTP status·retry 계약과 startup dependency wiring도 아직 고정되지 않았다.
- 수리 원본 export가 작업공간에 없으므로 실제 이관 품질과 모델 학습 가능성을 확정하지 않는다.
- 실증 인원, 비용 절감, 모델 정확도는 실제 측정 전 숫자를 채우지 않는다.

상세 실제 변경은 [제품 업데이트](./product-updates/README.md), 테스트 근거는 [2026년 7월 증거 인덱스](./evidence/2026-07.md)를 참조한다.

## 12. 확인이 필요한 외부 입력

- archive와 레거시 운영 데이터에 대한 실제 접근 권한·사용 범위
- 수리 export의 건수, 기간, 필드, 결측, 식별자 관계
- 복지관 pilot의 실제 기관·참여자·지원 절차와 개인정보 문서
- 운영 Firebase/GCP 프로젝트, budget, service account와 App Check 등록 앱
- 외부 발표에서 사용할 제품명과 상표 검토 결과

이 입력이 늦어져도 합성 fixture, Emulator, 실기기 자체 데이터로 시스템·계약·복구·보안 개발은 계속할 수 있다. 다만 현장·모델 성과로 바꾸어 주장할 수는 없다.
