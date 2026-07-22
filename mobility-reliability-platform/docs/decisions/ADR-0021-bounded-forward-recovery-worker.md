---
id: ADR-0021
title: Tenant-scoped bounded forward recovery worker and fixed-cutoff checkpoint
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0021: Tenant-scoped bounded forward recovery worker와 fixed-cutoff checkpoint

## 맥락

[ADR-0020](./ADR-0020-two-pass-forward-reconciliation.md)은 이미 claim된 receipt 하나를 current authorization, generation-pinned classification, optional manifest-only repair와 atomic action으로 처리하는 bounded protocol을 정했다. R7에서는 `reserved` receipt를 발견하고, recovery lease를 claim한 뒤 이 single-receipt protocol에 넘기는 outer worker가 필요하다.

Candidate discovery를 단순 반복 query로 구현하면 다음 위험이 생긴다.

- 매 실행마다 정렬 선두부터 읽으면 지속적으로 실패하는 문서가 뒤 후보를 굶길 수 있다.
- cursor만 저장하고 실행마다 cutoff를 바꾸면 유입량이 처리량 이상일 때 scan이 끝나지 않아 cursor 이전 후보를 영구 재방문하지 못할 수 있다.
- Firestore 문서가 형식상 손상됐다는 이유로 page 전체를 실패시키면 같은 poison document가 tenant의 모든 후속 복구를 막는다.
- Query 결과를 처리 권한으로 오해하면 조회와 claim 사이의 상태·deadline·lease 변경을 놓친다.
- Cursor나 checkpoint에 사용자·기기·artifact 정보를 넣으면 운영 control state가 새로운 개인정보 표면이 된다.
- Worker를 local component 검증만으로 startup·scheduler·readiness에 연결하면 production index, IAM과 실제 Firebase/GCP 실패 의미를 검증하지 않은 채 mutation 경로가 열린다.

## 결정 기준

- tenant 격리: candidate query와 checkpoint가 한 tenant 경계를 벗어나지 않을 것
- 결정성: 동일 due time에도 document ID로 안정적인 순서와 cursor를 만들 것
- 진행 보장: malformed candidate 하나가 다음 candidate 처리를 영구 차단하지 않을 것
- 소유권: query와 checkpoint는 advisory이며 Firestore claim transaction만 작업 소유권을 결정할 것
- 유한성: page, item, retry, claim, receipt와 전체 run에 상한을 둘 것
- 개인정보: observer, result, checkpoint에 좌표·body·UID·App ID·artifact path를 넣지 않을 것
- 운영 정직성: local·Emulator component와 배포된 scheduled worker를 구분할 것

## 검토한 선택지

### 선택지 A: 매 실행마다 첫 page부터 stateless scan

- 장점: checkpoint 저장소와 CAS가 필요 없다.
- 단점: 큰 backlog에서 뒤 후보가 반복적으로 밀리고, 선두 poison 또는 transient failure가 처리 비용을 계속 독점한다.
- 판단: bounded run 사이의 진행을 보장하지 못하므로 적용하지 않는다.

### 선택지 B: Cursor만 저장하고 실행마다 현재시각까지 scan

- 장점: 새 due candidate를 매 실행 즉시 같은 scan에 포함할 수 있다.
- 단점: 유입이 처리량 이상이면 tail이 계속 늘어 exhaustion과 cursor reset이 발생하지 않는다. Cursor 이전에서 claim response가 불명확했거나 malformed 상태가 정정된 receipt가 영구 누락될 수 있다.
- 판단: 정상적인 지속 유입에서도 starvation이 가능하므로 적용하지 않는다.

### 선택지 C: Tenant별 fixed-cutoff scan epoch와 CAS checkpoint

- 장점: 고정 cutoff 뒤 새로 due가 되는 정상 유입을 현재 epoch의 tail에 계속 붙이지 않고, exhaustion/reset 뒤 다음 epoch에서 다시 head를 방문한다. Malformed document도 cursor material이 유효하면 개별 격리하고 다음 문서로 진행할 수 있다.
- 단점: checkpoint schema, CAS conflict 처리, reset과 corrupt-state fallback 테스트가 추가된다. Cutoff 이후 due candidate는 다음 epoch까지 기다린다.
- 판단: bounded fairness와 claim 안전성을 함께 유지하므로 채택한다.

## 결정

### 1. Candidate query는 tenant-scoped direct collection을 사용한다

각 실행은 명시적으로 검증된 tenant ID 하나를 받고 다음 direct collection만 조회한다.

```text
tenants/{tenantId}/ingestReceipts
  where status == reserved
  where next_recovery_at <= scan_cutoff
  order by next_recovery_at ASC
  order by __name__ ASC
```

- Pagination cursor는 `(next_recovery_at, document ID)`다.
- Query는 `limit + 1`개만 읽어 다음 page 존재 여부를 판단하고 반환 page는 설정된 상한을 넘지 않는다.
- Firestore composite index는 `status ASC, next_recovery_at ASC, __name__ ASC`의 collection scope로 선언한다.
- Collection-group 전체 tenant query는 현재 선택하지 않는다.
- Candidate에는 tenant ID, reservation key, document ID, stored receipt ID, state와 due time만 포함한다.
- 저장된 `receipt_id`는 document ID와 같아야 한다. 불일치하거나 reservation key·tenant·state가 잘못된 candidate는 claim하지 않는다.
- Query 결과는 작업 권한이 아니다. `ClaimRecoveryLease` transaction이 authoritative receipt, linkage, state, due time, deadline과 current lease를 다시 읽고 winner를 결정한다.

### 2. Cursor material과 candidate validity를 분리한다

Firestore가 반환한 문서의 `next_recovery_at`과 document ID가 유효하면 cursor를 보존한다. 그 외 candidate field가 malformed여도 page 전체를 provider missing으로 축소하지 않는다.

- Worker는 candidate를 다시 검증하고 malformed item을 aggregate `invalid`로 관측한 뒤 그 document cursor까지 진행한다.
- Malformed candidate에는 lease claim, artifact read/write와 receipt mutation이 0이어야 한다.
- Provider query failure, invalid cursor material 또는 incoherent ordering은 page failure로 닫는다.
- Cursor와 공개 observer에는 reservation key, receipt ID, provider 원문 오류를 전달하지 않는다.

이 결정은 poison isolation을 위한 것이며 손상 문서를 정상 receipt로 간주하거나 삭제한다는 의미가 아니다.

### 3. Checkpoint는 fixed scan cutoff를 가진 advisory CAS state다

Tenant별 checkpoint는 다음 private server-only document에 저장한다.

```text
tenants/{tenantId}/recoveryWorkerState/forward
```

Checkpoint는 `revision`, `scan_cutoff`, cursor의 `next_recovery_at`·`document_id`, `updated_at`만 가진다.

- Cursor가 존재할 때 `scan_cutoff`도 반드시 존재하며 cursor due time보다 빠를 수 없다.
- 재개 실행은 새 현재시각이 아니라 저장된 `scan_cutoff`를 그대로 사용한다.
- Page 또는 item budget 뒤에는 마지막으로 scan한 cursor와 같은 cutoff를 revision CAS로 저장한다.
- 현재 pagination에서 더 반환할 candidate가 없으면 cursor와 cutoff를 함께 비우고 revision을 증가시켜 reset한다. Document를 삭제해 revision ABA를 만들지 않는다.
- Fixed cutoff는 Firestore read snapshot을 고정하지 않는다. Page 사이에 `status`·`next_recovery_at`이 바뀌거나 cutoff 이하 문서가 backfill/create되면 중복 scan 또는 다음 epoch까지의 지연이 생길 수 있으며 fresh claim과 eventual wrap으로만 안전하게 수렴한다.
- Concurrent worker 중 CAS winner만 다음 checkpoint를 쓴다. Loser와 provider-unavailable 경로는 checkpoint write를 중단하되 이미 처리 중인 scan은 bounded 범위에서 계속할 수 있다.
- Checkpoint loss·conflict는 중복 scan을 만들 수 있지만 claim 권한이나 receipt mutation 권한을 만들지 않는다.
- Client Firestore Rules는 `recoveryWorkerState` 전체 read/write를 거부한다.

### 4. Outer worker는 모든 실행 단위를 제한한다

Worker config는 page size, max pages, max items, page attempt, page/checkpoint/claim/per-item timeout, total run timeout, lease duration과 panic breaker를 상한 안에서만 허용한다.

- Page read의 transient failure는 상한이 있는 exponential full-jitter retry를 사용한다. Random source와 sleep은 deterministic test seam으로만 대체할 수 있다.
- Candidate마다 server UUID attempt ID를 새로 만들고 sweeper owner로 `ClaimRecoveryLease`를 한 번 호출한다.
- `lease_acquired`와 exact sweeper grant가 함께 검증된 경우에만 [ADR-0020](./ADR-0020-two-pass-forward-reconciliation.md)의 reconciler를 실행한다.
- Claim response가 불명확하거나 candidate·claim·execution 하나가 실패해도 다음 item을 처리하되, claim transaction이 실제 commit됐을 가능성은 receipt의 갱신된 due time과 후속 fenced recovery에 맡긴다.
- Claim 성공 직후 caller가 취소돼도 이미 생성된 `started` attempt를 방치하지 않도록 per-item timeout 안에서 single-receipt reconciler handoff를 마친다. 이후 새 candidate는 claim하지 않는다.
- Observer와 public result는 bounded enum, count와 duration만 받는다. Tenant, receipt, attempt, cursor, path, 좌표와 provider 오류 문자열은 노출하지 않는다.

### 5. 누락 field 탐지는 별도 integrity audit로 남긴다

Firestore query 특성상 `status` 또는 `next_recovery_at`이 없거나 query와 호환되지 않는 타입인 문서는 이 query 결과에 나타나지 않는다. Per-document poison cursor는 **query가 반환한 문서**만 격리할 수 있다.

- 모든 server writer는 receipt state와 `next_recovery_at` 불변조건을 계속 검증한다.
- Missing status/due document를 탐지하는 별도 bounded integrity audit 또는 server-maintained index를 operational readiness 전에 설계·검증한다.
- 현재 candidate scan 완료를 전체 receipt 무결성 감사 완료로 표현하지 않는다.

### 6. Runtime 연결은 이 결정의 범위가 아니다

Commit `9bd7787`은 provider-neutral outer worker와 Firestore candidate/checkpoint adapter, index·Rules와 local test seam을 구현한다. 그러나 다음은 연결하지 않는다.

- `cmd/server` startup composition
- Cloud Scheduler 또는 authenticated worker endpoint
- Runtime metrics exporter와 alert sink
- `/readyz` 변경
- Staging·production Firestore index, ADC/IAM과 Cloud Run invocation

따라서 executable은 기존 fail-closed 상태를 유지하며 이 ADR은 scheduled recovery가 운영 중이라는 증거가 아니다.

## 결과와 위험

- 기대 효과: tenant별 deterministic scan, fixed-cutoff epoch의 bounded fairness, malformed candidate 격리, concurrent checkpoint CAS와 fenced claim을 결합한다.
- 새로 생기는 위험: checkpoint 장애 또는 page 사이 mutable query 변화로 중복 read·claim 비용과 다음 epoch까지의 지연이 생길 수 있고, missing status/due receipt는 별도 audit 전까지 query에서 보이지 않는다.
- 되돌리는 조건: fixed cutoff가 실제 backlog SLO를 충족하지 못하거나 CAS contention 비용이 유의미하면 cursor schema version과 공정성 기준을 새 ADR로 재검토한다.
- 후속 검증: clean runner, staging composite index READY, actual ADC/IAM, concurrent scheduled invocation, metrics·alert, missing-field audit와 비용 측정을 별도 gate로 수행한다.

## 연결 문서

- 선행 결정: [ADR-0017](./ADR-0017-fenced-ingest-recovery.md), [ADR-0020](./ADR-0020-two-pass-forward-reconciliation.md)
- 증거: [EVD-20260722-029](../evidence/2026-07.md#evd-20260722-029--bounded-forward-recovery-worker와-cross-run-checkpoint)
- 사람 대상 리포트: [HR-20260722-20](../reports/human/HR-20260722-20-bounded-forward-recovery-worker.md)
- 제품 업데이트: 해당 없음 — startup·scheduler·readiness와 사용자 경로에 미연결
- 인시던트: 해당 없음 — local synthetic·Emulator 검토와 정정에 한정되고 production·staging·field 영향 없음
