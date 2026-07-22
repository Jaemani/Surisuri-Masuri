# 프로젝트 문서 인덱스

이 문서는 Mobility Reliability Platform의 문서 진입점이자 기준 문서 지도다. 같은 사실을 여러 문서에 복제하지 않고, 질문별 원문과 현재 상태를 연결한다.

## 1. 프로젝트를 처음 읽는 순서

1. [프로젝트 상세 개요](./PROJECT_OVERVIEW.md) — 배경, 사용자, 제품 흐름, 기술 목표, 현재 사실
2. [프로젝트 헌장](./PROJECT_CHARTER.md) — 승인된 범위, 비목표, 성공 조건, 재사용 경계
3. [시스템 아키텍처](./architecture/SYSTEM_ARCHITECTURE.md) — 런타임 경계와 신뢰 경계
4. [8개월 로드맵](./ROADMAP.md) — 2026년 5월~12월 월별 기술 게이트
5. [마스터 실행계획](./plans/MASTER_EXECUTION_PLAN.md) — 작업흐름, 의존성, 반월 단위 실행과 완료 조건
6. [검증·증거 계획](./plans/VALIDATION_AND_EVIDENCE_PLAN.md) — 테스트 수준, KPI, 실기기·현장 증거 규칙
7. [Telemetry 복구 실행계획](./plans/TELEMETRY_RECOVERY_PLAN.md) — lease, fencing, sweeper, generation-pinned recovery
8. [정기보고·회의 운영계획](./plans/REPORTING_AND_MEETING_PLAN.md) — 월 2회, 총 16회 계획 보고와 실제 기록 방법
9. [리포트 운영 인덱스](./reports/README.md) — fixed 사전작성본과 human 요청 리포트의 상태·발행 경계
10. [데이터·ML·AI 실행계획](./plans/DATA_ML_AI_PLAN.md) — 데이터 계보, 두 모델, 근거형 보고서 에이전트
11. [릴리스·파일럿·최종 데모 계획](./plans/RELEASE_PILOT_DEMO_PLAN.md) — 환경 승격, 현장 도입, 발표·포트폴리오 증거
12. [위험 등록부](./plans/RISK_REGISTER.md) — 현재 위험, 탐지 신호, 대응과 차단 조건

## 2. 질문별 기준 문서

| 질문 | 기준 문서 | 보조 문서 |
| --- | --- | --- |
| 왜 새로 만드는가 | [상세 개요](./PROJECT_OVERVIEW.md) | [ADR-0001](./decisions/ADR-0001-greenfield-boundary.md) |
| 무엇을 만들고 무엇을 만들지 않는가 | [프로젝트 헌장](./PROJECT_CHARTER.md) | [로드맵](./ROADMAP.md) |
| 5~12월에 언제 무엇을 판단하는가 | [마스터 실행계획](./plans/MASTER_EXECUTION_PLAN.md) | [정기리포트 16개](./reports/fixed/README.md) |
| Firebase, Go, domain command와 worker의 책임은 무엇인가 | [시스템 아키텍처](./architecture/SYSTEM_ARCHITECTURE.md) | [ADR-0007](./decisions/ADR-0007-firebase-first-hybrid.md), [ADR-0011](./decisions/ADR-0011-domain-command-worker-boundaries.md) |
| pending receipt를 누가 어떻게 복구하는가 | [Telemetry 복구 실행계획](./plans/TELEMETRY_RECOVERY_PLAN.md) | [ADR-0017](./decisions/ADR-0017-fenced-ingest-recovery.md), [ADR-0018](./decisions/ADR-0018-generation-pinned-read-only-classifier.md), [ADR-0019](./decisions/ADR-0019-current-forward-recovery-authorization.md), [ADR-0020](./decisions/ADR-0020-two-pass-forward-reconciliation.md), [ADR-0021](./decisions/ADR-0021-bounded-forward-recovery-worker.md), [ADR-0022](./decisions/ADR-0022-atomic-cleanup-transition-attempt-closure.md), [ADR-0023](./decisions/ADR-0023-fenced-cleanup-lease-claim.md), [ADR-0024](./decisions/ADR-0024-immutable-cleanup-dry-run-target.md), [ADR-0025](./decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md), [ADR-0026](./decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md), [ADR-0027](./decisions/ADR-0027-paired-read-only-cleanup-absence-attestation.md), [ADR-0028](./decisions/ADR-0028-progress-aware-expired-cleanup-takeover.md), [ADR-0029](./decisions/ADR-0029-durable-artifact-phase-cleanup-execution.md), [ADR-0030](./decisions/ADR-0030-atomic-cleanup-expiry-finalization.md), [ADR-0031](./decisions/ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md), [forward path 리포트](./reports/human/HR-20260721-09-fenced-forward-admission.md), [claim·cleanup transition 리포트](./reports/human/HR-20260721-10-recovery-claims-cleanup-transition.md), [action outcome·attempt failure 리포트](./reports/human/HR-20260722-17-forward-recovery-outcome.md), [authorization disposition 리포트](./reports/human/HR-20260722-18-authorization-disposition.md), [bounded worker 리포트](./reports/human/HR-20260722-20-bounded-forward-recovery-worker.md), [cleanup attempt closure 리포트](./reports/human/HR-20260722-21-cleanup-transition-attempt-closure.md), [cleanup lease 리포트](./reports/human/HR-20260722-22-fenced-cleanup-lease-claim.md), [cleanup target 리포트](./reports/human/HR-20260722-23-immutable-cleanup-dry-run-target.md), [cleanup delete 리포트](./reports/human/HR-20260722-24-generation-pinned-cleanup-delete.md), [cleanup ledger 리포트](./reports/human/HR-20260722-25-cleanup-execution-ledger.md), [signed absence 리포트](./reports/human/HR-20260722-26-signed-cleanup-absence-audit.md), [cleanup takeover 리포트](./reports/human/HR-20260722-27-progress-aware-cleanup-takeover.md), [durable phase execution 리포트](./reports/human/HR-20260722-28-durable-cleanup-phase-execution.md), [cleanup expiry finalization 리포트](./reports/human/HR-20260722-29-atomic-cleanup-expiry-finalization.md), [cleanup retry·hold 리포트](./reports/human/HR-20260723-30-cleanup-retry-hold-disposition.md), [ADR-0016](./decisions/ADR-0016-immutable-telemetry-artifact-lineage.md) |
| 데이터 구조와 이관 기준은 무엇인가 | [Target Domain Model](./data/TARGET_DOMAIN_MODEL.md) | [Legacy Inventory](./data/LEGACY_DATA_INVENTORY.md), [Migration Gates](./data/MIGRATION_GATES.md) |
| ML·AI가 무엇을 판단하고 무엇을 하지 않는가 | [데이터·ML·AI 계획](./plans/DATA_ML_AI_PLAN.md) | [ADR-0006](./decisions/ADR-0006-model-and-llm-responsibility.md) |
| 어떤 결과를 완료로 인정하는가 | [검증·증거 계획](./plans/VALIDATION_AND_EVIDENCE_PLAN.md) | [증거 인덱스](./evidence/README.md) |
| 정기보고와 회의록을 어떻게 작성하는가 | [보고·회의 계획](./plans/REPORTING_AND_MEETING_PLAN.md) | [리포트 운영 인덱스](./reports/README.md), [문서 운영 정책](./DOCUMENTATION_POLICY.md) |
| 실제 제품에서 무엇이 바뀌었는가 | [제품 업데이트](./product-updates/README.md) | [월별 증거](./evidence/2026-07.md) |
| 심각한 장애가 있었는가 | [인시던트 정책](./incidents/README.md) | 해당 `INC-*` 문서 |
| WSL과 실기기에서 어떻게 실행하는가 | [WSL Runbook](./development/WSL_RUNBOOK.md) | 앱·서비스별 README |
| telemetry orphan·stale receipt를 어떻게 분류하는가 | [Reconciliation Runbook](./development/TELEMETRY_RECONCILIATION_RUNBOOK.md) | [ADR-0017](./decisions/ADR-0017-fenced-ingest-recovery.md) |

## 3. 계획과 실제의 분리

다음 세 층을 섞지 않는다.

| 층 | 의미 | 기록 위치 |
| --- | --- | --- |
| 기준 계획 | 8개월 동안 검토할 순서와 기대 산출물 | `ROADMAP.md`, `plans/`, `reports/fixed/` |
| 실제 변경 | 코드·제품에서 검증된 변화 | `product-updates/`, 커밋, 배포 기록 |
| 실제 증거 | 테스트·화면·측정·사람 확인 결과 | `evidence/`, `reports/human/`, 필요 시 `incidents/` |

계획이 실제보다 앞서거나 뒤처져도 계획 문서를 성취 기록처럼 고쳐 쓰지 않는다. 차이는 해당 회차 정기리포트의 `실제 진행 입력란`과 증거 링크에 남긴다.

## 4. 2026-07-23 현재 검증된 구현 경계

다음은 문서 작성 시점에 로컬·클린 러너 증거가 연결된 범위다.

- 신규 monorepo, 문서 스트림, 계약·Firebase Rules 테스트 기반
- React Native 앱의 foreground 위치 수집 코드와 SQLite outbox 구현, JS 정적 export·순수 policy 검증. native SQLite/GPS callback과 실기기 동작은 미검증
- `telemetry-batch.v2` 계약과 raw telemetry에서 Firebase UID를 분리한 identity 경계
- Go telemetry ingest kernel의 strict decode, 멱등성·receipt·object 저장 인터페이스, fail-closed HTTP 경계
- Firebase Admin SDK dual-token verifier·App ID allowlist·production emulator guard factory의 local synthetic 검증. executable에는 미연결
- active tenant·beneficiary·installation·trip·assignment·current consent를 교차 검사하는 pure authorization policy와 Firestore exact-read adapter의 local synthetic 검증. executable에는 미연결
- 위 authorization을 replay·conflict 조회보다 먼저 재평가하고 두 uniqueness index와 최초 receipt를 같은 Firestore transaction에서 생성하는 admission adapter의 local fake-seam 및 Firestore Emulator concurrent same-batch 검증. ADC/IAM·production은 미검증이며 executable에는 미연결
- raw deterministic gzip과 canonical manifest를 `DoesNotExist`로 저장하고 exact hash·CRC·size·generation을 Firestore receipt에 고정하는 artifact adapter/finalizer의 local race·official testbench 검증. staging IAM·lifecycle·runtime은 미검증
- ADR-0017 recovery 중 R1 immutable reservation input, R2 lease/fence contract와 R3의 최초 lease·active replay 차단·expired takeover·fenced `MarkStored`/`MarkRejected`·safe release forward path는 local unit·Firestore Emulator와 clean CI 범위에서 구현·검증됐다. [EVD-20260721-017](./evidence/2026-07.md)은 `verified`이지만 staging·runtime 증거는 아니다.
- 다음 증분의 `RenewLease`, sweeper 전용 `ClaimRecoveryLease`, HTTP/sweeper takeover와 같은 transaction의 `started` attempt+count, reserved-origin `BeginCleanupTransition`, replay authorization 재평가·read-time coherence, revision/clock/invariant fail-closed와 Emulator 경쟁 test가 local·clean CI에서 검증됐다. [EVD-20260721-018](./evidence/2026-07.md)은 `verified`이지만 staging·runtime 증거는 아니다. 해당 증거 시점의 6분 grace는 provisional이었고 현재 정책은 아래 ADR-0023 증분이 대체한다.
- ADR-0018 R5의 provider-neutral classification request/result/inventory 계약, purpose별 opaque read grant와 strict manifest shape decoder는 WSL2 Docker의 local synthetic gate와 clean CI에서 구현·검증됐다. [EVD-20260721-019](./evidence/2026-07.md)은 `verified`이지만 provider·staging 증거는 아니다. current authorizer, final classification orchestration과 runtime 연결은 미구현이다.
- Forward recovery의 current-state 권한 경계는 [ADR-0019](./decisions/ADR-0019-current-forward-recovery-authorization.md)에 따라 구현됐다. Authoritative receipt에서 request를 만들고 같은 Firestore transaction snapshot의 tenant·beneficiary membership·installation·trip·assignment·동의를 검증한 뒤 30초 이하 opaque grant를 발급한다. Claim 뒤 current consent withdrawal denial을 포함한 local/Emulator 근거는 [EVD-20260721-024](./evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization)이며 runtime·staging 증거는 아니다.
- [ADR-0020](./decisions/ADR-0020-two-pass-forward-reconciliation.md)의 phase-aware planner와 manifest-only repair 경계도 구현됐다. Pass-1 `valid_raw_only` private evidence와 fresh current request가 같을 때만 short-lived write capability를 발급하고, GCS adapter는 raw surface 없이 canonical manifest만 `DoesNotExist` create한다. Unit-level exact replay, deadline/cancel·renewal·consent withdrawal은 [EVD-20260722-025](./evidence/2026-07.md#evd-20260722-025--two-pass-forward-recovery-planner와-manifest-only-repair-boundary)에서 확인했다. Staging GCS soft-delete/version 의미와 runtime은 아직 미검증이다.
- 같은 R6의 Firestore terminal action도 구현됐다. stored·rejected·hold·release는 receipt와 exact started attempt를 한 transaction에서 완료하며, bounded attempt-only failure, expired prior attempt closure와 commit-response loss용 fresh outcome capability가 exact fence·revision·action hash·lineage를 다시 검증한다. Local/Emulator/clean CI 근거는 [EVD-20260722-026](./evidence/2026-07.md#evd-20260722-026--forward-recovery-action-outcome과-attempt-failure-원자-경계)이며 startup·worker·scheduler·staging 증거는 아니다.
- Current authorization denied/unavailable disposition도 별도 capability로 구현됐다. Denied hold와 readable-malformed unavailable release는 transaction 안에서 exact receipt·fence·attempt·current relation을 다시 평가하고 receipt+attempt를 원자 commit하며, decision-domain-bound fresh outcome이 응답 유실을 read-only로 상관한다. [EVD-20260722-027](./evidence/2026-07.md#evd-20260722-027--current-authorization-disposition-원자-경계)의 local/Emulator 근거이며 reconciler runtime·staging 증거는 아니다.
- 위 R6 component를 이미 claim된 receipt 하나의 bounded protocol로 합성하는 reconciler도 구현됐다. Complete/raw-only 2-pass, renewal hard epoch, 5초 outcome tail, NotCommitted failure barrier·old-query late-commit 재조회와 cancellation finalizer가 local test로 고정됐고 Firestore control adapter conformance도 확인했다. [EVD-20260722-028](./evidence/2026-07.md#evd-20260722-028--bounded-forward-reconciler-composition)의 local/Emulator/testbench 근거이며 candidate worker·startup·staging 증거는 아니다.
- [ADR-0021](./decisions/ADR-0021-bounded-forward-recovery-worker.md)의 tenant-scoped due query, `(next_recovery_at, document_id)` cursor, 고정 scan cutoff epoch와 advisory CAS checkpoint, fresh transactional claim 뒤 R6 handoff를 합성한 bounded outer worker component도 구현됐다. Malformed advisory candidate·claim unknown·item panic을 격리하고 page/item/run budget과 fixed-cardinality observation을 둔다. [EVD-20260722-029](./evidence/2026-07.md#evd-20260722-029--bounded-forward-recovery-worker와-cross-run-checkpoint)의 local/Emulator 증거이며 startup·scheduler·metrics exporter·staging index/IAM을 연결한 runtime 증거는 아니다.
- [ADR-0022](./decisions/ADR-0022-atomic-cleanup-transition-attempt-closure.md)에 따라 reserved expiry cleanup은 만료된 forward lease의 exact prior attempt를 transaction에서 함께 읽는다. `started`이면 `failed/lease_expired`로 닫고 receipt를 `cleanup_pending`으로 함께 commit하며, missing·foreign token·completed attempt는 write 0으로 거부한다. Application·receipt·attempt clock 전체의 최솟값으로 조기 transition을 차단하는 local/Emulator 근거는 [EVD-20260722-030](./evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료)이다.
- [ADR-0023](./decisions/ADR-0023-fenced-cleanup-lease-claim.md)의 immutable transition metadata, `11m > max lease 5m + complete StoreBatch 5m` quiet policy와 cleanup-only fenced claim도 구현됐다. First claim과 expired takeover는 cleanup `started` attempt를 transaction으로 만들며 concurrent winner가 1명으로 수렴한다. GCS adapter는 cancellation 뒤 추가 create와 trusted late-success를 차단한다. [EVD-20260722-031](./evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim)의 local/Emulator/testbench/clean CI 근거다. Cleanup lease 자체는 artifact read·target create·delete 권한이 아니다.
- [ADR-0024](./decisions/ADR-0024-immutable-cleanup-dry-run-target.md)의 `cleanup_dry_run` purpose와 별도 issuer/fence, current receipt·exact started attempt 재검증, full classification evidence seal과 create-once Firestore target도 구현됐다. Concurrent same command는 target 1개와 created/replayed 각 1개로 수렴하고 conflicting target은 receipt·attempt를 포함해 write 0이다. Client direct read/write deny와 target 생성까지의 local/Emulator 근거는 [EVD-20260722-032](./evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target)에 기록한다.
- [ADR-0025](./decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md)의 concrete Firestore delete grant와 generation+metageneration-pinned GCS executor도 local component로 구현됐다. Raw-first complete-empty audit, missing counterpart path audit, soft-deleted/late generation과 incomplete inventory fail-closed, delete/inspect 404·timeout·permission·quota·412 분리를 [EVD-20260722-033](./evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit)에서 local/Emulator/pinned testbench/clean CI로 확인했다. 해당 증거의 full success observation은 non-authoritative shape이며, durable signed persistence의 후속 현재 상태는 아래 ADR-0027 증분에 분리한다.
- [ADR-0026](./decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)의 pure execution ledger와 Firestore persistence foundation도 구현됐다. Canonical plan과 target/fence/receipt revision을 exact cleanup attempt에 고정하고 pure domain에서 전체 dispatch·delete outcome·audit phase/revision을 검증한다. [EVD-20260722-034](./evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence)는 planned와 non-audit dispatch/delete outcome progress, generic API의 absence phase 차단까지의 역사적 증분을 기록한다.
- [ADR-0027](./decisions/ADR-0027-paired-read-only-cleanup-absence-attestation.md)의 fresh read-only audit authorization, paired GCS auditor의 Ed25519 opaque evidence와 Firestore raw·manifest absence phase persistence도 local component로 구현됐다. Exact replay는 `AuditedAt`과 attempt `UpdateTime`까지 write 0이고 wrong key·binding, stale receipt/fence/ledger drift는 control write 없이 거부된다. [EVD-20260722-035](./evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence)의 local/Emulator/clean CI 근거다. Regular generation과 soft-deleted generation listing은 순차 관측이므로 atomic snapshot이 아니며 staging IAM/write exclusion 전에는 production readiness가 아니다. 이 증분 당시 남았던 phase executor는 아래 ADR-0029에서, local success-only finalizer와 response-loss correlation은 ADR-0030에서 구현했다.
- [ADR-0028](./decisions/ADR-0028-progress-aware-expired-cleanup-takeover.md)의 progress-aware expired cleanup takeover도 local component로 구현됐다. Historical binding과 live authority를 분리하고 old ledger를 phase persisted time에서 검증한 뒤 prior progress를 보존한 `failed/lease_expired` closure, receipt fence·revision·attempt count +1과 pristine new attempt create를 한 transaction으로 commit한다. [EVD-20260722-036](./evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover)의 local/Emulator/clean CI 근거이며 old target·outcome·absence는 새 fence에 상속되지 않는다. 이 증분 이후 artifact phase executor는 아래 ADR-0029에서, local success-only finalizer와 response-loss correlation은 ADR-0030에서 구현했다.
- [ADR-0029](./decisions/ADR-0029-durable-artifact-phase-cleanup-execution.md)의 R8g durable artifact-phase executor도 local component로 구현됐다. Firestore dispatch를 provider mutation보다 먼저 commit하고 `applied` winner만 single-artifact grant를 받으며, known outcome만 paired signed absence audit로 전진한다. Replay dispatch와 durable `unknown`은 각각 `dispatch_pending`과 안전 정지로 수렴하고 raw absence 전 manifest 호출은 0이다. [EVD-20260722-037](./evidence/2026-07.md#evd-20260722-037--durable-artifact-phase-cleanup-execution)의 local/Emulator/testbench/clean CI 근거이며 성공 종점은 `ready_for_finalization`이다. 이 terminal success 경계의 후속 현재 상태는 아래 ADR-0030에 분리한다.
- [ADR-0030](./decisions/ADR-0030-atomic-cleanup-expiry-finalization.md)의 R8h local success-only finalizer와 read-only response-loss correlation도 구현됐다. Exact `manifest_absence_confirmed/revision 7`만 attempt `completed`, receipt `expired`, 두 uniqueness index의 동일 `purge_eligible_at`으로 한 transaction에서 전이하고 immutable cleanup target은 write하지 않는다. Commit 응답 유실 뒤에는 mutation을 반복하지 않고 `committed|not_committed|unverifiable`을 fresh read로 구분한다. [EVD-20260722-038](./evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation)의 local unit·Firestore Emulator 근거이며 사람 대상 범위는 [HR-20260722-29](./reports/human/HR-20260722-29-atomic-cleanup-expiry-finalization.md)에 기록한다.
- [ADR-0031](./decisions/ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)의 R8i local failure control도 구현됐다. Durable unknown error class와 10-class exhaustive policy, allowed phase/revision을 보존한 `cleanup_retry|cleanup_hold`, attempt+receipt 2문서 commit, immutable target·두 index write 0, old-fence-bounded retry claim·pristine attempt와 hold auto-claim 0을 local race와 Firebase demo Emulator에서 확인했다. 실제 commit 뒤 응답 유실도 보존된 query로 `committed`를 read-only 재확인한다. [EVD-20260723-039](./evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition)와 [HR-20260723-30](./reports/human/HR-20260723-30-cleanup-retry-hold-disposition.md)에 범위를 기록한다. Phase executor composition, operator hold release, accepted/held/rejected cleanup, nested purge와 runtime·scheduler·staging/production 연결은 계속 미구현·미연결이다.
- HTTP-only GCS reader, 분리된 `Versions`/`SoftDeleted` exact-path inventory, bounded `limit+1` 관찰, generation+metageneration read precondition, raw compressed-byte flag와 typed provider error 경계가 `main`에 구현됐다. local synthetic 계약은 [EVD-20260721-020](./evidence/2026-07.md), exact raw·manifest HTTP read와 metageneration precondition의 pinned official testbench 결과는 [EVD-20260721-021](./evidence/2026-07.md)에서 `verified`됐다. version·soft-delete staging 의미와 runtime은 미검증이다.
- strict canonical manifest·receipt cross-lineage, exact compressed digest, 2MiB bounded single-stream gzip, strict telemetry v2 payload와 explicit validator·codec registry가 pure content boundary로 구현돼 전체 local gate·독립 재리뷰·clean CI를 통과했다. [EVD-20260721-022](./evidence/2026-07.md)은 `verified`이지만 GCS orchestration·열 classification·log privacy scan·runtime 증거는 아니다.
- server-only current consent projection의 Firebase client direct read/write 차단
- Firestore client read를 own-person 또는 `case_worker`·`tenant_admin` 운영 범위로 제한하고 tenant/person query constraint를 고정한 local Rules Emulator 검증. production Rules에는 미배포
- adapter 미구성 상태에서 `/healthz=200`, `/readyz`와 ingest는 `503`

다음은 아직 운영 완료로 주장하지 않는다.

- background GPS 실기기 검증과 모바일 업로드
- 실제 Firebase ID token/App Check가 연결된 실행 경로
- production Firebase Rules 배포와 실제 mobile/admin query·index 검증
- production transaction·ADC/IAM, composite index `READY`, Cloud Storage lifecycle과 실제 receipt/object/manifest 복구 실행 검증. `status` 또는 `next_recovery_at`이 누락된 receipt는 due query에 보이지 않으므로 별도 bounded control-integrity audit도 필요
- 수리데이터 실제 이관, ML 학습, ONNX 배포, 생존분석
- 기관 콘솔, field pilot, 운영 SLO, AI report agent

최신 실제 상태는 [제품 업데이트](./product-updates/README.md)와 [증거 인덱스](./evidence/2026-07.md)를 우선한다.

## 5. 변경 규칙

- 범위 또는 성공 조건을 바꾸면 `PROJECT_CHARTER.md`와 ADR을 함께 갱신한다.
- 월별 순서를 바꾸면 `ROADMAP.md`와 `MASTER_EXECUTION_PLAN.md`의 차이를 기록한다.
- 저장·권한·신뢰 경계를 바꾸면 아키텍처, Target Domain Model, 관련 ADR을 갱신한다.
- 검증 기준을 바꾸면 Validation Plan에 이유와 적용 시작 버전을 남긴다.
- 미래 정기리포트의 계획 문구는 바꿀 수 있지만 실제 수행처럼 표현하지 않는다.
