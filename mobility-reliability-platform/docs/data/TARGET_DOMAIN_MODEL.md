# Target Domain Model

## 상태와 결정 근거

이 문서는 [ADR-0007](../decisions/ADR-0007-firebase-first-hybrid.md)의 accepted 결정을 구현 가능한 데이터 계약으로 구체화한다.

- **Stage 1 운영 기준**은 Firebase control plane + GCP telemetry data plane이다.
- Firebase Auth, App Check, Cloud Firestore, Cloud Storage, Go Cloud Run, FCM을 사용한다.
- **GPS sample 하나당 Firestore document write를 만들지 않는다.** 원본 sample은 압축 batch로 Cloud Storage에 저장한다.
- BigQuery는 분석·모델 query가 실제로 필요해진 **Stage 2**에서만 활성화한다.
- PostgreSQL/PostGIS는 BigQuery GIS로도 충족하지 못하는 공간 운영 query가 확인된 **Stage 3 escape hatch**다. 1차 운영 모델이 아니다.
- 레거시 코드, API, Firebase 프로젝트에는 런타임 의존하지 않는다. 적법한 레거시 데이터는 일회성 변환 입력일 뿐이다.

## 설계 원칙

1. 모든 신규 domain ID는 UUIDv7을 사용하고, QR/사람 표시용 `public_code`를 내부 ID와 분리한다.
2. tenant는 client body의 `tenant_id`를 신뢰하지 않고, 검증된 Firebase ID token custom claim과 Firestore membership으로 결정한다.
3. 직접 식별정보는 server-only PII collection에 분리한다.
4. Firestore는 작은 control document와 현재 projection을 제공한다. 대량 raw telemetry 및 대규모 분석 row store로 사용하지 않는다.
5. Cloud Storage object, object manifest, Firestore ingest receipt는 동일한 `tenant_id + batch_id + body_hash` 계보를 공유한다.
6. 수리·점검·동의·알림·예측은 사실, 결정, 설명을 분리하고 생성 주체와 버전을 추적한다.
7. 모델 예측과 LLM 보고서는 원본 사실을 수정하지 않는다.

## Stage 1 저장 경계

| 서비스 | 저장 대상 | 금지/제한 |
|---|---|---|
| Firebase Auth | 로그인 identity | 도메인 role과 기관 관계를 Auth user record만으로 판단하지 않음 |
| Cloud Firestore | tenant membership, 기기, 수리, 점검, 동의, trip metadata, ingest receipt, 현재 projection, 알림, 예측 요약, 근거/리포트 상태 | GPS sample별 write 금지, 대형 배열/경로 geometry 금지 |
| Cloud Storage | 압축 GPS batch, immutable object manifest, 승인된 export/model artifact | object path/metadata에 이름·전화번호·QR public code 금지 |
| Go Cloud Run | token/App Check 검증, schema 검증, tenant 결정, 멱등 수신, object/receipt 생성 | client가 보낸 role/tenant/server field 신뢰 금지 |
| Cloud Tasks/Pub/Sub | 비동기 projection, retry, DLQ 전달 | at-least-once를 전제로 idempotent consumer 필요 |
| FCM | 알림 전달 | 원본 좌표·민감한 상세내용을 payload에 넣지 않음 |

## Firestore collection path

모든 운영 엔터티는 tenant 경로 아래에 둔다. 예외는 tenant 해석을 위한 최소 bootstrap index와 전역 catalog뿐이며 모두 server-managed다.

```text
/tenants/{tenantId}
  /memberships/{firebaseUid}
  /people/{personId}
  /privatePeople/{personId}                    # PII, server-only
  /personRelationships/{relationshipId}
  /devices/{deviceId}
  /deviceAssignments/{assignmentId}
  /partCatalog/{partId}
  /componentInstallations/{componentId}
  /repairs/{repairId}
    /items/{repairItemId}
  /inspections/{inspectionId}
    /observations/{observationId}
  /trips/{tripId}                              # metadata/summary only
  /ingestReceipts/{batchId}                    # server-only write
  /consentRevisions/{consentRevisionId}
  /alerts/{alertId}
    /deliveries/{deliveryId}
  /modelPredictions/{predictionId}
  /evidenceFacts/{factId}
  /reportRuns/{reportRunId}
    /claims/{claimId}
  /domainEvents/{eventId}                      # control-plane event, server-only
  /projectionCheckpoints/{projectorName}       # server-only
  /deadLetters/{deadLetterId}                  # server-only
  /externalIdentifiers/{crosswalkId}           # legacy crosswalk, server-only
  /dataAccessGrants/{grantId}                   # cross-org sharing, server-only

/authzIndex/{firebaseUid}/tenants/{tenantId}    # bootstrap membership index, server-only
/globalPartCatalog/{partId}                     # 선택 전역 표준, server-only write
```

Firestore field의 `tenant_id`는 검색·export 계보를 위해 중복 저장할 수 있지만, 권한의 원천은 document path와 membership이다. backend는 path의 tenant와 field의 tenant가 다르면 write를 거절한다.

## 공통 ID와 문서 필드

모든 domain document는 가능한 범위에서 다음 필드를 공통으로 가진다.

| 필드 | 규칙 | 의미 |
|---|---|---|
| document ID | UUIDv7 | 신규 내부 ID; membership 문서만 Firebase UID 사용 |
| `tenant_id` | server-only, path와 동일 | export/계보용 중복 tenant ID |
| `schema_version` | 양의 정수 | wire/storage schema 버전 |
| `created_at`, `updated_at` | server timestamp | 서버 기준 감사 시각 |
| `created_by`, `updated_by` | auth subject 또는 service principal | 생성·변경 주체 |
| `revision` | 양의 정수 | optimistic concurrency 및 projection version |
| `status` | 명시 enum | 삭제/완료/무효 등 상태; 의미 없는 boolean 지양 |
| `source` | `native`, `legacy_import`, `system_projection` 등 | 데이터 출처 |

`public_code`는 기기/기관에서만 사용하며 무작위·추측 불가능한 값으로 생성한다. QR에는 DB path, Firebase UID, PII를 넣지 않는다.

## 인증, membership, Security Rules

### tenant 결정

1. client는 Firebase ID token과 App Check token을 전송한다.
2. Cloud Run이 두 token을 검증한다.
3. ID token의 허용 tenant custom claim과 `/tenants/{tenantId}/memberships/{uid}`의 활성 상태를 교차 확인한다.
4. 서버가 최종 `tenant_id`, membership role, 목적을 결정한다. client body의 tenant/role은 무시한다.

### membership 문서

`/tenants/{tenantId}/memberships/{firebaseUid}`:

```text
tenant_id, firebase_uid, person_id?, roles[], status,
valid_from, valid_to?, policy_version, created_at, updated_at
```

role은 `beneficiary`, `guardian`, `case_worker`, `repairer`, `tenant_admin`, `auditor`로 시작하며, 사람이 아니라 기관 내 권한을 표현한다.

### Security Rules 기본 정책

- 명시적 allow가 없는 경로는 deny한다.
- membership, role, tenant, server timestamp, projection version, model score, object path/hash, delivery provider ID는 **server-only field**다.
- `privatePeople`, `ingestReceipts`, `domainEvents`, `projectionCheckpoints`, `deadLetters`, `externalIdentifiers`, `dataAccessGrants`, `authzIndex`의 client write는 항상 거절한다.
- `privatePeople`의 client direct read도 거절한다. 본인 정보는 purpose 검사를 수행하는 callable/Cloud Run API가 필요한 필드만 반환한다.
- client가 직접 생성 가능한 draft가 있다면 `request.resource.data.diff(resource.data).affectedKeys()`로 허용 field whitelist를 적용한다.
- 수리 완료, 점검 판정, 동의 revision 확정, alert 상태 전환은 backend command를 통해서만 수행한다.
- Rules만으로 App Check를 대체하지 않는다. App Check enforcement와 ID token 검증을 함께 사용한다.
- Firebase Emulator Suite의 Security Rules test가 배포 gate다.

## 핵심 엔터티

### 1. 기관 `/tenants/{tenantId}`

| 필드 | 필수 | 설명 |
|---|---:|---|
| `tenant_id` | Y | document ID와 동일한 UUIDv7 |
| `public_code` | Y | 현장 표시용 기관 코드 |
| `organization_type` | Y | `welfare_center`, `repair_shop`, `local_government`, `operator` |
| `legal_name`, `display_name` | Y | 법적/표시 기관명 |
| `status` | Y | `active`, `suspended`, `closed` |
| `timezone` | Y | 기본 `Asia/Seoul` |
| `data_controller_type` | Y | 데이터 처리 책임 구분 |

기관 주소/전화번호는 `/tenants/{tenantId}/privateOrganization/profile` 같은 server-only 문서에 둔다. 기관 간 공유는 같은 tenant로 병합하지 않고 `dataAccessGrants`에 제공/수신 기관, 대상, 목적, 만료일을 기록한다.

### 2. 사용자와 사람

Firebase Auth user는 로그인 identity다. 서비스 대상 사람은 별도 document로 둔다.

`/people/{personId}`:

```text
person_id, tenant_id, status,
recipient_type_code, supported_district_code,
created_at, updated_at, source
```

`/privatePeople/{personId}` (server-only):

```text
person_id, legal_name, phone_e164, birth_date?, address_text?,
encryption_key_version, pii_revision, created_at, updated_at
```

`/personRelationships/{relationshipId}`:

```text
from_person_id, to_person_id, relationship_type,
valid_from, valid_to?, status
```

`recipient_type_code`는 versioned reference mapping을 사용한다. 레거시 `smsConsent` boolean은 신규 포괄 동의로 승격하지 않는다.

### 3. 기기 `/devices/{deviceId}`

| 필드 | 필수 | 설명 |
|---|---:|---|
| `device_id`, `tenant_id` | Y | 내부 UUIDv7 |
| `public_code` | Y | QR/현장용 코드; tenant 내 unique index를 backend가 보장 |
| `device_type` | Y | `power_wheelchair`, `mobility_scooter`, `other` |
| `manufacturer`, `model_name`, `serial_number` | N | 확인되지 않으면 NULL/미포함 |
| `manufactured_at`, `purchased_at`, `commissioned_at` | N | 의미별 날짜 분리 |
| `status` | Y | `unassigned`, `active`, `maintenance`, `retired`, `lost` |
| `source_quality` | Y | `verified`, `legacy_unverified`, `user_reported` |

사용 이력은 `/deviceAssignments/{assignmentId}`에 `device_id`, `person_id`, `assignment_type`, `valid_from`, `valid_to`, `status`를 기록한다. backend transaction이 같은 기기의 활성 주사용자 중복을 차단한다.

### 4. 부품

`/partCatalog/{partId}`:

```text
part_id, tenant_id, part_code, canonical_category_code,
manufacturer?, model_name?, specification?,
expected_life_distance_m?, expected_life_days?, evidence_source?, active
```

`/componentInstallations/{componentId}`:

```text
component_id, tenant_id, device_id, part_id,
serial_number?, installed_at, removed_at?,
installed_by_repair_id?, removed_by_repair_id?,
odometer_m_at_install?, odometer_m_at_remove?, condition_status
```

기대 수명은 출처가 있을 때만 저장하며 실제 고장 사실과 구분한다.

### 5. 수리와 수리 항목

`/repairs/{repairId}`:

```text
repair_id, tenant_id, device_id,
service_tenant_id?, repairer_membership_uid?,
occurred_at, recorded_at, repair_kind,
status, currency,
billed_amount?, subsidized_amount?, copay_amount?,
battery_voltage_v?, memo_redacted?,
source_quality, created_at, updated_at
```

`/repairs/{repairId}/items/{repairItemId}`:

```text
repair_item_id, category_code, part_id?, action_code,
problem_code?, detail_text?, quantity?, unit_cost?, line_amount?,
removed_component_id?, installed_component_id?
```

수리소 당시 정보 snapshot은 versioned map으로 보존할 수 있지만 기관 ID를 대체하지 않는다. 자유 메모는 PII redaction 이후만 운영 문서에 저장한다.

### 6. 점검과 관찰

`/inspections/{inspectionId}`:

```text
inspection_id, tenant_id, device_id, inspection_type,
template_id, template_version, performed_by_uid?,
started_at, completed_at?, overall_result,
follow_up_repair_id?, status
```

`/inspections/{inspectionId}/observations/{observationId}`:

```text
observation_id, item_code,
response_status(normal|issue|not_observed|not_applicable),
severity?, measured_value?, unit?, note_redacted?
```

레거시 16개 boolean은 versioned mapping을 거쳐 observation 문서로 변환한다. 누락과 `false`를 자동으로 같은 의미로 처리하지 않는다.

### 7. 주행 세션 `/trips/{tripId}`

Firestore에는 metadata와 작은 summary만 둔다.

```text
trip_id, tenant_id, device_id, person_id,
installation_id, client_session_id,
started_at, ended_at?, capture_mode, status,
batch_count, raw_sample_count, accepted_sample_count,
distance_m?, duration_s?, quality_status, quality_score?,
processing_version?, latest_projection_at?, created_at, updated_at
```

금지 field:

- sample 배열
- 원본 lat/lng 배열
- 전체 polyline/대형 geometry
- sample마다 생성한 subcollection document

스마트폰 이동을 전동보장구 주행으로 자동 확정하지 않는다. 세션 모드, 사용자 확인, 품질 모델의 `abstain`을 함께 보존한다.

### 8. GPS batch와 object manifest

#### Storage object path

```text
telemetry/v1/tenants/{tenantId}/devices/{deviceId}/trips/{tripId}/
  year={YYYY}/month={MM}/day={DD}/{batchId}.ndjson.zst

telemetry-manifests/v1/tenants/{tenantId}/trips/{tripId}/
  year={YYYY}/month={MM}/day={DD}/{batchId}.manifest.json
```

path에는 이름, 전화번호, 상세 주소, Firebase UID, 기기 `public_code`를 넣지 않는다.

압축 batch record:

```text
client_sample_id, sequence_no, recorded_at,
latitude, longitude, accuracy_m,
altitude_m?, speed_mps?, bearing_deg?, motion_activity?, is_mocked?
```

manifest:

```text
manifest_version, payload_schema_version,
tenant_id, device_id, trip_id, installation_id,
batch_id, client_batch_id, body_hash, object_sha256,
compression, content_type, sample_count,
first_recorded_at, last_recorded_at,
object_path, object_generation, received_at,
validator_version, consent_revision_id, kms_key_version?
```

manifest는 immutable object로 만들고 object generation precondition을 사용한다. 서버가 검증 후 작성하며 client 제공 manifest를 그대로 신뢰하지 않는다.

#### Firestore ingest receipt

`/ingestReceipts/{batchId}`:

```text
batch_id, tenant_id, trip_id, device_id, installation_id,
client_batch_id, body_hash, object_sha256,
object_path, manifest_path, object_generation,
status(received|stored|queued|projected|rejected|deleting|deleted),
sample_count, accepted_count?, rejected_count?,
error_reason?, payload_schema_version, projector_version?,
received_at, stored_at?, projected_at?, expires_at
```

receipt는 control plane 상태이며 GPS sample을 포함하지 않는다. client read는 본인 세션의 제한 field에만 허용하고 write는 server-only다.

### 9. 위치 보존기간

Stage 1 제안값이며 실증 전 개인정보 영향평가와 기관 계약으로 확정한다.

- 정밀 원본 batch: 수신 후 기본 **30일**, 명시 동의와 검토가 있어도 최대 90일. Storage lifecycle rule로 만료한다.
- 마스킹된 route artifact가 필요한 경우: 기본 **90일**, 별도 derived Storage prefix와 manifest로 관리한다.
- Firestore trip summary: 서비스 관계 종료 또는 정책상 보존기간까지. 원본 위치가 없어도 재식별 위험이 있는 field는 최소화한다.
- Stage 2 익명 집계: 최대 **24개월**, 소수 사용자 셀 억제와 k-threshold를 적용한다.
- 동의 철회/삭제 요청은 미래 수집을 즉시 중단하고 Firestore metadata, Storage object/manifest, Stage 2 BigQuery row/delete job을 하나의 deletion workflow로 추적한다.

`expires_at`은 receipt와 manifest에 기록하고 lifecycle policy drift를 모니터링한다. 삭제 완료 시 위치나 PII가 아닌 범위·건수·object generation만 deletion receipt로 남긴다.

### 10. 동의 `/consentRevisions/{consentRevisionId}`

```text
consent_revision_id, tenant_id, person_id,
purpose_code, policy_version,
status(granted|denied|withdrawn|expired),
granted_at?, withdrawn_at?, expires_at?,
collection_channel, evidence_object_path?, evidence_hash?,
guardian_person_id?, supersedes_revision_id?, created_at
```

목적은 `service_operation`, `precise_location`, `maintenance_model`, `sms_notice`, `research_export` 등으로 분리한다. 과거 revision을 update로 덮지 않는다. 현재 동의 상태는 server projection으로 계산한다.

### 11. 알림

`/alerts/{alertId}`:

```text
alert_id, tenant_id, person_id, device_id,
prediction_id?, alert_type, severity, reason_codes,
status, created_at, acknowledged_at?, resolved_at?
```

`/alerts/{alertId}/deliveries/{deliveryId}`:

```text
delivery_id, channel, destination_ref,
template_version, attempt_no, provider_message_id?,
status, sent_at?, failed_reason?
```

예측이 생성돼도 동의, quiet hours, 정책 threshold, abstention을 통과해야 알림을 만든다. FCM payload에는 PII와 정밀 위치를 넣지 않는다.

### 12. 모델 예측 `/modelPredictions/{predictionId}`

Firestore에는 운영 UI에 필요한 예측 요약만 둔다.

```text
prediction_id, tenant_id, device_id, component_id?,
prediction_type, model_version,
feature_snapshot_manifest_path, feature_snapshot_hash,
predicted_at, valid_until?, score?, calibrated_probability?,
lower_bound?, upper_bound?, decision, reason_codes,
superseded_by_prediction_id?, created_at
```

모델 artifact, 전체 feature snapshot, evaluation output은 versioned Storage path에 둔다. `model_version`, dataset manifest hash, 코드 commit, feature schema, metric은 model manifest에서 추적한다. client는 score/model version을 쓸 수 없다.

### 13. 근거와 보고서

`/evidenceFacts/{factId}`:

```text
fact_id, tenant_id, subject_type, subject_id,
fact_type, value, unit?, as_of,
source_entity_type, source_entity_id,
derivation_version, content_hash, created_at
```

`/reportRuns/{reportRunId}` 및 `/claims/{claimId}`:

```text
reportRun: report_type, audience, generator_version,
prompt_hash, status, artifact_path?, created_at

claim: claim_text, claim_type, fact_ids[], validation_status
```

근거 없는 주요 claim은 `unsupported`로 표시하고 최종본에서 제외하거나 `[확인 필요]`로 강등한다. report artifact가 크면 Storage에 두고 Firestore에는 상태와 hash만 둔다.

## 멱등성 계약

모바일 outbox와 비동기 worker는 최소 한 번 전송을 전제로 한다.

- 앱 설치마다 `installation_id`, 세션마다 `client_session_id`, batch마다 `client_batch_id`, sample마다 `client_sample_id`를 앱에서 생성한다.
- ingest idempotency key는 `sha256(tenant_id | installation_id | client_batch_id | payload_schema_version)`다.
- Cloud Run은 Firestore transaction으로 `/ingestReceipts/{batchId}`를 create하고 body hash를 비교한다.
- 동일 key/동일 body hash는 기존 receipt를 반환한다.
- 동일 key/다른 body hash는 `409 IDEMPOTENCY_CONFLICT`로 거절하고 audit event를 남긴다.
- Storage write는 `ifGenerationMatch=0`을 사용해 overwrite를 막는다.
- receipt 생성 후 object write 전에 실패한 상태를 sweeper가 복구할 수 있도록 명시적 상태 machine과 retry lease를 둔다.
- 수리/점검/동의 command도 `Idempotency-Key + body_hash` receipt를 server-only collection에 저장한다. 보존기간은 최소 30일이며 최대 offline 지연보다 길어야 한다.

## 이벤트, projection, 재처리, DLQ

### control-plane event

`/domainEvents/{eventId}`:

```text
event_id, tenant_id, aggregate_type, aggregate_id,
aggregate_revision, event_type, event_schema_version,
occurred_at, received_at, source, source_event_id?,
idempotency_key, payload, payload_hash,
correlation_id?, causation_id?, processing_status
```

수리·점검·동의 같은 낮은 빈도의 control event만 Firestore에 둔다. GPS sample event는 Storage batch가 원장이고 Firestore event로 펼치지 않는다.

### projection

- trip summary는 `/trips/{tripId}`에 반영한다.
- 현재 기기 상태는 `/devices/{deviceId}/state/current` 또는 크기가 작을 때 device server-only projection field로 반영한다.
- projector checkpoint는 `/projectionCheckpoints/{projectorName}`에 `projector_version`, `last_receipt_cursor`, `updated_at`, `replay_run_id?`를 둔다.
- replay는 Storage object manifest 또는 control event를 읽어 shadow collection/prefix에 재구성하고 count/hash invariant 검증 후 version pointer를 전환한다.
- replay mode에서는 FCM/SMS/외부 API 호출을 금지한다. effect ledger로 기존 발송을 식별한다.

### DLQ

- schema/consent/tenant 검증 실패는 receipt를 `rejected`로 만들고 reason code를 남긴다.
- 일시적 processor 실패는 Cloud Tasks/Pub/Sub retry 후 `/deadLetters/{deadLetterId}`에 receipt/event ID, object generation, processor version, error class, attempt count, first/last failure 시각을 기록한다.
- DLQ는 원본 object를 수정하지 않는다. 재처리 시 새 `replay_run_id`와 처리 결과를 연결한다.

## PII와 위치 접근 통제

- 이름·전화번호·상세 주소는 `privatePeople`에만 저장한다.
- Storage path, manifest custom metadata, Cloud Logging, Crashlytics custom key, FCM, 모델 feature에 PII를 넣지 않는다.
- 원본 위치 object는 일반 Firebase client SDK로 직접 읽지 못하게 하고, 검증된 backend service account와 목적 제한 signed access만 허용한다.
- CMEK 사용 여부와 key rotation은 실증 전 위협모델/비용 검토로 확정한다.
- PII 조회, 위치 export, 동의 대리 변경, 모델 학습 snapshot 승인은 server-side audit event를 남긴다.
- backup/export도 동일한 보존·삭제 정책을 적용하고 export manifest로 추적한다.

## 레거시 crosswalk

`/externalIdentifiers/{crosswalkId}`는 server-only다.

```text
source_system, source_collection, source_id_type, source_id,
target_entity_type, target_id, match_method, confidence,
source_snapshot_hash, mapping_version,
reviewed_by?, reviewed_at?, created_at
```

`source_system + source_collection + source_id_type + source_id` hash를 document ID로 사용할 수 있다. 레거시 Mongo ObjectId, Firestore document ID, Firebase UID, 공개 `vehicleId`를 namespace 없이 합치지 않는다.

## Stage 2 — 선택적 BigQuery

다음 조건이 실제로 생길 때만 활성화한다.

- Storage manifest scan만으로 시간분할 학습 dataset 생성이 비효율적이다.
- 코호트/공간 집계를 반복 수행해야 한다.
- 쿼리 SLA와 비용 상한이 정의됐다.

제안 dataset:

| table | partition | cluster | 용도 |
|---|---|---|---|
| `telemetry_samples` | `DATE(recorded_at)` | `tenant_id`, `device_id` | 승인된 batch의 분석 복제본 |
| `trip_features` | `DATE(feature_at)` | `tenant_id`, `device_id` | versioned feature |
| `repair_events` | `DATE(occurred_at)` | `tenant_id`, `device_id` | 시간분할 label 후보 |
| `model_predictions` | `DATE(predicted_at)` | `tenant_id`, `model_version` | 평가/monitoring |

- partition filter를 강제하고 query bytes 상한/budget alert를 설정한다.
- Storage manifest hash, load job ID, row count, rejected row count를 lineage table에 둔다.
- row-level/authorized view로 tenant 접근을 제한하며 BigQuery를 운영 UI의 권한 원천으로 쓰지 않는다.
- 삭제 요청은 partition expiration만 기다리지 않고 대상 row delete job과 완료 receipt를 추적한다.
- BigQuery GIS는 익명 지역 집계의 1차 공간 분석 선택지다.

## Stage 3 — PostGIS escape hatch

PostgreSQL/PostGIS는 다음이 측정으로 확인될 때만 ADR을 새로 작성해 도입한다.

- BigQuery GIS의 batch 지연이 운영 query SLA를 충족하지 못함
- 빈번한 저지연 공간 join/transaction이 제품 핵심이 됨
- 예상 비용과 운영 인력이 승인됨

도입하더라도 Firebase membership/control plane을 즉시 대체한다고 가정하지 않는다. 데이터 소유권, 동기화 원천, 삭제 계보를 새 ADR에서 결정한다.

## 구현 전 확정할 ADR/정책

1. UUIDv7 생성 위치와 clock rollback 처리.
2. Security Rules의 역할별 read/write matrix와 Emulator test fixture.
3. Storage batch 최대 bytes/sample 수/시간창과 압축 포맷.
4. 원본 30일, derived 90일, 익명 집계 24개월 보존안.
5. device `public_code` 유일성 reservation 방식.
6. 수리 범주, 점검 template, 부품 catalog 초기 코드.
7. App Check enforcement rollout과 개발/실증 bypass 통제.
8. Stage 2 BigQuery activation threshold와 비용 상한.
