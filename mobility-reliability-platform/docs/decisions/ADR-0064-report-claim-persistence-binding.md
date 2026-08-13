# ADR-0064 — report claim persistence binding

- 상태: accepted-local
- 결정일: 2026-08-13
- 범위: Firestore client read defense-in-depth

## 결정

`reportRuns/{reportRunId}` client read는 저장된 `tenant_id`뿐 아니라 `report_run_id == reportRunId`를 요구한다. 하위 `claims/{claimId}`는 다음 조건을 모두 만족할 때만 동일 tenant의 운영 직원에게 읽기를 허용한다.

- claim `tenant_id == tenantId`
- claim `report_run_id == reportRunId`
- claim `claim_id == claimId`
- 부모 report가 존재하고 같은 tenant·report ID를 가짐
- claim과 부모의 `fact_bundle_hash`가 같음

Firestore subcollection은 부모 문서 없이 존재할 수 있고 하위 권한이 부모 권한을 상속하지 않으므로 이 결합을 claim rule에서 명시한다. client write는 계속 전부 거부한다.

## 경계

- Rules는 Admin SDK를 제한하지 않는다. production writer는 같은 binding을 transaction/create-only 경계에서 별도로 검증해야 한다.
- hash equality는 Fact content가 올바르거나 서명됐음을 증명하지 않는다.
- 현재 local Emulator 검증이며 production Rules 배포·index readiness는 별도다.

근거: [EVD-20260813-030](../evidence/2026-08-product.md#evd-20260813-030--report-claim-tenantparent-binding)
