# Legacy Data Inventory

## 목적과 경계

이 문서는 `power_assist_device_helper_backend`, `soo-ri`, `soo-ri-admin`에서 확인한 데이터 구조를 신규 프로젝트의 일회성 이관과 도메인 설계 참고자료로 정리한다.

- **확인된 사실**은 저장소의 모델, 타입, API 구현 또는 운영 문서에서 직접 확인한 내용이다.
- **신규 프로젝트 결정**은 기존 코드나 브랜드에 런타임 의존하지 않고 새 시스템에서 적용할 원칙이다.
- 기존 코드, 화면, API, Firebase 프로젝트는 신규 제품의 실행 의존성이 아니다. DB 형식과 적법하게 사용할 수 있는 수리 데이터만 변환 입력으로 취급한다.
- 이 문서는 실제 운영 DB의 상태를 증명하지 않는다. 운영 DB export를 받으면 `MIGRATION_GATES.md`의 프로파일링을 다시 수행해야 한다.

## 조사한 근거

| 영역 | 주요 근거 |
|---|---|
| MongoDB 모델 | `power_assist_device_helper_backend/lib/db/models/*.js` |
| 레거시 집계 관계 | `power_assist_device_helper_backend/playground-1.mongodb.js` |
| 웹 클라이언트 도메인 타입 | `soo-ri/src/domain/models/*.ts` |
| Firestore/Cloud Functions 구현 | `soo-ri-admin/functions/api.js`, `soo-ri-admin/functions/welfare/**` |
| 관리자 클라이언트 타입 | `soo-ri-admin/src/services/*.ts`, `soo-ri-admin/src/types/index.ts` |
| 이전 문서 | `soo-ri-admin/FIREBASE_MIGRATION.md`, `soo-ri-admin/DATA_STATUS.md` |

## 저장소별 역할

### `power_assist_device_helper_backend`

**확인된 사실**

- Next.js API Routes, Firebase Auth, MongoDB/Mongoose 조합의 과거 백엔드다.
- Mongoose 모델은 `users`, `vehicles`, `repairs`, `selfChecks`, `guardians`, `RepairStations`, `admins`를 정의한다.
- `prisma/schema.prisma`에는 PostgreSQL용 `Admin`, `User` 예시가 있으나, 조사한 도메인 API와 관계 모델은 Mongoose 구현에 있다. 이를 운영 도메인의 기준 스키마로 간주하지 않는다.
- 수리 집계 예시는 `repairs.vehicleId -> vehicles._id -> vehicles.userId -> users._id` 조인을 사용한다.

### `soo-ri`

**확인된 사실**

- 사용자/수리사 웹 클라이언트이며 사용자, 기기, 수리, 자가점검, 수리소, 복지 리포트 타입을 가진다.
- 수리와 자가점검은 배포 API의 데이터 계약을 전제로 하고, 복지 리포트는 Firestore의 `user_welfare_reports`를 직접 읽는 경로가 있다.
- 웹 모델의 기본값 처리 때문에 누락 필드가 빈 문자열, `0`, `false`, 현재 시각으로 보일 수 있다. 이 기본값을 원본 사실로 이관하면 안 된다.

### `soo-ri-admin`

**확인된 사실**

- 관리자 웹과 Cloud Functions가 함께 있으며 API와 Firestore 직접 조회가 혼재한다.
- 확인된 Firestore 컬렉션은 `users`, `vehicles`, `repairs`, `selfChecks`, `repairStationsLegacy`, `user_welfare_reports`, `welfare_tasks`다.
- 이전 문서에는 `repairStations`, `guardians`도 매핑 대상으로 적혀 있으나 현재 API는 수리소 목록을 `repairStationsLegacy`에서 읽는다.
- 이전 문서는 MongoDB→Firestore 이관을 “진행 중”으로 표시하고 있다. 문서의 완료 주장과 실제 데이터 완전성은 운영 export로 검증해야 한다.

## 확인된 레거시 엔터티

### 사용자 `users`

| 필드 | 확인된 의미 | 관찰된 위험 |
|---|---|---|
| Mongo `_id` / Firestore document ID | 내부 사용자 식별자 | 저장소 전환 후 값이 달라졌을 수 있음 |
| `firebaseUid` | Firebase Auth UID | Firestore document ID와 동일하다고 가정할 수 없음 |
| `name`, `phoneNumber` | 직접 식별정보 | 도메인 데이터와 같은 문서에 저장됨 |
| `role` | `user`, `admin`, `repairer`, `guardian` | 사람 유형과 시스템 권한이 한 필드에 혼합됨 |
| `recipientType` | 지원 대상 유형 | `수급`, `수급자`, `general`, `disabled`, `lowIncome` 등 구현/문서 간 값 불일치 |
| `supportedDistrict` | 지원 지역 | 서울 25개 구와 `서울 외`; 빈 문자열 fallback도 존재 |
| `smsConsent` | 문자 수신 여부 | 구체적 목적, 약관 버전, 동의 시각, 철회 이력이 없음 |
| `guardianIds` | 보호자 참조 목록 | ObjectId 또는 문자열 문서 ID일 수 있음 |
| `sensorId` | 과거 외부 GPS 센서 매핑 | 신규 시스템에서는 IoT 센서를 사용하지 않으므로 이관 대상 아님 |
| `createdAt`, `updatedAt` | 생성/수정 시각 | Mongo Date, Firestore Timestamp, 문자열이 혼재 가능 |

### 보호자 `guardians`

| 필드 | 확인된 의미 | 관찰된 위험 |
|---|---|---|
| `_id` | 보호자 문서 ID | 사용자 문서의 `guardianIds`와 양방향 정합성 확인 필요 |
| `name` | 보호자 이름 | 직접 식별정보 |
| `firebaseUid` | 인증 식별자 또는 입력값 | 모델 주석상 미가입 보호자도 있어 실제 Auth UID인지 불명확 |
| `userId` | 연결 사용자 ObjectId | 한 보호자가 여러 사용자를 보호하는 관계를 자연스럽게 표현하지 못함 |

### 기기 `vehicles`

| 필드 | 확인된 의미 | 관찰된 위험 |
|---|---|---|
| `_id` / document ID | 내부 기기 식별자 | API 경로의 `vehicleId`와 혼용됨 |
| `vehicleId` | QR 등에 쓰인 공개 고유 ID | Mongo에서 unique였으나 Firestore 전역 유일성은 export 검증 필요 |
| `userId` | 현재 소유 사용자 ID | 소유/사용 이력 없이 현재 값만 보존 |
| `model` | 기기 모델 | 자유 텍스트 |
| `purchasedAt` | 구매일 | Date/Timestamp/문자열/NULL 가능 |
| `manufacturedAt` | 제조일 | README/관리자 타입에는 `registeredAt`이 나타나 의미 충돌 |
| `vehicleType` | 기기 유형 | Cloud Functions 가입 경로에서만 확인됨 |

### 수리 `repairs`

| 필드 | 확인된 의미 | 관찰된 위험 |
|---|---|---|
| `_id` / document ID | 수리 ID | source별 ID namespace 필요 |
| `vehicleId` | 수리 대상 기기 참조 | Mongo에서는 `vehicles._id`, Firestore/API에서는 내부 document ID 또는 공개 `vehicleId`가 혼용됨 |
| `repairedAt` | 수리 시각 | 시간대와 정밀도 확인 필요 |
| `billingPrice` | 청구/수리 비용 | 통화, 부가세, 지원금/본인부담 구분 없음 |
| `isAccident` | 사고 수리 여부 | NULL 또는 문자열 오염 가능성은 export에서 검증 필요 |
| `repairStationCode`, `repairStationLabel` | 수리소 코드와 당시 명칭 | 참조와 snapshot을 구분하지 않은 비정규화 |
| `repairer` | 수리 기사 이름 | Mongoose 기본값이 boolean `true`로 선언된 타입 오류가 있음 |
| `repairCategories` | 수리 범주 배열 | `타이어`, `타이어 | 튜브`, `타이어&튜브` 등 표기 차이 가능 |
| `batteryVoltage` | 배터리 전압 | 미측정과 `0`이 구분되지 않을 수 있음 |
| `etcRepairParts` | 기타 부품/상세 | API/클라이언트에는 있으나 현재 Mongoose `Repairs.js`에는 없음 |
| `memo` | 메모 | 개인정보나 민감정보가 섞였을 가능성 |
| `status`, `troubleInfo`, `repairDetail`, `requestedAmount` | 관리자 타입에 나타난 업무 필드 | 실제 Firestore 저장 여부는 export로 확인 필요 |
| `createdAt`, `updatedAt` | 기록 시각 | `repairedAt`과 의미를 분리해야 함 |

### 자가점검 `selfChecks`

**확인된 사실**

- 기기 참조와 16개 boolean 관찰값으로 구성된다.
- 항목은 구동장치 2개, 전자제어 2개, 제동장치 2개, 타이어/튜브 2개, 배터리 2개, 시트 2개, 발걸이 2개, 프레임 2개다.
- 필드명은 `motorNoise`, `abnormalSpeed`, `batteryBlinking`, `chargingNotStart`, `breakDelay`, `breakPadIssue`, `tubePunctureFrequent`, `tireWearFrequent`, `batteryDischargeFast`, `incompleteCharging`, `seatUnstable`, `seatCoverIssue`, `footRestLoose`, `antislipWorn`, `frameNoise`, `frameCrack`이다.

**관찰된 위험**

- `break*`는 도메인상 `brake*`가 의도된 것으로 보이지만 기존 키를 임의로 수정해서는 안 된다.
- 미응답과 정상 응답이 모두 `false`로 합쳐졌을 수 있다.
- 점검 수행자, 수행 방식, 점검 버전, 판정/조치 이력이 없다.

### 수리소 `RepairStations` / `repairStationsLegacy`

| 필드 | 확인된 의미 | 관찰된 위험 |
|---|---|---|
| `code`, `label` | 수리소 코드/명칭 | source별 유일성 확인 필요 |
| `firebaseUid` | 수리소 인증자 | 계정이 없는 수리소도 있음 |
| `state`, `city`, `region`, `address` | 주소 구성 | 필드 의미와 행정구역 수준이 문서 예시에서 일관되지 않음 |
| `telephone` | 전화번호 | 기관 연락처이지만 접근 제한 필요 |
| `aid` | `[일반, 차상위, 수급]` 지원금 배열 | 위치 기반 배열은 의미가 취약하며 금액 단위/유효기간 없음 |
| `coordinate` | GeoJSON Point `[경도, 위도]` | 일부 API는 단순 배열도 허용 |

### 관리자 `admins`

- Mongo 모델은 수리소 ObjectId, `id`, 평문처럼 보이는 `password` 필드를 가진다.
- 신규 프로젝트로 비밀번호를 이관하지 않는다. 인증 공급자와 역할/기관 멤버십을 새로 구성한다.

### 복지 리포트와 작업

| 컬렉션 | 확인된 내용 | 신규 시스템 취급 |
|---|---|---|
| `user_welfare_reports` | 사용자별 summary/risk/advice/services/metadata, fallback 여부 | 과거 생성물 참고용. 모델 근거로 재사용하지 않음 |
| `welfare_tasks` | 비동기 리포트 생성 상태 | 새 작업 실행/감사 모델을 별도 설계 |

## ID 관계와 혼용 문제

확인된 레거시 관계는 다음과 같다.

```text
Firebase Auth UID
  └─ users.firebaseUid
       ├─ users._id / Firestore document ID
       │    ├─ vehicles.userId
       │    └─ guardians.userId
       └─ users.guardianIds -> guardians._id 또는 문서 ID

vehicles._id / Firestore document ID  <혼용>  vehicles.vehicleId(공개 QR ID)
  ├─ repairs.vehicleId
  └─ selfChecks.vehicleId
```

Cloud Functions의 기기 상세 조회는 URL 값을 먼저 Firestore document ID로 조회하고, 실패하면 `vehicles.vehicleId`로 다시 조회한다. 반면 수리·점검 조회는 URL 값을 그대로 `repairs.vehicleId`, `selfChecks.vehicleId`와 비교한다. 따라서 레거시 `vehicleId`는 이름만으로 참조 종류를 결정할 수 없다.

## 확인된 불일치와 이관 위험 목록

1. Mongo ObjectId, Firestore document ID, Firebase UID, 공개 기기 ID가 API 전반에서 혼용된다.
2. `recipientType` 코드 체계가 구현과 문서마다 다르다.
3. `manufacturedAt`과 `registeredAt`이 서로 대체된 흔적이 있다.
4. `repairCategories` 범주 표기가 여러 형태다.
5. Mongoose 수리 모델의 `repairer` 기본값은 문자열 필드에 boolean `true`다.
6. `etcRepairParts` 등 일부 필드는 모델, README, 클라이언트 사이에서 존재 여부가 다르다.
7. 자가점검 boolean은 미응답과 정상 상태를 구분하지 못한다.
8. 수리소 이름과 코드는 수리 레코드에 snapshot으로 중복되지만 정합성 규칙이 없다.
9. SMS 동의는 목적·버전·시각·철회 이력 없이 boolean 하나다.
10. 위치/이동거리 리포트는 과거 IoT `sensorId`와 외부 API에 의존한다. 이 경로는 신규 모바일 GPS 시스템으로 이관하지 않는다.
11. Firestore API 일부는 요청 본문을 스키마 검증 없이 spread하여 저장한다.
12. 관리자 인증, 통계, 수리소 저장 로직 일부가 placeholder/TODO다. 이 값을 운영 사실로 간주하지 않는다.

## 현재 작업공간의 데이터 파일 상태

- **실제 수리 이력 전체 export(문서에서 언급된 약 550건 포함)는 현재 작업공간에 없다.**
- **신규 모바일 기기에서 수집한 원본 GPS 위치 샘플도 현재 작업공간에 없다.**
- 운영 MongoDB/Firestore snapshot, 레코드 수 manifest, 데이터 사전도 없다.
- `soo-ri-admin/functions/data`에는 과거 외부 센서 매핑 CSV와 복지 서비스 CSV 등이 있으나, 이는 수리 원본 export나 신규 모바일 GPS 원본이 아니다. 외부 센서 매핑은 신규 시스템의 수집원으로 사용하지 않는다.

따라서 현재 단계에서 가능한 것은 스키마 수준의 매핑 설계까지다. 실제 레코드 이관 가능성, 모델 학습 적합성, 결측률은 export 입수 후에만 확정한다.

## 신규 프로젝트 결정

- 레거시 DB를 실시간 조회하거나 레거시 API를 proxy하지 않는다.
- 레거시 ID는 `legacy_id_crosswalk`에 source namespace와 함께 보관하고, 신규 내부 ID로 대체한다.
- 과거 외부 GPS `sensorId`는 이관하지 않는다. 모바일 `installation_id`와 주행 세션이 새 수집 경계다.
- 이름, 전화번호, 상세 주소 같은 개인정보는 코어 도메인 테이블에서 분리한다.
- 레거시 리포트의 텍스트와 건강점수는 사실/라벨로 사용하지 않는다.
- 누락값을 `0`, `false`, 현재 시각으로 임의 보정하지 않는다. `unknown`, NULL 또는 quarantine으로 남긴다.
- 실제 변환과 수용 기준은 `MIGRATION_GATES.md`, 신규 정규 모델은 `TARGET_DOMAIN_MODEL.md`를 따른다.
