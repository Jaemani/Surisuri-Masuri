# Migration Gates

## 목적과 현재 상태

기존 MongoDB/Firestore 형식과 적법하게 사용할 수 있는 수리 이력을 신규 Firebase-first hybrid 시스템으로 **일회성 변환**하기 위한 승인 게이트다. 기존 서비스와의 실시간 동기화나 레거시 런타임 호환은 목표가 아니다.

현재 작업공간에는 문서에서 언급된 전체 수리 원본 export와 신규 모바일 GPS 원본이 없다. 따라서 변환 계약은 설계할 수 있지만, 이관 완료나 약 550건의 품질을 주장할 수 없다.

Stage 1 target:

- Firebase Auth + App Check
- Firestore tenant control plane
- Cloud Storage 압축 GPS batch/object manifest
- Go Cloud Run ingest
- Firestore ingest receipt/current projection
- Firebase Emulator Suite/Security Rules 자동검증

BigQuery는 Stage 2 선택 항목이며 PostgreSQL/PostGIS는 Stage 3 escape hatch다. 둘 다 이관 완료의 기본 선행조건이 아니다.

## 운영 원칙

- 각 게이트는 산출물, 자동 검사 결과, 승인자를 남긴다.
- 실패 레코드는 `accepted`, `quarantined`, `rejected`로 구분하며 조용히 버리거나 기본값으로 채우지 않는다.
- 원본 snapshot은 읽기 전용 hash를 고정하고 변환은 반복 실행 가능해야 한다.
- source ID와 신규 UUIDv7의 crosswalk를 보존한다.
- 이름, 전화번호, 메모, 위치는 fixture와 로그에서 마스킹한다.
- 레거시 비밀번호, 외부 IoT `sensorId`, 과거 LLM 점수/리포트는 운영 데이터로 이관하지 않는다.
- **GPS sample별 Firestore document write 또는 sample subcollection 생성을 금지한다.**

## Gate 0 — 권한·범위 승인

**필수 산출물**

- 사용자, 기기, 수리, 점검, 기관 데이터별 소유/사용 권한 확인서
- 기관별 개인정보 처리 역할, 이관 목적, 보존기간, 삭제 책임
- source system 목록과 이관 기준시각
- 포함/제외 collection·field 목록

**통과 조건**

- 수리 데이터를 신규 운영 및 모델 평가에 사용할 범위가 문서화되어야 한다.
- 위치·연락처·동의 기록은 적법 근거가 불명확하면 제외한다.
- 관리자 password와 외부 IoT 센서 ID는 제외 목록에 있어야 한다.

## Gate 1 — 재현 가능한 원본 snapshot

**필수 산출물**

- 각 collection/table의 원본 export
- `source_manifest.json`: source, export 시각, query/범위, record count, byte size, SHA-256, exporter version
- export 실패/재시도 로그
- 암호화된 quarantine Storage bucket/prefix와 접근자 목록

**통과 조건**

- manifest record count와 파일 파싱 count가 100% 일치한다.
- 모든 파일 hash가 재검증된다.
- 원본 object에 retention/권한이 설정되고 변환은 파생 prefix에서 수행한다.
- Firestore Timestamp, Mongo Extended JSON, ObjectId를 손실 없이 읽는다.
- object name과 custom metadata에 PII가 없다.

현재 상태: **BLOCKED BY INPUT**. 전체 수리/점검/기기/사용자 export가 작업공간에 없다. 데이터 이관 실행만의 선행조건이며 신규 개발 전체의 차단은 아니다.

## Gate 2 — 스키마·분포 프로파일링

collection마다 다음을 산출한다.

- 필드 출현률, NULL/빈 문자열/0/false 분포
- 실제 타입과 파싱 실패 표본
- distinct count와 중복 후보
- 날짜 최소/최대, 미래·역전 시각
- 금액 최소/최대/음수/비정상치
- 범주값과 빈도
- 자유 문자열 PII/민감정보 scan
- 참조 대상 존재율

| 대상 | 필수 검사 |
|---|---|
| 사용자 | Mongo `_id`, Firestore doc ID, `firebaseUid`, 전화번호 충돌 |
| 기기 | document ID와 공개 `vehicleId` 유일성, 소유자 참조, 날짜 의미 |
| 수리 | `vehicleId`가 내부 ID인지 공개 ID인지 후보별 resolve율 |
| 점검 | 16개 field 출현률, boolean 외 타입, 미응답 표현 |
| 수리소 | `code` 중복, label/address 충돌, 좌표 순서/범위 |

**통과 조건**

- machine-readable profile과 입력 manifest hash가 연결된다.
- 레거시 UI default와 원본값을 구분한다.
- 미해결 의미 충돌을 mapping open issue로 등록한다.

## Gate 3 — 코드와 의미 매핑 승인

`mapping_version`이 있는 다음 표를 승인한다.

- `recipient_type_mapping`
- `repair_category_mapping`
- `role_mapping` → tenant membership role
- `inspection_item_mapping` → versioned observation code
- `date_field_mapping`: `manufacturedAt`, `registeredAt`, `purchasedAt`
- `repair_field_mapping`: 금액, 상태, 기타 부품/상세

**통과 조건**

- 모든 distinct source code가 `mapped`, `deprecated`, `unknown/quarantine` 중 하나다.
- 의미가 불명확한 값을 추측해 합치지 않는다.
- `false`가 필수 응답이었다는 근거가 없으면 정상 대신 `not_observed`로 처리한다.
- 날짜 field 이름만으로 제조일/등록일을 바꾸지 않는다.

## Gate 4 — UUID/public code/crosswalk

신규 내부 ID는 UUIDv7, 현장 QR/표시값은 별도 `public_code`다. `/tenants/{tenantId}/externalIdentifiers/{crosswalkId}`는 server-only로 적재한다.

```text
source_system, source_collection, source_id_type, source_id,
target_entity_type, target_id, match_method, confidence,
source_snapshot_hash, mapping_version, reviewed_by?, reviewed_at?
```

기기 resolve 순서:

1. source namespace + 내부 document/ObjectId 정확 일치
2. source 안에서 유일성이 검증된 `vehicleId` 정확 일치
3. 사용자/모델/날짜 후보 — 자동 merge 금지, 사람 검토

**통과 조건**

- source ID 하나가 둘 이상의 target으로 연결되는 경우 0건.
- 서로 다른 사람/기기를 전화번호나 public code만으로 자동 병합한 경우 0건.
- `public_code` tenant 내 중복 0건.
- 미해결 수리/점검 대상은 quarantine한다.
- crosswalk client read/write는 Security Rules로 거절된다.

## Gate 5 — Firestore target shape와 참조 무결성

**필수 검사**

- 모든 document가 `/tenants/{tenantId}/...`의 승인된 collection path에만 생성된다.
- document path tenant와 server-managed `tenant_id`가 일치한다.
- accepted 수리/점검은 같은 tenant의 존재하는 기기를 참조한다.
- device assignment의 주사용 기간이 겹치지 않는다.
- repair item의 부품은 같은 device/tenant에 속한다.
- 날짜 관계(`ended_at >= started_at`, `removed_at >= installed_at`)가 유효하다.
- document 크기와 indexed field 수가 사전 상한 이하다.

**통과 조건**

- cross-tenant 참조 0건.
- hard invariant 위반 0건.
- orphan을 가상의 “unknown device”로 묶지 않는다.
- raw GPS sample, sample 배열, 전체 geometry가 Firestore에 생성된 건수 **0건**.

## Gate 6 — PII·동의·server-only field 분리

**변환 검사**

- 이름, 전화번호, 상세 주소는 `/privatePeople/{personId}` 같은 server-only PII path에만 적재한다.
- 일반 `people`, devices, repairs, telemetry manifest, logs, Crashlytics, FCM에 직접 식별 문자열이 없는지 scan한다.
- 전화번호 E.164 정규화와 원문 보존 필요 여부를 분리한다.
- 메모는 자동 탐지+사람 검토 후 `memo_redacted`만 적재한다.
- 레거시 `smsConsent=true`는 목적/증거/버전이 없으면 신규 포괄 동의로 승격하지 않는다.
- 정밀 위치 목적의 active consent revision이 없으면 ingest를 거절한다.

**Security Rules 통과 조건**

- client의 `privatePeople`, `ingestReceipts`, `domainEvents`, `deadLetters`, `externalIdentifiers`, projection checkpoint direct write가 모두 거절된다.
- client가 `tenant_id`, membership role/status, projection/model field, object path/hash를 변경하려는 요청이 모두 거절된다.
- 본인 정보 API는 backend가 필요한 최소 PII만 반환한다.

## Gate 7 — 결정적 control-plane import

**필수 산출물**

- versioned mapping/config
- Admin SDK/BulkWriter 기반 import 도구와 artifact digest
- input manifest hash → output manifest hash lineage
- record별 disposition과 reason code
- import batch ID와 Firestore write 결과

**통과 조건**

- import 도구는 Firebase client SDK나 관리자 UI로 실행하지 않는다.
- 동일 input+mapping version 재실행 결과의 canonical hash가 동일하다.
- 동일 crosswalk ID와 target UUID를 재사용해 duplicate document를 만들지 않는다.
- NULL/누락을 `0`, `false`, 현재시각으로 채운 record가 0건.
- 수리 category 배열이 repair item 문서로 손실 없이 펼쳐진다.
- partial failure 재시도 후 중복 document 0건.

## Gate 8 — Storage GPS batch/manifest/receipt 계약

신규 모바일 GPS 수집과 이관 테스트 fixture 모두 다음 경계를 지켜야 한다.

**필수 흐름**

1. 모바일은 SQLite outbox에서 제한 크기 batch를 만든다.
2. Go Cloud Run이 Firebase ID token, App Check, membership, consent를 검증한다.
3. 서버가 payload schema, sample sequence/time/range, tenant를 검증한다.
4. Storage에 `{batchId}.ndjson.zst`를 generation precondition으로 생성한다.
5. immutable `{batchId}.manifest.json`을 생성한다.
6. Firestore `/ingestReceipts/{batchId}`에 path/hash/status/count만 기록한다.
7. 비동기 projector가 `/trips/{tripId}` summary/current projection을 제한된 write로 갱신한다.

**필수 검사**

- batch 압축 해제 후 sample count와 manifest count 일치.
- body hash, object SHA-256, object generation, manifest path, receipt 값 일치.
- object path/metadata에 이름·전화·Firebase UID·public QR code가 없음.
- 동일 idempotency key/body hash 재전송은 기존 receipt 반환.
- 동일 key/다른 body는 `409 IDEMPOTENCY_CONFLICT`.
- receipt 생성과 object write 사이 장애를 sweeper가 복구.
- 만료 object가 lifecycle policy로 삭제되고 receipt/deletion workflow가 이를 추적.

**통과 조건**

- GPS sample별 Firestore write **0건**.
- Firestore receipt는 batch당 최대 1개 기본 문서, trip projection은 설정된 coalescing 한도 이내다.
- Storage object/manifest/receipt lineage 불일치 0건.
- retry/concurrent upload 후 duplicate object/receipt 0건.

## Gate 9 — 멱등성·event replay·DLQ

**멱등성**

- ingest key는 `sha256(tenant_id | installation_id | client_batch_id | payload_schema_version)`다.
- 수리/점검/동의 command도 `Idempotency-Key + body_hash` receipt를 가진다.
- 같은 key/같은 body와 같은 key/다른 body를 concurrency test한다.

**재처리**

- control event와 Storage object manifest에서 shadow projection을 재구축한다.
- replay 전후 trip summary, device state, accepted/rejected count의 canonical hash를 비교한다.
- replay mode에서 FCM/SMS/외부 API 호출을 금지한다.
- projector checkpoint/version과 `replay_run_id`를 기록한다.

**DLQ**

- retry 소진 event/batch는 `/deadLetters/{id}`에 receipt/event ID, object generation, processor version, error class, attempt count를 기록한다.
- DLQ 재처리는 원본 object/event를 수정하지 않고 새 run과 결과를 연결한다.

**통과 조건**

- 중복 domain result 0건.
- replay count/hash unexplained delta 0건.
- replay 중 외부 side effect 0건.
- DLQ 누락 0건, reason 없는 rejection 0건.

## Gate 10 — 정량 reconciliation

source별 다음 식을 만족해야 한다.

```text
source_count = accepted_count + quarantined_count + rejected_count
```

**검사 항목**

- 사용자/기기/수리/점검 record count
- 연월별·범주별 수리 건수
- 수리비 합계/중앙값/범위
- 기기별 첫/마지막 수리일
- 사용자/기기/수리소 distinct count
- code mapping 전후 분포
- Firestore target collection별 document count
- repair header와 item count의 관계
- import manifest의 write success/retry/failure count

**수용 기준**

- 설명되지 않은 record 손실 0건.
- accepted 수리의 device resolve 100%.
- mandatory timestamp parsing 실패가 accepted에서 0건.
- accepted 금액의 NaN/무한/설명 없는 음수 0건.
- 금액 합계의 unexplained delta 0원.
- 임계치를 맞추기 위해 불량 record를 삭제하지 않고 quarantine 비율/reason을 보고한다.

## Gate 11 — Emulator Suite와 Security Rules

운영 Firebase 접근 전에 Emulator Suite를 기본 검증 환경으로 사용한다.

**fixture**

- tenant A/B
- beneficiary/guardian/case worker/repairer/admin/auditor
- active/revoked/expired membership
- 본인/타인 person/device
- 정상/철회/만료 consent
- server-only field가 포함된 악성 request

**필수 Rules test**

- tenant A token으로 tenant B의 모든 control collection read/write 거절.
- 비회원, revoked/expired membership 접근 거절.
- client의 membership/role/tenant 변경 거절.
- `privatePeople`, receipt, prediction raw field, event, DLQ, crosswalk direct write 거절.
- 허용 role의 최소 read와 허용 field만 포함한 draft create 성공.
- field 추가 공격, nested map 변경, server timestamp 위조 거절.
- Storage Rules가 telemetry raw object의 client direct read/write를 거절.

**통과 조건**

- Rules unit/integration test 100% 통과.
- cross-tenant read/write 성공 0건.
- 예상 deny/allow matrix가 문서와 test 이름으로 연결된다.
- emulator export/seed가 PII 없는 합성 fixture만 포함한다.
- Rules 배포는 test artifact hash와 review 승인이 있어야 한다.

## Gate 12 — App Check·운영 보안·비용 경계

**필수 테스트/설정**

- Android/iOS/Web App Check enforcement와 통제된 개발 token
- Cloud Run의 ID token/App Check/membership 교차검증
- service account 최소권한, Storage prefix 권한, secret rotation
- Crashlytics/Cloud Logging PII·좌표 scan
- Firestore/Storage usage dashboard와 GCP budget alert
- Cloud Run min instances `0`, concurrency/memory/timeout 상한
- Storage lifecycle drift와 deletion job monitoring

**통과 조건**

- client payload tenant/role을 조작해도 권한 상승 0건.
- raw object client direct access 성공 0건.
- 로그/Crashlytics의 PII·정밀 좌표 검출 0건.
- GPS sample 수에 비례한 Firestore write가 없음이 usage test에서 확인된다.
- 비용 부하 테스트 결과가 승인된 월간 상한 안에 있다.

## Gate 13 — 표본 검증과 도메인 승인

**표본 구성**

- 무작위 레코드와 각 수리 범주
- 배터리 전압/기타부품/사고/고액 수리 edge case
- 기기 ID 혼용 후보
- 같은 날 복수 수리, 소유자 변경, NULL 날짜
- 점검 16개 항목 true/false/누락 조합

**통과 조건**

- 복지관/수리 도메인 담당자가 원본과 신규 document/UI를 대조한다.
- 표본 크기와 오류 허용치는 실제 export 규모 확인 후 test plan에 고정한다.
- 오류 발견 시 mapping version을 올리고 전체 dry-run을 재실행한다. 운영 document를 개별 patch해 숨기지 않는다.

## Gate 14 — 모델 학습 적합성

이관 성공은 모델 학습 가능성을 뜻하지 않는다.

- 기기별 관측 시작/종료(censoring) 정의
- 부품 교체/수리 구분과 표준 범주 mapping률
- 수리 전 누적 주행거리와 시간축 결합
- 동일 기기 반복 수리와 소유 변경 처리
- 시간 분할과 누출 방지
- 부품별 긍정 사건 수

**Stage 1 통과 조건**

- 승인된 Storage manifest만 feature snapshot 입력으로 사용한다.
- dataset manifest에 code commit, schema/mapping version, object hash, 포함/제외 기준을 기록한다.
- 사건 수가 부족하면 `data_insufficient` 또는 규칙 baseline으로 둔다.
- 과거 LLM 리포트/건강점수를 ground truth로 쓰지 않는다.

**Stage 2 BigQuery를 활성화하는 경우 추가 gate**

- `DATE(recorded_at)` partition, `tenant_id/device_id` clustering 적용.
- partition filter required, query bytes 상한, budget alert 설정.
- Storage manifest→load job→row count/reject count lineage 보존.
- time-split dataset 재현성과 delete job 검증.
- authorized view/row access test 통과.

BigQuery는 이 요구가 실제로 생기기 전에는 활성화하지 않는다.

## Gate 15 — production import와 rollback

1. 최종 source snapshot/hash 고정.
2. Emulator/local dry-run 후 별도 Firebase staging project에 전체 import.
3. Gate 2~14 재실행.
4. 승인 output manifest만 production project에 Admin SDK로 import.
5. production read-only count/hash/Rules smoke test.
6. 문제 시 import batch를 비활성화하고 이전 projection pointer로 rollback.
7. crosswalk/import receipt는 server-only로 보존.
8. 필요한 감사·보존 후 레거시 credential/service account 폐기.

**완료 조건**

- production import batch ID, source/output hash, Firebase project ID, 승인자, 시각이 기록된다.
- 신규 서비스가 레거시 API/DB/Firebase 환경 없이 기동한다.
- quarantine 처리 계획과 책임자가 정해진다.
- 데이터 보존/삭제 runbook이 승인된다.
- PostGIS 연결 없이 Stage 1 기능이 동작한다.

## Stage 3 PostGIS escape hatch

PostgreSQL/PostGIS는 migration 기본 gate가 아니다. BigQuery GIS가 측정된 공간 운영 SLA/비용을 충족하지 못할 때만 새 ADR과 별도 이관 계획을 만든다. 도입 전에는 PostGIS schema, RLS, dual-write를 선제적으로 구현하지 않는다.

## 필수 산출물 체크리스트

- [ ] 권한·범위 확인서
- [ ] source/export manifest와 SHA-256
- [ ] field/type/profile 보고서
- [ ] versioned code mapping
- [ ] legacy ID crosswalk
- [ ] PII/consent 분리 검사
- [ ] Firestore target shape/import manifest
- [ ] Storage GPS object/manifest/receipt lineage test
- [ ] idempotency/replay/DLQ test
- [ ] reconciliation/quarantine 보고서
- [ ] Emulator Suite/Security Rules test artifact
- [ ] App Check/비용/TTL/deletion test
- [ ] 모델 dataset card 또는 `data_insufficient` 결정
- [ ] production import receipt와 rollback 기록

## 금지 사항

- 운영 DB를 직접 수정하며 변환 규칙을 맞추는 행위
- source가 다른 ID를 namespace 없이 병합하는 행위
- 누락 날짜를 import 시각으로 채우는 행위
- 미응답 boolean을 정상으로 확정하는 행위
- 레거시 `smsConsent`를 정밀 위치/모델 동의로 확대하는 행위
- tenant/role/server field를 client payload에서 신뢰하는 행위
- GPS sample별 Firestore document 또는 subcollection을 생성하는 행위
- Storage path/metadata/log에 PII나 public QR code를 넣는 행위
- replay 중 FCM/SMS/외부 API side effect를 재실행하는 행위
- quarantine record를 통계에서 숨기는 행위
- 필요가 입증되기 전에 BigQuery/PostGIS를 1차 운영 의존성으로 추가하는 행위
