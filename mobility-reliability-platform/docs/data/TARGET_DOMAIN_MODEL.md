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

1. 서버가 발급하는 신규 domain ID는 UUIDv7을 사용하고, QR/사람 표시용 `public_code`를 내부 ID와 분리한다. `installationId`, `clientSessionId`, `clientBatchId`, `clientSampleId` 같은 명시적 client correlation UUID와 파생 index key는 예외다.
2. request의 `tenantId`는 authorization 후보 scope일 뿐 권한 근거가 아니다. 서버는 검증된 Firebase principal과 해당 tenant의 활성 Firestore membership으로 scope를 authorize하고, 필요한 배포에서만 custom claim을 추가 제한으로 교차 확인한다.
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
| Go Cloud Run telemetry gateway | token/App Check 검증, telemetry schema·scope 검증, 멱등 수신, object/receipt 생성 | 범용 비즈니스 CRUD, client가 보낸 role/tenant/server field 신뢰 금지 |
| Domain Command API | session/trip 발급, 수리·점검·동의 command, purpose-limited PII read | telemetry raw 수집과 무관한 임의 direct Admin SDK write 금지 |
| Cloud Tasks/Pub/Sub + async worker | projection, importer, feature/fact/report job, retry, DLQ | at-least-once를 전제로 idempotent consumer와 side-effect 차단 필요 |
| FCM | 알림 전달 | 원본 좌표·민감한 상세내용을 payload에 넣지 않음 |

## Firestore collection path

모든 운영 엔터티는 tenant 경로 아래에 둔다. 예외는 tenant 해석을 위한 최소 bootstrap index와 전역 catalog뿐이며 모두 server-managed다.

```text
/tenants/{tenantId}
  /memberships/{firebaseUid}
  /appInstallations/{installationId}             # server-only write
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
    /recoveryAttempts/{attemptId}              # 좌표 없는 복구 감사 ledger
  /recoveryWorkerState/{workerId}              # advisory scan checkpoint, server-only
  /ingestIdempotency/{derivedKey}              # server-only read/write
  /ingestClientBatches/{derivedKey}            # server-only client-batch uniqueness
  /ingestCleanupTargets/{cleanupId}             # exact-generation 삭제 target
  /ingestIntegrityFindings/{findingId}          # accepted/rejected 상태 보존형 finding
  /ingestPurgeJobs/{receiptId}                  # bounded linkage purge coordinator
  /consentRevisions/{consentRevisionId}
  /consentStates/{derivedId}                   # 현재 동의 projection, server-only
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

Firestore document와 Storage manifest는 `snake_case`를 사용한다. 모바일·HTTP JSON의 `camelCase`는 wire 형식일 뿐이며 adapter가 명시적으로 mapping한다.

## 공통 ID와 문서 필드

모든 domain document는 가능한 범위에서 다음 필드를 공통으로 가진다.

| 필드 | 규칙 | 의미 |
|---|---|---|
| document ID | 기본 UUIDv7 | 서버 domain ID 기준. membership은 Firebase UID, app installation은 client UUID, hash index는 파생 key를 사용 |
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
3. 검증 principal은 Firebase UID와 App ID만 유지하고 raw GPS body에는 넣지 않는다.
4. request의 `tenantId`를 후보 scope로 사용해 tenant의 active 상태와 `/tenants/{tenantId}/memberships/{uid}`의 활성 상태·유효기간을 확인하고, 필요한 배포에서만 허용 tenant custom claim을 추가 제한으로 교차 확인한다.
5. 서버가 membership role, installation의 UID·App ID, trip의 exact assignment reference, 현재 consent state와 목적, sample 시간 범위를 검사해 scope를 최종 authorize한다. client body의 tenant/role은 단독 권한 근거로 사용하지 않는다.
6. 첫 rollout은 `beneficiary` membership의 `person_id == trip.person_id`인 본인 upload만 허용한다. guardian 등 대신 upload는 관계와 목적 allow matrix가 추가되기 전 허용하지 않는다.

### membership 문서

`/tenants/{tenantId}/memberships/{firebaseUid}`:

```text
tenant_id, firebase_uid, person_id?, roles[], status,
valid_from, valid_to?, policy_version, created_at, updated_at
```

role은 `beneficiary`, `guardian`, `case_worker`, `repairer`, `tenant_admin`, `auditor`로 시작하며, 사람이 아니라 기관 내 권한을 표현한다.

`person_id`는 사람이 아닌 service/auditor membership에서는 생략할 수 있지만, telemetry를 업로드하는 `beneficiary` membership에는 필수이며 trip의 `person_id`와 같아야 한다.

`/appInstallations/{installationId}`는 다음 계약으로 client UUID, Firebase UID와 App Check App ID를 연결하는 server-managed 문서다.

```text
installation_id, tenant_id, firebase_uid, app_check_app_id,
status(active|revoked), registered_at, last_verified_at?, revoked_at?,
schema_version, created_at, updated_at, revision
```

active 상태에 `revoked_at`이 있거나 path/field tenant, installation ID, UID, App ID가 다르면 telemetry authorization을 거절한다. 하드웨어 fingerprint를 만들지 않으며 재설치·로그아웃·기기 양도 시 새 relation으로 취급한다.

### Security Rules 기본 정책

- 명시적 allow가 없는 경로는 deny한다.
- tenant document가 `active`이고 path/field tenant가 일치해야 membership이 client read 권한을 만든다.
- beneficiary 등 일반 member는 `membership.person_id`와 직접 일치하는 people·relationship·assignment·trip·consent·alert만 조회한다. people은 자신의 path를 단건 get만 할 수 있다. 나머지 list/query는 path와 같은 `tenant_id`와 자신의 `person_id` filter를 모두 증명해야 한다.
- 초기 operational direct read role은 `case_worker`, `tenant_admin`으로 제한한다. guardian·repairer·auditor의 타인·기관 간·감사 목적 조회는 relationship·grant·purpose를 검사하는 backend DTO를 사용한다.
- devices·repairs·inspections·prediction·evidence·report처럼 문서 자체에서 본인 person 관계를 안전하게 증명할 수 없는 데이터는 operational staff만 직접 읽고 beneficiary는 backend DTO를 사용한다. 상세 matrix는 [ADR-0014](../decisions/ADR-0014-firestore-client-read-boundary.md)를 따른다.
- membership, role, tenant, server timestamp, projection version, model score, object path/hash, delivery provider ID는 **server-only field**다.
- `privatePeople`, `appInstallations`, `ingestReceipts`, `ingestIdempotency`, `ingestClientBatches`, `recoveryWorkerState`, `consentStates`, `domainEvents`, `projectionCheckpoints`, `deadLetters`, `externalIdentifiers`, `dataAccessGrants`, `authzIndex`의 client write는 항상 거절한다.
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

#### 기관 간 수리 접근 grant

복지관 tenant와 수리소 tenant를 합치거나 수리사를 모든 복지관의 member로 중복 등록하지 않는다. `/tenants/{dataOwnerTenantId}/dataAccessGrants/{grantId}`는 server-only로 다음 최소 계약을 가진다.

```text
grant_id, data_owner_tenant_id, service_tenant_id,
purpose(repair_intake|repair_write|history_read),
resource_type(device|assignment_set), resource_ids[],
allowed_actions[], status(active|revoked|expired),
valid_from, valid_to, created_by, created_at, revoked_at?
```

- QR에는 grant·tenant·Firestore path가 아니라 추측 불가능한 device `public_code`만 넣는다.
- Domain Command API가 public code를 해석하고, 수리사의 active service-tenant membership과 data-owner tenant의 active grant, 대상 기기, 목적, 만료를 모두 검사한다.
- 수리 event는 data-owner tenant에 기록하되 `service_tenant_id`와 검증된 repairer principal reference를 남긴다.
- 조회·command 시 grant ID, 목적, actor, 대상, 결과를 민감정보가 없는 access audit로 기록한다.
- grant가 없거나 철회·만료·대상 불일치이면 정보 존재 여부를 과도하게 노출하지 않고 거절한다.
- client direct cross-tenant read/write는 grant가 있어도 허용하지 않는다.

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

사용 이력은 `/deviceAssignments/{assignmentId}`에 다음 필드를 기록한다.

```text
assignment_id, tenant_id, device_id, person_id,
assignment_type(primary_user|temporary_user),
status(active|ended|revoked), valid_from, valid_to?
```

backend transaction이 같은 기기의 활성 주사용자 중복을 차단한다. trip은 해당 시점의 `device_assignment_id`를 불변 reference로 저장해 ingest authorization에서 `(device_id, person_id)` query 없이 exact read로 검증한다.

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
trip_id, tenant_id, device_id, person_id, device_assignment_id,
installation_id, client_session_id,
consent_revision_id,
started_at, ended_at?,
capture_mode(foreground|background|reconciled_offline),
status(recording|ended|cancelled), ingest_expires_at,
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

`trip_id`는 인증된 session-start command가 발급하는 server UUIDv7이다. `client_session_id`는 모바일이 offline capture를 시작할 때 만든 UUID correlation ID다. command는 active membership, person과 기기의 유효한 assignment, app installation, `precise_location` consent revision을 검증하고 둘을 연결한다. trip과 receipt의 client direct write는 Stage 1에서 허용하지 않는다.

`recording`과 `ended` 상태만 `now < ingest_expires_at` 동안 batch를 받을 수 있다. `ended`에는 `ended_at`이 필수이며 기본 지연 업로드 window는 종료 후 72시간이다. batch의 첫·마지막 `captured_at`은 trip 시간 범위와 대조하고 진행 중 trip의 미래 시각은 server now 이후 5분까지만 clock skew로 허용한다. 세부 권한 계약과 원자성 gate는 [ADR-0013](../decisions/ADR-0013-telemetry-authorization-snapshot.md)을 따른다.

### 8. GPS batch와 object manifest

#### Storage object path

```text
telemetry/v2/tenants/{tenantId}/devices/{deviceId}/trips/{tripId}/
  year={YYYY}/month={MM}/day={DD}/{batchId}.json.gz

telemetry-manifests/v2/tenants/{tenantId}/trips/{tripId}/
  year={YYYY}/month={MM}/day={DD}/{batchId}.manifest.json
```

path에는 이름, 전화번호, 상세 주소, Firebase UID, 기기 `public_code`를 넣지 않는다.

`telemetry-batch.v2` request는 하나의 제한된 JSON object이며 wire JSON 자체를 deterministic gzip으로 저장한다. 따라서 아래 request와 sample은 실제 HTTP/TypeScript `camelCase` 계약이고, manifest와 Firestore 문서만 `snake_case`다.

```text
schemaVersion, clientBatchId, tenantId, deviceId, tripId,
clientSessionId, installationId, consentRevisionId, sentAt, samples
```

Firebase UID와 App ID는 request body나 raw object에 저장하지 않는다. 압축 batch sample:

```text
clientSampleId, sequence, capturedAt,
latitude, longitude, horizontalAccuracyM,
altitudeM?, speedMps?, headingDegrees?, activityHint?,
isMockLocation?, source(phone_gps)
```

manifest:

```text
manifest_version, payload_schema_version,
tenant_id, device_id, trip_id, installation_id,
batch_id, client_batch_id, body_hash, object_sha256,
object_crc32c, object_size,
compression(gzip), content_type(application/json), sample_count,
first_captured_at, last_captured_at,
object_path, object_generation, object_metageneration,
received_at, expires_at,
validator_version, consent_revision_id, kms_key_version?
```

manifest는 versioned struct의 compact UTF-8 JSON으로 canonicalize하고 immutable object로 만든다. raw와 manifest 모두 `DoesNotExist` precondition을 사용하며 SHA-256, CRC32C, size, generation과 metageneration을 검증한다. 서버가 검증 후 작성하며 client 제공 manifest를 그대로 신뢰하지 않는다. exact replay·collision·부분 실패 복구 계약은 [ADR-0016](../decisions/ADR-0016-immutable-telemetry-artifact-lineage.md)을 따른다.

#### Firestore ingest receipt

`/ingestReceipts/{batchId}`:

```text
receipt_id, reservation_key, client_batch_key,
batch_id, tenant_id, trip_id, device_id, installation_id,
consent_revision_id, client_batch_id, body_hash, object_sha256,
object_crc32c, object_size, object_path,
object_generation, object_metageneration,
manifest_path, manifest_sha256, manifest_crc32c, manifest_size,
manifest_generation, manifest_metageneration,
status(reserved|stored|queued|projected|rejected|cleanup_pending|expired|recovery_hold|deleting|deleted),
expected_sample_count, sample_count?,
first_captured_at, last_captured_at, validator_version,
fencing_token,
lease_owner_id?, lease_owner_kind?,
lease_acquired_at?, lease_heartbeat_at?, lease_expires_at?,
recovery_attempt_count, next_recovery_at?, last_recovery_code?,
cleanup_quiescence_until?,
cleanup_mode?(reservation_expiry|artifact_retention|explicit_deletion|security_approved_rejected),
cleanup_origin_status?(reserved|recovery_hold|stored|queued|projected|rejected),
next_integrity_check_at?, hold_reason?, hold_review_due_at?,
accepted_count?, rejected_count?,
rejection_code?, payload_schema_version, projector_version?, revision,
created_at, updated_at, stored_at?, projected_at?,
reservation_deadline, artifact_expires_at,
receipt_retention_floor, purge_eligible_at?
```

receipt는 control plane 상태이며 GPS sample을 포함하지 않는다. Stage 1 client direct read/write는 모두 거절하고 gateway가 안전한 receipt DTO만 반환한다.

- 신규 HTTP reservation은 initial lease를 같은 transaction에서 만들고 `fencing_token=1`로 시작한다. takeover마다 정확히 1 증가하고 terminal 상태에서도 감소·재사용하지 않는다.
- active lease의 owner·token·deadline이 일치하는 worker만 finalizer를 실행한다. lease field가 일부만 있으면 손상 상태다.
- 신규 receipt의 `next_recovery_at`은 initial `lease_expires_at`이며 renew·takeover·safe release와 같은 transaction에서 새 lease expiry 또는 backoff로 갱신한다.
- 이미 stored+동일 전체 lineage 또는 rejected+동일 code인 finalizer replay는 상태·revision을 바꾸지 않고 기존 receipt를 반환할 수 있다. 실제 mutation에는 live fence가 필수다.
- manifest 재구성에 필요한 expected sample count·capture bounds·validator version은 최초 reservation에서 고정하고 이후 current deployment 값으로 바꾸지 않는다.
- recovery runtime은 receipt의 `validator_version`을 explicit decoder/validator/manifest-builder registry에서 찾아 사용한다. 알 수 없는 version은 current code로 대체하지 않고 hold한다.
- reservation 때 `reservation_deadline < artifact_expires_at`과 `receipt_retention_floor`를 고정하고 `purge_eligible_at`은 비워 둔다. terminal cleanup·감사 완료 transaction이 두 index와 receipt에 같은 purge eligibility를 설정한다. eligibility 뒤 bounded purge job이 nested attempt와 linked cleanup target·integrity finding을 먼저 제거하고, 마지막 transaction만 두 index와 receipt를 함께 삭제한다.
- manifest v1의 기존 `expires_at` field는 `artifact_expires_at` 의미로만 읽고 reservation deadline이나 receipt purge에 사용하지 않는다.
- `recovery_hold`는 reserved-origin의 손상·불가능한 artifact ordering을 위한 사람 검토 상태이며 일반 retry가 자동 해제하지 않는다. accepted/rejected receipt는 integrity finding 때문에 이 상태로 downgrade하지 않는다.
- reserved-origin 만료 전 hold는 `now <= hold_review_due_at < artifact_expires_at`을 가져야 한다. 만료 뒤 처음 발견된 reserved finding은 `hold_review_due_at=now`로 기록하고 즉시 held cleanup 대상이 된다. 어느 hold도 artifact lifecycle을 자동 연장하지 않는다.
- `cleanup_pending`은 `BeginCleanupTransition` 또는 `BeginHeldCleanup`이 token을 증가시키고 forward lease를 fence-out한 뒤 늦은 Storage write의 quiet period를 기다리는 reserved-origin 상태다. Deadline transition 시 recovery attempt count가 있는 만료 lease는 exact nested attempt를 같은 transaction에서 `failed/lease_expired`로 닫아야 하며, linkage를 검증할 수 없으면 receipt transition도 금지한다. 일반 finalizer와 accepted/rejected cleanup은 이 상태로 전환할 수 없다. `expired`는 reserved-origin partial artifact cleanup 완료 또는 version-aware empty 확인 뒤에만 사용한다.
- accepted `stored|queued|projected` cleanup은 `deleting -> deleted`를 사용하며 동일 lineage replay는 모든 accepted lifecycle 상태에서 complete 의미다. rejected receipt는 cleanup target 진행과 무관하게 `rejected`와 replay-rejected 의미를 유지한다.
- stored/rejected transition은 `next_integrity_check_at`을 설정한다. 별도 bounded integrity auditor가 이 cursor와 Storage Inventory로 stored-missing·rejected-artifact를 찾으며 reserved sweeper query가 이 역할을 대신하지 않는다.

`/recoveryWorkerState/forward`:

```text
revision,
next_recovery_at?, document_id?, scan_cutoff?,
updated_at
```

이 문서는 tenant별 forward candidate scan의 advisory fairness checkpoint다. `next_recovery_at + document_id` cursor와 `scan_cutoff`는 함께 존재하거나 함께 비어야 하며 cursor 시각은 cutoff 뒤일 수 없다. Worker는 같은 revision을 읽은 CAS transaction으로 revision을 1 증가시키고, page 경계에서는 cursor와 고정 cutoff를 저장하며 epoch tail에서는 두 값을 함께 reset한다. Load/persist 장애나 CAS conflict는 duplicate scan을 만들 수 있지만 receipt ownership·lease·artifact 권한을 만들거나 박탈하지 않는다. Client read/write는 모두 거절한다. 현재 worker ID는 `forward` 하나이며 startup·scheduler 연결 전의 local component state다. `status`·`next_recovery_at`이 query에서 누락된 receipt를 찾는 무결성 audit cursor로 사용하지 않는다. 자세한 결정은 [ADR-0021](../decisions/ADR-0021-bounded-forward-recovery-worker.md)을 따른다.

`/ingestReceipts/{batchId}/recoveryAttempts/{attemptId}`:

```text
attempt_id, tenant_id, receipt_id,
owner_kind(request|sweeper|cleanup), fencing_token, worker_version,
status(started|completed|failed),
decision_domain?(artifact_reconciliation|current_authorization),
authorization_disposition?(denied|unavailable),
phase?, classification?, reason_code?, action?, outcome?, action_hash?,
hold_code?, release_code?, rejection_code?,
raw_sha256?, raw_crc32c?, raw_size?, raw_generation?, raw_metageneration?,
manifest_sha256?, manifest_crc32c?, manifest_size?, manifest_generation?, manifest_metageneration?,
hold_review_due_at?, failure_code?,
started_at, completed_at?, failed_at?
```

recovery attempt는 좌표·artifact path·raw body·Firebase UID·App ID·credential token과 provider 원문 오류를 포함하지 않는 감사·운영 ledger다. `started`와 `failed`에는 decision/action terminal field가 없어야 한다. Failed prior attempt에 `decision_domain` 또는 `authorization_disposition`을 포함한 terminal residue가 있으면 takeover는 새 lease·attempt를 만들지 않고 fail-closed한다. Completed artifact action은 `decision_domain=artifact_reconciliation`, current authorization hold/release는 `decision_domain=current_authorization`과 exact bounded disposition을 사용하며 서로 교환되지 않는다. expired lease를 takeover한 request와 sweeper/cleanup claim은 claim transaction에서 `started` attempt를 만들고 후속 결과를 fenced update한다. 상태 원장은 receipt이며 current protocol의 receipt action과 attempt completion은 한 transaction에 기록한다. 응답 유실은 fresh outcome correlation으로 판별하고 mutation을 replay하지 않는다. forward recovery와 expiry cleanup은 다른 mode와 완료 조건을 사용한다. 자세한 계약은 [ADR-0017](../decisions/ADR-0017-fenced-ingest-recovery.md)과 [ADR-0020](../decisions/ADR-0020-two-pass-forward-reconciliation.md)을 따른다.

Firestore는 parent receipt 삭제 시 `recoveryAttempts`를 cascade delete하지 않는다. attempt는 독립 TTL로 먼저 지우지 않고 receipt의 `purge_eligible_at` 이후 bounded purge job이 document name cursor로 지운다. terminal purge가 시작된 receipt에는 새 attempt 생성을 금지한다.

`/ingestIntegrityFindings/{findingId}`:

```text
finding_id, tenant_id, receipt_id, receipt_revision,
origin_status(stored|queued|projected|rejected),
reason(stored_missing|lineage_mismatch|rejected_artifact|provider_unavailable),
severity, status(open|cleanup_approved|resolved|escalated),
evidence_hashes[], ownership_verified?, approval_id?,
found_at, review_due_at, resolved_at?
```

accepted/rejected receipt의 integrity 문제는 status를 `recovery_hold`로 바꾸지 않고 finding으로 기록한다. finding에는 좌표·raw body·UID/App ID를 넣지 않는다. rejected artifact cleanup은 `ownership_verified=true`와 사람의 보안 `approval_id` 없이는 시작할 수 없다. linked finding도 receipt 최종 purge 전에 bounded 삭제 또는 승인된 최소 증거로 축약한다.

`/ingestCleanupTargets/{cleanupId}`:

```text
cleanup_id, tenant_id, receipt_id, reservation_key,
mode(reservation_expiry|artifact_retention|explicit_deletion|security_approved_rejected),
cleanup_origin_status(reserved|recovery_hold|stored|queued|projected|rejected),
receipt_revision, fencing_token,
object_path?, object_generation?, object_sha256?,
manifest_path?, manifest_generation?, manifest_sha256?,
status(planned|raw_deleted|manifest_deleted|completed|failed|hold),
raw_delete_result?, manifest_delete_result?, error_class?,
created_at, updated_at, completed_at?, worker_version
```

cleanup target은 delete 전에 transaction으로 생성하며 이후 mode, origin status, path나 generation을 새 latest 값으로 교체하지 않는다. raw exact generation을 먼저 삭제하고 manifest exact generation을 다음에 삭제한다. transient/permission/quota 오류를 NotFound로 바꾸지 않으며, target과 receipt lineage가 완전하지 않으면 delete를 실행하지 않는다. rejected origin은 exact ownership evidence와 별도 보안 승인 ID가 없으면 target 자체를 만들지 않는다.

reserved-origin expiry cleanup은 exact prior forward attempt가 있으면 먼저 같은 transaction에서 terminal로 닫고 receipt를 `cleanup_pending`으로 바꾸며 모든 forward lease를 fence-out한 뒤 `cleanup_quiescence_until`까지 기다린다. Attempt closure와 receipt transition은 함께 commit 또는 rollback되어야 하고 recovery attempt count는 누적값으로 유지한다. accepted retention cleanup은 receipt를 `deleting`으로 바꾸며, rejected cleanup은 receipt status를 유지한다. 모든 경로는 immutable `cleanup_origin_status`를 기록하고 quiet period를 최대 lease와 Storage operation timeout 합보다 길게 둔다. 삭제 뒤 version-aware live generation을 재검사하며 새 generation이 보이면 원 target을 바꾸지 않고 finding 또는 별도 linked target으로 분리한다.

`/ingestPurgeJobs/{receiptId}`:

```text
receipt_id, receipt_revision, linkage_hash,
status(planned|attempts_purging|targets_purging|findings_purging|ready|linkage_deleted|failed),
attempt_cursor?, attempt_deleted_count,
target_cursor?, target_deleted_count,
finding_cursor?, finding_deleted_count,
verified_empty_at?, linkage_deleted_at?, purge_job_expires_at,
created_at, updated_at, error_class?
```

purge job은 receipt가 terminal이고 `purge_eligible_at <= now`일 때만 생성한다. `recoveryAttempts`, `ingestCleanupTargets`, `ingestIntegrityFindings`를 bounded·resumable page로 먼저 제거하고 세 query가 empty임을 증명한 뒤 `ready`가 된다. 마지막 transaction은 job의 receipt revision/linkage hash, 두 uniqueness index와 receipt를 재검증하고 세 linkage 문서만 원자 삭제한다. job에는 좌표·UID·object path를 넣지 않고 최소 count·hash·완료시각만 자체 retention까지 보존한다.

### 9. 위치 보존기간

Stage 1 제안값이며 실증 전 개인정보 영향평가와 기관 계약으로 확정한다.

- 정밀 원본 batch: 수신 후 기본 **30일**, 명시 동의와 검토가 있어도 최대 90일. Storage lifecycle rule로 만료한다.
- 마스킹된 route artifact가 필요한 경우: 기본 **90일**, 별도 derived Storage prefix와 manifest로 관리한다.
- Firestore trip summary: 서비스 관계 종료 또는 정책상 보존기간까지. 원본 위치가 없어도 재식별 위험이 있는 field는 최소화한다.
- Stage 2 익명 집계: 최대 **24개월**, 소수 사용자 셀 억제와 k-threshold를 적용한다.
- 동의 철회는 미래 수집과 pending forward recovery를 즉시 중단한다. 기존 artifact 삭제는 동의 문구·법적 보존과 별개일 수 있으므로 철회 자체를 자동삭제 trigger로 사용하지 않는다.
- 명시적 삭제 요청 또는 승인된 retention expiry가 있을 때만 Firestore metadata, Storage object/manifest, 활성화된 Stage 2 dataset을 generation/dataset lineage가 있는 deletion workflow로 추적한다.

`artifact_expires_at`은 receipt와 manifest에 기록하고 lifecycle policy drift를 모니터링한다. forward processing의 `reservation_deadline`은 그보다 앞선다. `purge_eligible_at`은 cleanup·감사 완료 전 null이며, 완료 transaction이 `receipt_retention_floor`와 audit window를 반영해 두 index와 receipt에 함께 설정한다. nested attempt, cleanup target과 integrity finding purge가 끝나기 전 receipt를 삭제하지 않는다. production 기간값은 개인정보 보존 승인과 staging 검증 전에는 확정된 운영값으로 주장하지 않는다. 삭제 완료 시 위치나 PII가 아닌 범위·건수·object generation만 deletion receipt로 남긴다.

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

현재 상태는 server-only `/consentStates/{derivedId}`에 다음과 같이 projection한다.

```text
tenant_id, person_id, purpose_code,
current_revision_id, status(granted|denied|withdrawn|expired),
effective_at, expires_at?, updated_at, revision
```

`derivedId = sha256(person_id + U+001F + purpose_code)` lowercase hex다. 새 revision과 현재 상태 전환은 같은 backend transaction에서 수행한다. telemetry authorization은 참조 revision뿐 아니라 current state가 같은 revision을 가리키며 현재 `precise_location` granted·미만료인지 함께 검사한다. 이전 granted revision이 남아 있어도 current state가 철회·교체됐으면 batch를 받지 않는다.

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
fact_type, value, unit?, effective_from, effective_to?,
source_refs[], calculation_version,
model_version?, confidence?, abstention_reason?,
content_hash, sensitivity, created_at, expires_at?
```

`/reportRuns/{reportRunId}` 및 `/claims/{claimId}`:

```text
reportRun: report_type, audience, generator_version,
prompt_hash, report_schema_version, validator_version,
fact_bundle_hash, fact_count, model_provider?, model_version?,
status, failure_class?, fallback_used,
artifact_path?, artifact_hash?, created_at, completed_at?

claim: claim_text, claim_type, fact_ids[],
validation_status, validation_codes[], created_at
```

근거 없는 주요 claim은 `unsupported`로 표시하고 최종본에서 제외하거나 `[확인 필요]`로 강등한다. report artifact가 크면 Storage에 두고 Firestore에는 상태와 hash만 둔다.

## 멱등성 계약

모바일 outbox와 비동기 worker는 최소 한 번 전송을 전제로 한다.

- 앱 설치마다 `installation_id`, 세션마다 `client_session_id`, batch마다 `client_batch_id`, sample마다 `client_sample_id`를 앱에서 생성한다.
- ingest idempotency key는 decoded request 값으로 계산한 `sha256(schemaVersion + U+001F + tenantId + U+001F + installationId + U+001F + clientBatchId)`의 lowercase hex다.
- Cloud Run은 첫 reservation에서 server UUIDv7 `batch_id`를 만들고, Firestore transaction으로 `/ingestIdempotency/{derivedKey}`, `/ingestClientBatches/{sha256(tenant_id + U+001F + client_batch_id)}`와 `/ingestReceipts/{batchId}`를 함께 create한다.
- 두 index는 `tenant_id`, 두 derived key, `receipt_id == batch_id`, `installation_id`, `client_batch_id`, `payload_schema_version`, `body_hash`, 생성시각, `receipt_retention_floor`, nullable `purge_eligible_at`의 최소 linkage를 함께 가진다. 일부 index나 receipt가 빠졌거나 linkage가 다르면 새 문서를 보완 생성하지 않고 dependency 오류로 닫는다.
- transaction callback은 authorization exact read를 index·receipt 조회보다 먼저 수행하고 retry마다 현재 snapshot을 다시 평가한다. callback 안에서는 UUID·시각 생성, Storage, 로그, 외부 호출을 금지한다. 자세한 상태 판정은 [ADR-0015](../decisions/ADR-0015-atomic-telemetry-admission.md)를 따른다.
- 동일 key/동일 body hash는 기존 receipt를 반환한다.
- 동일 key/다른 body hash는 `409 IDEMPOTENCY_CONFLICT`로 거절하고 audit event를 남긴다.
- Go Storage write는 `storage.Conditions{DoesNotExist: true}` precondition으로 overwrite를 막는다. 충돌 object 비교는 조건이 없는 새 handle로 generation·hash·size·CRC를 확인하며 `GenerationMatch: 0`을 직접 구성하지 않는다.
- 신규 receipt와 initial request lease는 같은 admission transaction에서 생성한다. existing pending replay는 active lease이면 Storage에 접근하지 않고, expired lease takeover는 fencing token을 증가시킨다.
- receipt 생성 후 object write 전에 실패한 상태를 sweeper가 복구할 수 있도록 명시적 상태 machine, current-consent recovery authorization과 retry lease를 둔다. raw가 없으면 sweeper는 payload를 재구성하지 않고 client replay를 기다린다.
- manifest가 있으면 version-aware inventory에서 generation candidate가 정확히 하나인지 먼저 확인한다. bytes가 동일해 보여도 복수 candidate면 자동 선택하지 않는다. 유일한 manifest generation을 pin하고 내부 raw generation을 exact read하며, manifest가 없을 때만 raw candidate generation을 발견 즉시 pin한다.
- 만료 전 forward reconciliation과 만료 후 cleanup을 분리한다. cleanup은 immutable target에 exact generation을 먼저 기록한 뒤 raw→manifest 순서로 조건부 삭제하며 latest path/prefix 삭제를 금지한다.
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

- 무인증, App Check, membership, tenant, device/trip/installation, consent authorizer 거부는 권한 확인 전이므로 idempotency index·receipt·Storage object를 만들지 않는다. 민감 scope를 포함하지 않는 분류 metric과 제한된 security audit만 남긴다.
- authorization 이후 reservation된 batch의 object content conflict 같은 terminal ingest failure만 receipt를 `rejected`로 전환하고 안정적인 reason code를 남긴다.
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
