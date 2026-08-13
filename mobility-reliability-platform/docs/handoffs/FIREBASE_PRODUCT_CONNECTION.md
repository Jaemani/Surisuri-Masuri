# Firebase 제품 연결 handoff

## 현재 경계

`services/domain-command`가 Firestore 쓰기의 유일한 제품 command 경계다. 모바일·복지관 콘솔은 Firestore에 수리·지원금 문서를 직접 쓰지 않는다.

### Command endpoints

| 함수 | 용도 | HTTP body |
| --- | --- | --- |
| `createRepairRequest` | 사용자·보호자·복지관 수리 접수 | `tenantId`, `beneficiaryId`, `deviceId`, `issueSummary`, `publicFundingInvolved`, `requestedAmountKrw?` |
| `transitionRepairRequest` | 복지관 배정·수리사 작업·센터 검증·완료 | `tenantId`, `repairRequestId`, `toStatus`, `expectedRevision` 및 아래 상태별 exact field |
| `appendSubsidyTransaction` | 지원금 배정·예약·집행·해제·반전·조정 | `tenantId`, `accountId`, `personId`, `policyVersionId`, `transactionType`, `amountKrw`, `reasonCode`, `workOrderId?` |

공통 header:

```text
Authorization: Bearer <Firebase ID token>
X-Firebase-AppCheck: <App Check token>
Idempotency-Key: <8~128자 stable key>
Content-Type: application/json
```

상태별 추가 field는 다음과 같이 제한된다. 다른 상태의 field를 섞으면 `UNEXPECTED_COMMAND_FIELD`로 거부한다.

- 배정: `repairStationId`, `repairerFirebaseUid`, `note?`
- 일정 확정: `scheduledAt`
- 작업 시작: 추가 field 없음
- 수리사 제출: `billedAmountKrw`, `workItems[]`; `submittedAt`은 서버가 생성
- 수정 요청: `note?`
- 센터 검증: `subsidyDecisionId`, 공적 지원 건의 `subsidyAccountId`, `note?`
- 완료·재개·거절·취소: `note?`

`workItems`는 `categoryCode`, `actionCode`, `quantity`, `lineAmountKrw`만 허용하며 항목 합계가 청구액과 일치해야 한다. 완료되면 `/repairs/{repairId}/items`에 검증 이력으로 함께 기록된다.

### Read projection endpoints

모바일과 콘솔 UI는 다음 purpose-limited projection endpoint를 통해 서버가 조합한 DTO만 읽는다.

- `GET /getMobileProductSnapshot`
- `GET /getConsoleOperationsSnapshot?projection=<name>`

두 endpoint는 구현됐지만 production에는 아직 배포되지 않았다. repository adapter는 endpoint, Firebase ID token, App Check token을 dependency injection으로 받으며, 하나라도 빠지면 `NOT_CONFIGURED`로 fail closed한다. 기본 local preview는 실제 Firebase 설정 전 deterministic demo를 명시적으로 사용한다.

모바일 projection은 역할별 union이다. beneficiary는 `repairRequest/device/subsidy`, repairer는 자신에게 배정된 `repairJobs`만 받는다. 수리사 job에는 status, revision, 공개 기기정보, 구조화 수리 항목과 서버 파생 allowed action이 포함되며 PII·지원금 잔액·UID·GPS는 없다. 콘솔은 `dashboard|users|devices|repairs|ledger|inspections|partners|reports|services` 중 하나를 요구하며 `X-Tenant-Id`를 보낸다.

## 저장 계약

- `repairWorkOrders/{workOrderId}`와 `statusHistory`
- 완료 `repairs/{repairId}`와 구조화 `items/{itemId}`
- `subsidyAccounts/{accountId}/transactions/{transactionId}`
- `domainEvents/{eventId}`
- `commandIdempotency/{derivedKey}`

Firestore 문서는 snake_case이고 모바일/HTTP wire만 camelCase다. Rules를 우회하는 Admin SDK command가 tenant 상태, membership 유효기간, 사람·기기 배정, 보호자 관계, 수리사 배정, 지원금 계정 범위를 직접 검증한다.

## 환경 상태

- 검증: WSL2 local Firestore Emulator, synthetic fixture
- production Firebase project: 미연결
- native Android/iPhone auth/App Check: 미검증
- 현장 데이터·사용자: 미연결
- guardian 대상 선택·관계 projection: 미구현, fail closed
- cross-organization repair grant: 미구현, fail closed

### R12 report persistence 경계

`reportRuns/{reportId}`와 하위 `claims/{claimId}`의 client read Rules는 same-tenant parent, exact path/field ID와 Fact bundle hash 결합을 검사하고 direct client write를 차단한다. 이는 defense-in-depth Rules뿐이다. Admin SDK writer, report worker, 실제 Firestore persistence, 사람 승인·발행 receipt와 production 배포는 구현되지 않았다. Local lifecycle과 콘솔은 deterministic synthetic fixture를 사용한다.

현재 정확한 경계는 [CURRENT_STATUS](./CURRENT_STATUS.md)와 [R12 EVD-030~032](../evidence/2026-08-product.md#evd-20260813-030--report-claim-tenantparent-binding)을 따른다.

## 다른 환경에서 이어받기

```bash
rtk pnpm install --frozen-lockfile
rtk pnpm --filter @mobility-reliability/domain-command test
rtk pnpm --filter @mobility-reliability/domain-command test:emulator
rtk pnpm --filter @mobility-reliability/domain-command build
```

production 연결 전 실제 Firebase project ID, Functions region/base URL, Android/iOS App Check provider와 복지관 tenant fixture를 별도 환경변수/secret으로 공급한다. 토큰과 실제 project config를 Git에 넣지 않는다.
