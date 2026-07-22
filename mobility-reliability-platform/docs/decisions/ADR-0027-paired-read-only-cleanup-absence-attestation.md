---
id: ADR-0027
title: Paired read-only cleanup absence attestation
status: accepted
decided_at: 2026-07-22
owners:
  - project owner
supersedes: null
superseded_by: null
---

# ADR-0027: read-only cleanup 부재 증거를 provider auditor와 Firestore verifier에 결합한다

## 맥락

[ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)은 generic progress API가 `raw_absence_confirmed`와 `manifest_absence_confirmed`를 저장하지 못하게 하고, fresh read-only absence audit만 이 두 단계의 근거가 되도록 결정했다. 그러나 read grant와 결과 shape만 caller에게 주면 provider를 읽지 않은 caller가 `confirmed_absent` observation을 직접 구성하거나, 이전 grant의 결과를 다른 phase에 재사용할 수 있다.

이번 결정이 닫는 범위는 **현재 cleanup attempt의 한 artifact path에 대한 read-only 부재 관측을 서명하고 Firestore ledger에 저장하는 경계**다. 실제 object 삭제, raw-to-manifest phase orchestration, terminal attempt completion, receipt `expired`, retry·hold, purge 또는 runtime 활성화는 포함하지 않는다.

Cloud Storage의 exact-path inventory 구현은 regular generation 조회와 soft-deleted generation 조회를 순차 수행한다. 따라서 결과는 provider의 원자적 snapshot 또는 point-in-time proof가 아니다. 이 한계를 숨기지 않고, post-quiescence application writer fencing과 staging에서 검증된 IAM write exclusion을 운영 전제조건으로 둬야 한다.

## 결정 기준

- 최소권한: absence capability는 exact-path inventory read만 허용하고 delete backend에 전달할 수 없어야 한다.
- 출처 무결성: caller가 read grant나 public verifier만으로 승인 가능한 absence evidence를 만들 수 없어야 한다.
- Fresh fencing: request·grant·evidence·persistence가 같은 receipt revision, attempt fence, target/plan hash와 ledger revision에 결합돼야 한다.
- Replay 안전성: 동일 관측의 응답 유실 재시도는 write 0으로 수렴하고, 다른 시각·key·binding은 conflict 또는 unauthorized로 닫혀야 한다.
- 시간 경계: provider read와 Firestore transaction 모두 grant와 lease의 더 이른 deadline 안에서 끝나야 한다.
- 관측 정직성: 두 inventory read를 atomic snapshot이나 전역 부재 증명으로 표현하지 않는다.
- 운영 격리: local component 구현만으로 scheduler, startup, delete, terminal finalizer 또는 staging/production 동작을 활성화하지 않는다.

## 검토한 선택지

### 선택지 A: Caller가 unsigned observation을 만들어 generic progress API에 전달

- 장점: 구현과 테스트가 단순하다.
- 단점: provider read가 실제로 수행됐는지 persistence layer가 구분할 수 없고, command shape만으로 absence phase를 위조할 수 있다.
- 판단: 제외한다.

### 선택지 B: Firestore store가 GCS client와 private signing key를 모두 소유

- 장점: 한 process 안에서 authorization, inventory와 persistence를 이어 호출할 수 있다.
- 단점: Firestore control-plane adapter가 provider I/O까지 소유해 책임과 권한이 커지고, read-only auditor와 persistence 검증자의 분리가 사라진다.
- 판단: 제외한다.

### 선택지 C: Provider auditor가 private key를 소유하고 Firestore store는 paired verifier만 소유

- 장점: GCS inventory를 실제로 읽는 adapter만 증거를 만들 수 있고, caller·grant·verifier만으로는 서명할 수 없다. Firestore는 provider credential이나 private key 없이 서명과 current state를 독립 검증한다.
- 단점: auditor와 store를 같은 pairing에서 생성·주입해야 하며 process 재시작·key rotation을 포함한 runtime composition 정책이 추가로 필요하다.
- 판단: local component 경계로 채택한다. Persistent cross-process attestation이나 운영 key lifecycle을 의미하지 않는다.

## 결정

### 1. Firestore가 current-state read grant를 발급한다

`AuthorizeCleanupAbsenceAudit`는 Firestore transaction에서 receipt, 두 uniqueness index, exact started cleanup attempt와 immutable target을 fresh read한다. 허용된 ledger phase에서만 다음 binding을 가진 30초 이하의 concrete grant와 request를 만든다.

```text
request hash
target hash + plan hash
receipt revision
cleanup owner + fencing token + lease expiry
ledger revision + next absence-confirmed phase
artifact(raw|manifest) + exact expected path
grant checked-at + expiry + capability seal
```

Grant는 destructive cleanup grant와 다른 concrete type이다. GCS auditor의 inventory-only surface에만 전달할 수 있으며 delete executor는 이 타입을 받지 않는다.

### 2. Paired GCS auditor만 opaque evidence를 서명한다

`NewCleanupAbsenceAuditor`는 Ed25519 key pair를 process 안에서 생성한다. Private key는 auditor 내부에 남기고 paired public verifier만 반환한다. `cleanupattest.Evidence`의 필드는 package 외부에서 설정할 수 없고, 서명 payload는 다음 값에 결합한다.

```text
request hash
concrete grant capability seal hash
artifact(raw|manifest)
outcome=confirmed_absent
observed_at
```

Auditor는 grant/lease deadline 안에서 exact expected path의 bounded inventory를 읽는다. Regular와 soft-deleted inventory가 모두 complete, non-truncated, error-free이고 candidate가 0개일 때만 evidence를 만든다. Live generation, soft-deleted generation, incomplete inventory, permission, timeout, cancellation 또는 provider unavailable은 evidence를 만들지 않는다.

Verifier나 grant만 가진 caller는 valid evidence를 제조할 수 없다. Zero verifier, wrong key, 다른 request·grant binding, artifact 또는 observation time에 대한 evidence는 persistence 전에 거부한다.

### 3. Firestore persistence가 서명과 current state를 다시 검증한다

`RecordCleanupAbsenceAudit`만 absence-confirmed phase를 저장할 수 있다. Generic `RecordCleanupExecutionProgress`는 계속 두 phase를 거부한다.

Persistence는 먼저 paired verifier로 evidence를 확인하고 grant가 evidence observation time과 현재 application time 모두에서 유효한지 검사한다. 그 후 grant/lease deadline context를 transaction 전체에 적용하고 receipt·index·attempt·target을 다시 읽어 다음을 확인한다.

- target/plan hash, receipt revision과 current fence가 request와 일치한다.
- current ledger revision과 phase가 request의 expected state다.
- evidence observation time과 transaction effective time이 허용 clock skew 안에 있다.
- receipt, 두 index와 immutable target linkage가 변하지 않았다.

성공 시 exact attempt의 audit outcome과 `AuditedAt`만 다음 단조 phase로 기록한다. Receipt, 두 index와 immutable target은 write하지 않는다.

### 4. Replay와 drift를 분리한다

- Exact request, grant binding, evidence와 같은 persisted `AuditedAt` 재전송은 `replayed`와 write 0이다.
- 같은 phase라도 다른 observation time은 exact replay가 아니므로 conflict와 write 0이다.
- Stale receipt revision, fence, ledger revision, target/plan binding 또는 다른 key evidence는 mutation 전에 거부한다.
- Firestore transaction deadline 또는 parent cancellation 이후 결과를 성공으로 승격하지 않는다.

### 5. GCS inventory 결과를 bounded observation으로만 해석한다

현재 GCS reader는 regular generation listing과 soft-deleted generation listing을 하나의 provider 원자 snapshot으로 묶을 수 없다. 따라서 `confirmed_absent`는 다음 조건 아래의 **bounded, sequential observation**이다.

- cleanup quiescence와 application writer fencing이 이미 성립한다.
- 해당 prefix에 대한 out-of-band write를 IAM과 운영 절차로 배제한다.
- 두 목록이 모두 complete/non-truncated이고 같은 짧은 authorization window 안에서 읽힌다.

이 조건이 staging에서 검증되기 전에는 production readiness를 승인하지 않는다. 문서와 UI에서 이 결과를 atomic snapshot, point-in-time proof 또는 모든 외부 writer에 대한 절대적 부재 증명으로 표현하지 않는다.

### 6. 실행·운영 활성화는 계속 닫아 둔다

이 R8e 증분은 raw 또는 manifest 한 단계의 read-only audit와 persistence component만 제공한다. 당시 후속 범위였던 progress-bearing expired cleanup takeover는 [ADR-0028](./ADR-0028-progress-aware-expired-cleanup-takeover.md)에서 local transaction component로 구현했지만 runtime에는 연결하지 않았다. 다음은 계속 미연결·미구현이다.

- Delete dispatch/outcome과 absence audit을 순서대로 호출하는 phase executor
- Retry·hold disposition persistence
- Attempt `completed`, receipt `expired`와 세 control document의 `purge_eligible_at`을 묶는 terminal finalizer
- Commit response-loss `committed|not_committed|unverifiable` correlation
- Scheduler, startup, readiness, runtime route와 실제 staging/production delete
- Auditor key lifecycle, rotation과 cross-process 배포 정책

## 결과와 위험

- Caller가 임의 observation을 구성해 absence-confirmed phase를 쓰는 경로가 닫힌다.
- Read-only grant와 destructive grant가 concrete type과 provider surface에서 분리된다.
- Evidence는 한 request와 한 grant에 묶여 다른 fence·revision·artifact에 재사용할 수 없다.
- In-memory private key는 local composition의 최소 경계일 뿐 production key management 설계가 아니다.
- Sequential GCS listing 사이의 out-of-band write race는 provider 수준에서 제거되지 않았다. Application fencing만으로 외부 writer를 막을 수 없으므로 least-privilege IAM과 staging write-exclusion 검증이 필수다.
- Terminal finalizer가 없으므로 이 구현만으로 receipt가 `expired`가 되거나 object가 purge되는 일은 없다.

## 연결 문서

- 선행 결정: [ADR-0025](./ADR-0025-generation-pinned-cleanup-delete-and-audit.md), [ADR-0026](./ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)
- 실행계획: [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- 운영 절차: [Telemetry Reconciliation Runbook](../development/TELEMETRY_RECONCILIATION_RUNBOOK.md)
- 증거: [EVD-20260722-035](../evidence/2026-07.md#evd-20260722-035--서명된-read-only-cleanup-absence-audit와-firestore-persistence)
- 사람 대상 리포트: [HR-20260722-26](../reports/human/HR-20260722-26-signed-cleanup-absence-audit.md)
- 제품 업데이트: 해당 없음 — executable·scheduler·사용자·staging·production 경로 미연결
- 인시던트: 해당 없음 — production·staging·field 영향 없음
