# Telemetry Reconciliation Runbook

## 1. 현재 사용 상태

이 문서는 [ADR-0017](../decisions/ADR-0017-fenced-ingest-recovery.md)의 운영 절차를 구현·검증하기 위한 사전 runbook이다. 2026-07-22 현재 single-receipt reconciler component는 구현됐지만 candidate sweeper, cleanup command, startup wiring과 staging IAM은 구현되지 않았으므로 production artifact를 조회·수정·삭제하는 실행 지침이 아니다.

- 허용: synthetic fixture, Firestore Emulator, pinned official Storage testbench의 read-only/classifier와 bounded single-receipt protocol 검증
- 금지: 실제 bucket에서 path/latest/prefix 기준 삭제, receipt 수동 수정, 실제 사용자 좌표를 로그·문서에 복사
- runtime 상태: adapter wiring 전 `/readyz`와 ingest는 계속 `503`

## 2. 용어

- forward reconciliation: `reservation_deadline` 전 valid partial artifact를 `stored`까지 전진시키는 흐름
- expiry cleanup: deadline 뒤 새 artifact를 만들지 않고 사전 기록한 exact generation만 제거하는 별도 흐름
- fence: lease takeover마다 증가하는 receipt의 단조 증가 token
- recovery hold: 자동 완료·삭제가 안전하지 않아 사람 검토가 필요한 상태
- stored-missing: stored receipt가 기록한 exact generation을 읽을 수 없는 무결성 상태

## 3. 최초 대응 순서

1. 작업을 시작하기 전에 environment, project ID, bucket, commit, worker version이 staging/test인지 확인한다.
2. receipt ID와 tenant ID만으로 control record를 조회하고 body·좌표·token·UID/App ID를 출력하지 않는다.
3. 두 uniqueness index와 receipt의 3-way linkage·revision·state·deadline을 확인한다.
4. active lease가 있으면 owner를 추정해 빼앗지 않고 expiry와 heartbeat를 확인한다.
5. recovery claim transaction이 반환한 owner/fence가 없으면 Storage 작업을 하지 않는다.
6. claim을 artifact permission으로 사용하지 않는다. pending forward recovery는 current tenant·installation·trip·assignment·precise-location consent의 system authorization과 grant/fence binding이 성공해야 하며, 그 전에는 Storage call을 만들지 않는다.
7. manifest exact path의 version-aware inventory를 먼저 읽는다. candidate가 복수면 bytes를 비교해 권위 generation을 고르지 않는다.
8. manifest candidate가 유일하면 exact generation을 pin하고 내부 raw exact generation만 따른다.
9. manifest candidate가 없을 때만 raw path의 version-aware inventory를 읽고 유일 candidate를 고정한다.
10. permission, quota, timeout, malformed provider response를 NotFound로 기록하지 않는다. inventory 뒤 exact generation이 사라진 경우도 no-artifact가 아니라 drift다.
11. read 전후 exact attrs·metageneration을 비교하고 strict manifest/raw validation을 수행한다.
12. R5 classifier는 분류 결과를 반환하기만 한다. R6 reconciler가 current authorization, two-pass confirmation, optional manifest-only write, action/disposition과 attempt completion을 순서대로 호출하며 classifier 자체는 recovery attempt를 수정하지 않는다.
13. Lease remaining이 45초 이하이면 stage 경계에서만 renew한다. 성공하면 old evidence를 모두 폐기하고 initial authorization부터 다시 시작하며, 응답이 불명확하면 old fence로 진행하지 않는다.
14. Action/disposition commit 오류는 old mutation을 재호출하지 않고 exact outcome을 읽는다. 첫 결과가 not-committed면 attempt-failure transaction barrier를 먼저 시도하고, 실패 시 old outcome을 다시 확인한 뒤 current disposition만 허용한다.
15. Caller cancellation 뒤 detached tail에서는 outcome read, attempt failure와 current disposition만 허용한다. Artifact I/O, renewal, normal action은 계속하지 않는다.
16. hold·data-loss 후보는 자동 retry/delete를 중지하고 사람 검토로 escalation한다.

## 4. 분류별 운영 판단

| 분류 | forward action | cleanup action | escalation |
| --- | --- | --- | --- |
| no artifact | client replay 대기, bounded backoff | version-aware empty 확인 뒤 expired 후보 | 반복 age 증가 |
| valid raw-only | payload/receipt 재검증, manifest 생성, fenced finalizer | 새 manifest 금지, raw exact target만 계획 | deadline 근접 |
| valid complete | cross-lineage 후 fenced finalizer | raw→manifest exact target 계획 | finalizer 반복 실패 |
| manifest-only | write 0, hold | verified manifest exact target만 별도 승인 | 즉시 integrity review |
| raw content conflict | exact bytes 자체 digest·decompressed body hash·strict lineage 위반 증거 후 fenced reject; recompression mismatch 단독 reject 금지 | 소유 불명 object 자동삭제 금지 | security/integrity review |
| metadata conflict | reject 금지, hold | delete 금지 | provider/IAM review |
| generation drift | 재조회로 latest 선택 금지, hold | delete 금지 | versioning/lifecycle review |
| stored-missing, artifact expiry 전 | downstream 차단, finding | 자동삭제 금지 | high severity integrity alert |
| stored-missing, artifact expiry 후 | 승인된 lifecycle/deletion evidence 대조 | 정상 deletion workflow 또는 exact integrity cleanup | 증거 불일치 시 escalation |
| consent invalid pending | 새 artifact·finalizer 0, consent-invalid hold | 철회만으로 자동삭제 금지; 명시적 요청/retention expiry만 cleanup | privacy owner 확인 |

## 5. Hold에서 확인할 근거

- receipt: state, revision, immutable input, reservation/artifact/purge timestamps, last fence
- index: reservation/client-batch 두 문서가 같은 receipt·batch·body hash를 가리키는지
- manifest: exact bytes/hash/CRC/size/generation과 referenced raw generation
- raw: exact compressed bytes/hash/CRC/size/generation, strict decoded identity와 capture bounds
- provider: bucket versioning, lifecycle, retention, IAM 변경 이력
- deletion: 기존 cleanup target, 승인자, step별 outcome
- worker: attempt ID, worker version, owner kind, fence, error class, timing

문서·티켓·스크린샷에는 좌표, raw payload, ID token, App Check token, 사람 이름·전화번호·주소를 넣지 않는다.

## 6. 절대 실행하지 않는 조치

- manifest 없이 raw 내용을 추정하거나 raw 없이 manifest를 역생성
- stored receipt를 발견한 최신 generation으로 다시 연결
- metadata를 in-place 수정해 검사를 통과시키기
- prefix 전체 또는 unresolved environment variable을 대상으로 삭제
- `generation=latest` 의미의 삭제
- cleanup target 생성 전에 object 삭제
- raw 삭제 실패 뒤 manifest부터 삭제
- transient provider error를 이미 삭제됨으로 기록
- hold receipt의 index를 먼저 지워 client batch를 재사용 가능하게 만들기
- current consent invalid pending receipt를 forward 완료
- recovery hold를 이유로 raw artifact lifecycle을 자동 연장

## 7. Expiry cleanup 재개 규칙

expiry cleanup은 일반 finalizer가 시작하지 않는다. `BeginCleanupTransition`이 `reserved + deadline 경과 + active lease 없음 + 3-way linkage 정상`을 transaction에서 확인하고 token을 증가시켜 `cleanup_pending`으로 전환한다. reserved-origin hold는 `BeginHeldCleanup`만 같은 상태로 보낼 수 있다. accepted receipt는 `BeginAcceptedDeletion`으로 `deleting -> deleted`를 사용해 replay-complete를 유지한다. rejected artifact는 receipt를 `rejected`로 유지하며 exact ownership과 별도 보안 승인 없이는 cleanup target을 만들지 않는다. 모든 cleanup entry는 origin status를 고정하고 최대 lease+Storage operation timeout보다 긴 quiet period 뒤 시작한다. cleanup target은 처음 발견한 exact lineage를 immutable하게 유지한다.

```text
planned
  -> raw_deleted
  -> manifest_deleted
  -> completed
```

- raw delete 뒤 crash: 같은 raw delete outcome을 idempotently 확인하고 같은 manifest generation으로 재개한다.
- manifest delete 뒤 crash: 같은 target의 두 outcome을 확인하고 receipt 상태만 finalization한다.
- target 생성 뒤 새 generation 발견: 기존 target을 교체하지 않고 hold한다.
- 삭제 뒤 version-aware live generation을 다시 검사하고, late generation이 있으면 `expired` 완료를 금지한다.
- exact generation NotFound: provider error가 아닌지, 승인된 lifecycle/deletion evidence가 있는지 확인한 뒤 outcome을 확정한다.
- artifact expiry 전 stored-missing만 즉시 data-loss finding으로 분류한다. expiry 이후에는 승인된 lifecycle/deletion evidence와 `deleting/deleted` workflow를 먼저 대조한다.
- 만료 전 hold는 review due를 artifact expiry보다 앞에 둔다. 이미 만료된 finding은 review due를 현재시각으로 두고 즉시 integrity cleanup 대상으로 보내며, cleanup이 막히면 privacy/operations incident로 escalate한다.
- artifact cleanup·감사가 끝날 때까지 `purge_eligible_at`은 null이다. 완료 transaction이 두 index와 receipt에 같은 eligibility를 설정하되 독립 TTL 삭제는 사용하지 않는다.
- eligibility 뒤 purge job은 receipt 하위 attempt와 linked cleanup target·integrity finding을 bounded cursor로 먼저 삭제한다. 세 집합이 empty임을 증명한 뒤에만 마지막 transaction으로 두 index와 receipt를 함께 삭제한다. Firestore parent delete가 subcollection을 지운다고 가정하지 않는다.

## 8. Local/CI 검증 체크리스트

R5 read-only classifier의 독립 완료 기준은 [ADR-0018](../decisions/ADR-0018-generation-pinned-read-only-classifier.md)을 따른다. 아래 목록은 이후 reconciler·cleanup까지 포함한 전체 recovery checklist이며 R5 하나의 완료 조건으로 해석하지 않는다.

2026-07-21 local R5 classifier gate는 [EVD-20260721-023](../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier), current forward authorization gate는 [EVD-20260721-024](../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization)에서 통과했다. 전자는 synthetic classifier matrix와 pinned official Storage testbench reader, 후자는 provider-neutral policy와 Firestore Emulator의 claim→authorization→consent withdrawal을 검증한다. 둘을 하나의 startup worker로 연결하거나 staging authorization·lifecycle과 아래 전체 recovery checklist를 완료한 증거는 아니다.

- [ ] fake clock으로 lease exact-expiry boundary 재현
- [ ] 두 request/sweeper의 concurrent claim winner 1명
- [ ] recovery claim 대 `BeginCleanupTransition` 경계 경쟁에서 cleanup/recovery 중 허용된 winner만 1명
- [ ] takeover 뒤 stale renew/release/stored/rejected update 0
- [ ] raw-only → same raw generation manifest → stored
- [ ] no-artifact → Storage write 0
- [ ] forward/pre-expiry manifest-only·stored-missing → create/delete/finalize 0
- [ ] consent invalid pending → forward write 0
- [ ] timeout/403/429 → missing 분류 0
- [ ] cleanup dry-run target의 path·generation·hash 고정
- [ ] 복수 manifest generation은 bytes 동일 여부와 관계없이 자동 선택 0
- [ ] post-expiry hold가 즉시 integrity cleanup 또는 incident escalation으로 연결
- [ ] accepted deletion 중 replay-complete, rejected side cleanup 중 replay-rejected 유지
- [ ] rejected artifact는 ownership+보안 승인 없으면 target/delete 0
- [ ] attempt가 transaction limit보다 많아도 resumable purge 뒤 orphan 0
- [ ] actual delete test는 official testbench synthetic generation만 사용
- [ ] recovery ledger와 log privacy scan 통과
- [ ] Firestore Emulator와 GitHub clean runner 통과

## 9. Escalation과 문서 스트림

- local synthetic 실패: EVD에 실패→수정→재검증을 기록하고 Incident는 열지 않는다.
- staging integrity mismatch: 배포·cleanup을 중지하고 risk/evidence를 갱신한다.
- production/field에서 위치 data loss, cross-tenant access, 잘못된 삭제가 발생: 관련 write와 cleanup을 중지하고 Incident policy에 따라 즉시 기록한다.
- runtime 연결 전에는 Product Update를 만들지 않는다.
- 사람용 리포트는 실제 구현·검증 결과만 쓰며 이 runbook의 계획을 성과로 표현하지 않는다.

## 10. 연결 문서

- [ADR-0017](../decisions/ADR-0017-fenced-ingest-recovery.md)
- [Telemetry Recovery Plan](../plans/TELEMETRY_RECOVERY_PLAN.md)
- [Target Domain Model](../data/TARGET_DOMAIN_MODEL.md)
- [Risk Register](../plans/RISK_REGISTER.md)
- [Incident policy](../incidents/README.md)
