# 시스템 아키텍처

## 1. 목표

이 아키텍처는 모바일 위치 수집, 오프라인 동기화, 공간 데이터 처리, 기기 상태 복원, ML 평가, 근거형 보고서를 하나의 감사 가능한 흐름으로 연결한다.

## 2. 시스템 경계

```text
[Mobile App]
  React Native UI / Native location lifecycle
  SQLite event outbox / ONNX quality inference
          │
          ├── domain command ──> [Domain Command API]
          │                       session/trip, repair, inspection, consent
          │
          └── versioned batch ─> [Go Telemetry Gateway]
                                  server-derived idempotency / receipt
                     │
[Firebase / GCP Boundary]
  Firebase Auth + App Check + membership authorization
                     │
                     ├──> Cloud Storage: compressed raw GPS batches + lifecycle
                     └──> Firestore: domain event / receipt / metadata
                                      │
                                      └──> [Async Workers]
                                           receipt reconciler / expiry cleanup
                                           projector / importer / feature / fact / report
                                           │
                                           ├──> current device/part state
                                           └──> Storage or optional BigQuery
                    │
          ┌─────────┴─────────┐
          ▼                   ▼
[ML Service]            [Institution Console]
  training/evaluation     operational views
  model registry          reports/adapters
  calibrated inference    review/feedback
          │                   │
          └──────> [Report Agent] <──────┘
                     schema + fact validation
```

## 3. 런타임 책임

### Mobile

- 사용자가 명시적으로 시작한 주행 세션을 기본 단위로 수집한다.
- OS 권한과 lifecycle 변화를 이벤트로 남긴다.
- 서버 수신 확인 전까지 로컬 이벤트를 보관한다.
- 네트워크 재연결 시 제한된 batch로 재전송한다.
- ML 결과가 낮은 신뢰도이면 사용자 또는 후처리 검토 대상으로 보낸다.

### Telemetry Gateway

다음 목록은 target architecture다. Lease·fencing, single-receipt reconciler, bounded candidate/checkpoint와 expiry cleanup의 claim·read-only classification·dry-run target·generation-pinned delete/audit, execution progress ledger, paired signed absence persistence와 progress-aware expired takeover는 local code로 구현됐지만 `cmd/server`·scheduler에 연결되지 않았다. [ADR-0028](../decisions/ADR-0028-progress-aware-expired-cleanup-takeover.md)의 takeover는 old progress를 보존해 실패 종료하고 새 fence의 pristine attempt를 원자 생성할 뿐 provider mutation 권한을 상속하지 않는다. [ADR-0026](../decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)의 terminal completion·response-loss correlation과 artifact별 phase executor, retry·hold·purge는 아직 구현되지 않았다. [ADR-0027](../decisions/ADR-0027-paired-read-only-cleanup-absence-attestation.md)의 regular/soft-deleted inventory는 순차 관측이므로 atomic snapshot이 아니며 staging IAM writer-exclusion 전에는 production readiness가 아니다. Recovery 결정은 [ADR-0017](../decisions/ADR-0017-fenced-ingest-recovery.md), [ADR-0020](../decisions/ADR-0020-two-pass-forward-reconciliation.md), [ADR-0021](../decisions/ADR-0021-bounded-forward-recovery-worker.md), [ADR-0024](../decisions/ADR-0024-immutable-cleanup-dry-run-target.md), [ADR-0025](../decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md), [ADR-0026](../decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md), [ADR-0027](../decisions/ADR-0027-paired-read-only-cleanup-absence-attestation.md), [ADR-0028](../decisions/ADR-0028-progress-aware-expired-cleanup-takeover.md)을 따른다.

- 비즈니스 CRUD를 모두 담당하는 범용 백엔드가 아니다.
- 모바일 텔레메트리의 인증, 계약 검증, 멱등성, receipt만 책임진다.
- Firebase ID token과 App Check를 확인한 뒤 request의 `tenantId` 후보 scope를 active membership, 기기 배정, trip, installation, consent로 authorize한다.
- authorization exact read와 `ingestIdempotency`·`ingestClientBatches`·`ingestReceipts` 최초 생성을 하나의 Firestore transaction에 두며, retry callback마다 현재 권한을 다시 평가한다.
- `schemaVersion`, `tenantId`, `installationId`, `clientBatchId`로 서버가 파생한 같은 key와 동일 body는 기존 receipt를 반환한다.
- UUID·시각·body hash는 transaction callback 전에 한 번만 만들고, Cloud Storage write는 commit 성공 뒤에만 실행한다.
- 수집 시각과 기기 발생 시각을 분리한다.
- raw sample은 Firestore 개별 문서가 아니라 압축 Storage object로 저장한다.
- raw gzip과 canonical manifest는 각각 `DoesNotExist`로 생성하며 exact generation·SHA-256·CRC32C·size를 receipt에 고정한다.
- raw 성공 뒤 manifest/finalizer가 실패하면 overwrite하지 않고 동일 bytes를 generation-pinned read로 검증한 뒤 전진한다.
- 신규 reservation과 initial request lease는 같은 Firestore transaction에서 만들고, pending replay는 active lease가 있으면 Storage에 접근하지 않는다.
- lease takeover마다 receipt의 fencing token을 증가시키며 renew·release·stored/rejected finalizer는 현재 owner와 token이 일치할 때만 허용한다.
- reservation 처리 deadline, raw/manifest lifecycle expiry와 receipt purge 시점을 분리해 cleanup 근거가 artifact보다 먼저 사라지지 않게 한다. parent receipt 삭제 전 bounded purge job이 nested recovery attempt와 linked cleanup target·integrity finding을 먼저 제거하고, 마지막 transaction만 두 uniqueness index와 receipt를 함께 삭제한다.
- Cleanup lease를 Storage 권한으로 쓰지 않는다. Cleanup 전용 read grant가 exact receipt·started attempt·fence를 확인하고 classifier의 request와 mutable result 전체를 seal한 뒤, 별도 target-create grant가 exact generation dry-run target 하나만 만들 수 있다.
- Absence audit은 destructive grant와 다른 exact-path inventory-only grant를 사용한다. Paired GCS auditor만 private Ed25519 key를 소유해 request·concrete grant·artifact·관측시각에 결합된 evidence를 만들고 Firestore store는 public verifier와 fresh current transaction으로 raw·manifest audit phase만 저장한다.

### Domain Command API

- telemetry gateway와 분리된 control-plane API다.
- 인증된 session-start/stop을 통해 server UUIDv7 `tripId`를 발급하고 offline `clientSessionId`와 연결한다.
- 수리·점검·부품 교체·동의 revision·제한된 본인정보 조회 command를 처리한다.
- ID token, App Check, membership, role, 사람·기기 관계, 목적을 검사하고 immutable domain event를 생성한다.
- 초기 배포 후보는 Firebase Functions v2 callable/HTTPS이며 구현 전 별도 ADR에서 runtime과 공통 authorization policy 공유 방식을 확정한다.

### Async Workers

receipt reconciler의 bounded candidate/checkpoint component와 expiry cleanup delete/audit component는 현재 배포 worker가 아니다.

- Cloud Tasks/Pub/Sub의 at-least-once 전달을 전제로 projection, importer, feature, fact, report job을 처리한다.
- receipt reconciler는 stale `reserved` 후보를 bounded query로 찾되 Firestore transaction claim을 유일한 소유권 판정으로 사용한다.
- Tenant별 scan은 시작 cutoff를 epoch 동안 고정하고 `(next_recovery_at, document ID)` cursor를 advisory CAS checkpoint에 저장한다. Checkpoint 장애·충돌은 중복 scan만 허용하며 receipt 처리 권한을 바꾸지 않는다.
- forward reconciler는 current consent를 다시 확인하고 valid raw-only/raw+manifest만 generation-pinned 방식으로 완료한다. raw 없음, manifest-only, stored-missing을 추정 복구하지 않는다.
- expiry cleanup은 forward recovery와 분리한다. 현재 local component는 immutable dry-run target을 current receipt/fence와 다시 결합해 exact generation+metageneration만 조건부 삭제하고, regular/soft-deleted inventory가 모두 complete empty인지를 raw→manifest 순서로 감사한다. Success observation은 non-authoritative shape이며 timeout·permission·quota·drift, soft-deleted/late generation은 completion으로 승격하지 않는다. Mutable execution state는 target이 아니라 exact cleanup attempt에 기록하고, fresh completion transaction만 attempt·receipt·두 index를 원자 완료한다. 이 ADR-0026 경계와 purge·runtime wiring은 아직 구현 전이다.
- worker는 idempotency와 bounded advisory checkpoint를 가지며 replay 중 FCM·외부 호출을 실행하지 않는다. DLQ와 runtime replay mode는 scheduler 단계에서 별도 확정한다.
- worker/runtime version, service account, trigger, retry, rollback을 독립 배포 단위로 기록한다.

### Data Platform

- Firestore는 기관·기기·수리·점검·동의·receipt·현재 projection을 제공한다.
- Cloud Storage는 pseudonymous raw batch와 동일 계보의 immutable manifest를 보관하고 lifecycle rule을 적용한다.
- BigQuery는 분석 필요가 증명된 뒤 날짜 partition과 tenant/device clustering으로 활성화한다.
- 개인 식별정보와 기기·수리·위치 이벤트를 논리적으로 분리한다.
- 원본 위치에는 TTL을 적용하고 파생 집계의 계보를 보존한다.
- immutable event와 삭제 대상 개인정보를 같은 저장 정책으로 취급하지 않는다.
- 현재 상태는 projection이며 원 이벤트를 수정하지 않는다.

### ML

- 첫 모델은 안전 판단이 아니라 데이터 품질·이동 유형 보조 판별을 수행한다.
- 신뢰성 모델은 향후 일정 기간의 점검 필요 위험을 추정한다.
- 학습 데이터, feature version, model version, threshold를 inference 결과에 저장한다.
- 데이터 부족 또는 distribution shift 시 abstain한다.

### Report Agent

- 모델 값을 다시 계산하지 않는다.
- 허용된 fact ID만으로 사용자·수리사·복지관별 설명을 구성한다.
- validator를 통과하지 못한 문장은 삭제하거나 `확인 필요`로 표시한다.
- 생성 결과와 최종 사람 검토 결과를 모두 보존한다.

## 4. 핵심 신뢰 경계

1. 모바일 기기 입력은 신뢰하지 않고 Auth·App Check·스키마·시간·속도·정확도를 검증한다.
2. tenant ID는 클라이언트 주장만 믿지 않고 Firebase membership의 권한으로 재결정한다.
3. 관리자 콘솔은 원본 좌표를 기본 표시하지 않는다.
4. 모델 결과와 사람의 수리 판단은 서로 다른 provenance를 가진다.
5. LLM 출력은 데이터베이스 변경 명령으로 직접 사용하지 않는다.

## 5. 핵심 관측 지표

- 모바일 수집 성공률, 권한 상태, 배터리 소모
- 미동기화 이벤트 수와 최고 대기시간
- batch 거부율, 중복률, p95 수신시간
- GPS accuracy 분포와 필터 탈락률
- projector lag와 재처리 실패
- 모델 입력 누락률, abstention, calibration drift
- 보고서 schema 유효율, 근거 지원 precision
- tenant 접근 거부와 민감 데이터 조회 감사로그

## 6. 배포 단계

초기에는 Firebase control plane, Cloud Storage, scale-to-zero Go Cloud Run만 운영한다. BigQuery는 분석 요구가 확인된 뒤 활성화한다. 데이터 규모와 운영 증거가 나오기 전에는 PostgreSQL/PostGIS, Kafka, Kubernetes를 도입하지 않는다. 확장보다 실패 복구, 비용 가시성, 재현성을 먼저 증명한다.
