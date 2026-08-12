# Firebase 제품 연결 handoff

## 현재 경계

`services/domain-command`가 Firestore 쓰기의 유일한 제품 command 경계다. 모바일·복지관 콘솔은 Firestore에 수리·지원금 문서를 직접 쓰지 않는다.

### Command endpoints

| 함수 | 용도 | HTTP body |
| --- | --- | --- |
| `createRepairRequest` | 사용자·보호자·복지관 수리 접수 | `tenantId`, `beneficiaryId`, `deviceId`, `issueSummary`, `publicFundingInvolved`, `requestedAmountKrw?` |
| `transitionRepairRequest` | 복지관 배정·수리사 제출·센터 검증·완료 | `tenantId`, `repairRequestId`, `toStatus`, `expectedRevision` 및 상태별 필수 field |
| `appendSubsidyTransaction` | 지원금 배정·예약·집행·해제·반전·조정 | `tenantId`, `accountId`, `personId`, `policyVersionId`, `transactionType`, `amountKrw`, `reasonCode`, `workOrderId?` |

공통 header:

```text
Authorization: Bearer <Firebase ID token>
X-Firebase-AppCheck: <App Check token>
Idempotency-Key: <8~128자 stable key>
Content-Type: application/json
```

### Read projection endpoints

모바일과 콘솔 UI는 다음 purpose-limited projection endpoint를 통해 서버가 조합한 DTO만 읽는다.

- `GET /getMobileProductSnapshot`
- `GET /getConsoleOperationsSnapshot?projection=<name>`

두 endpoint는 구현됐지만 production에는 아직 배포되지 않았다. repository adapter는 endpoint, Firebase ID token, App Check token을 dependency injection으로 받으며, 하나라도 빠지면 `NOT_CONFIGURED`로 fail closed한다. 기본 local preview는 실제 Firebase 설정 전 deterministic demo를 명시적으로 사용한다.

모바일 projection은 역할별 union이다. beneficiary는 `repairRequest/device/subsidy`, repairer는 `repairJobs`만 받는다. 콘솔은 `dashboard|users|devices|repairs|ledger|inspections|partners|reports|services` 중 하나를 요구하며 `X-Tenant-Id`를 보낸다.

## 저장 계약

- `repairWorkOrders/{workOrderId}`와 `statusHistory`
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

## 다른 환경에서 이어받기

```bash
pnpm install
pnpm --filter @mobility-reliability/domain-command test
pnpm --filter @mobility-reliability/domain-command test:emulator
pnpm --filter @mobility-reliability/domain-command build
```

production 연결 전 실제 Firebase project ID, Functions region/base URL, Android/iOS App Check provider와 복지관 tenant fixture를 별도 환경변수/secret으로 공급한다. 토큰과 실제 project config를 Git에 넣지 않는다.
