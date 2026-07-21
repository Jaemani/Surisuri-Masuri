# ADR-0014: Firestore client 직접 조회를 본인·운영 역할로 제한

- 상태: accepted
- 결정일: 2026-07-21
- 관련 결정: [ADR-0007](./ADR-0007-firebase-first-hybrid.md), [ADR-0011](./ADR-0011-domain-command-worker-boundaries.md), [ADR-0013](./ADR-0013-telemetry-authorization-snapshot.md)

## 맥락

초기 Firestore Rules는 active membership이면 같은 tenant의 people, 관계, 기기 배정, trip, 동의, alert, prediction, report 등을 모두 읽을 수 있었다. 복지관 tenant에는 여러 서비스 대상자가 포함되므로 beneficiary 한 명의 로그인만으로 다른 사람의 민감 기록을 조회할 수 있는 구조다. ingest authorizer가 원본 GPS write를 보호해도 일반 Firebase client read는 별도 경계이므로 이를 보완하지 못한다.

Firestore Rules에서 기기와 사람의 관계를 여러 문서 query로 임의 결합하면 Rules 비용·query constraint·schema 결합이 커진다. guardian, 수리사, auditor의 접근은 관계·기관 간 grant·목적·감사 기록도 필요해 단순 role 하나로 직접 읽기를 허용하기 어렵다.

## 검토한 선택지

1. active tenant membership이면 tenant 내 domain projection을 모두 읽게 유지한다.
2. 모든 domain read를 client에서 막고 backend API만 사용한다.
3. 본인 person reference가 문서에 직접 있는 제한된 collection만 owner read를 허용하고, 기관 운영 문서는 최소 staff role에만 허용하며 복잡한 목적 기반 조회는 backend DTO로 분리한다.

## 결정

선택지 3을 채택한다.

### 공통 membership gate

- Firebase 인증뿐 아니라 `/tenants/{tenantId}`가 존재하고 `tenant_id`가 path와 같으며 `status == active`여야 한다.
- `/memberships/{uid}`도 path/field UID·tenant가 일치하고 active·유효기간 내이며 canonical `roles[]`를 가져야 한다.
- tenant document는 active member의 단건 `get`만 허용하고 tenant `list`는 거절한다.
- membership은 자신의 document 단건 `get`만 허용하고 list·write는 거절한다.

### 본인 person 범위

다음 문서는 `membership.person_id`가 document의 person reference와 같을 때 본인 read를 허용한다.

| collection | 본인 판정 |
| --- | --- |
| `people/{personId}` | path `personId` |
| `personRelationships/{id}` | `from_person_id` 또는 `to_person_id` |
| `deviceAssignments/{id}` | `person_id` |
| `trips/{id}` | `person_id` |
| `consentRevisions/{id}` | `person_id` |
| `alerts/{id}` | `person_id` |

`people`은 path와 resource의 person ID가 모두 일치하는 단건 get만 허용하고 list는 거절한다. 나머지 person-scoped list/query는 Firestore가 조건을 증명할 수 있도록 `tenant_id + person_id` filter가 필요하다. 앱은 filter 없는 전체 tenant query를 시도하지 않는다.

### 기관 운영 역할

초기 direct operational staff role은 `case_worker`, `tenant_admin` 두 개로 제한한다. 이 role은 위 person-scoped collection과 다음 운영 collection을 읽을 수 있다.

```text
devices/state, componentInstallations,
repairs/items, inspections/observations,
modelPredictions, evidenceFacts, reportRuns/claims
```

`partCatalog`는 민감정보가 없는 tenant catalog로 보고 모든 active member에게 read를 허용한다. 모든 client mutation은 계속 backend command 뒤에 둔다.

### 직접 허용하지 않는 관계

- `guardian`은 membership role만으로 다른 person 문서를 읽지 않는다. guardian-to-beneficiary relationship, 목적, 유효기간을 확인하는 backend DTO가 필요하다.
- `repairer`는 복지관 tenant의 직접 member나 광범위 reader가 아니다. server-only `dataAccessGrant`를 검사하는 Domain Command API를 사용한다.
- `auditor`는 민감 원문 전체가 아니라 목적 제한·필드 최소화·감사 로그가 있는 export/DTO를 사용한다.
- beneficiary가 person reference가 없는 device·repair·prediction을 읽으려면 backend가 assignment와 목적을 확인한 안전한 DTO를 반환한다.

### Server-only 경계

`privatePeople`, app installation, current consent state, ingest receipt/index, domain event, DLQ, external ID, access grant 등 기존 server-only collection의 client read/write 차단은 유지한다. Firebase Admin SDK가 Rules를 우회한다는 사실은 backend 자체 authorization과 field allowlist를 생략하는 근거가 아니다.

## 결과

- active beneficiary가 같은 복지관의 다른 사용자를 열람하는 경로를 Rules에서 차단한다.
- suspended·closed tenant는 membership document가 active여도 client read를 만들지 못한다.
- staff console이 필요한 bounded 운영 read는 유지하되 repairer·auditor·guardian의 복잡한 관계를 role 하나로 축약하지 않는다.
- 일부 beneficiary 화면은 Firestore direct read 대신 backend DTO가 필요해 API 호출·비용·latency가 늘 수 있다.
- Rules는 보안의 한 층일 뿐이며 backend command, App Check, data access grant, 목적 기반 audit를 대체하지 않는다.

## 필수 검증

- active·suspended tenant와 active·future·expired·revoked membership
- beneficiary의 own/other person get과 person filter 없는 list
- beneficiary의 device·repair·prediction direct read 거절
- case worker·tenant admin의 운영 read 허용
- guardian·repairer·auditor의 타인·운영 민감 direct read 거절
- 모든 role의 client mutation과 server-only collection read/write 거절
- cross-tenant read와 unknown role 거절
