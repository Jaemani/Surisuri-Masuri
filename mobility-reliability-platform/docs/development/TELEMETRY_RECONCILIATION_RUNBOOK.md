# Telemetry Reconciliation Runbook

## 1. 현재 사용 상태

이 문서는 [ADR-0017](../decisions/ADR-0017-fenced-ingest-recovery.md)의 운영 절차를 구현·검증하기 위한 사전 runbook이다. 2026-07-22 현재 single-receipt reconciler, bounded candidate worker, cleanup dry-run target, local generation-pinned delete/audit와 partial progress persistence component는 구현됐다. Absence audit·completion, scheduler/startup wiring, metrics exporter와 staging IAM은 구현되지 않았으므로 production artifact를 조회·수정·삭제하는 실행 지침이 아니다.

- 허용: synthetic fixture, Firestore Emulator, pinned official Storage testbench의 read-only/classifier와 bounded single-receipt protocol 검증
- 금지: 실제 bucket에서 path/latest/prefix 기준 삭제, receipt 수동 수정, 실제 사용자 좌표를 로그·문서에 복사
- runtime 상태: adapter wiring 전 `/readyz`와 ingest는 계속 `503`

## 2. 용어

- forward reconciliation: `reservation_deadline` 전 valid partial artifact를 `stored`까지 전진시키는 흐름
- expiry cleanup: deadline 뒤 새 artifact를 만들지 않고 사전 기록한 exact generation만 제거하는 별도 흐름
- fence: lease takeover마다 증가하는 receipt의 단조 증가 token
- recovery hold: 자동 완료·삭제가 안전하지 않아 사람 검토가 필요한 상태
- stored-missing: stored receipt가 기록한 exact generation을 읽을 수 없는 무결성 상태
- scan epoch: 시작 시 고정한 cutoff 이하만 순회하고 tail 소진 뒤 cursor와 cutoff를 함께 초기화하는 한 번의 bounded candidate scan
- advisory checkpoint: 공정한 다음 scan 위치를 위한 cursor이며 receipt ownership이나 artifact 권한이 아닌 control hint

## 3. R7 candidate scan 절차

1. Tenant별 `ingestReceipts`에서 `status=reserved`, `next_recovery_at <= scan_cutoff`만 조회한다.
2. `next_recovery_at ASC, document ID ASC`로 정렬하고 마지막 반환 문서의 두 값을 cursor로 사용한다. 같은 시각의 문서도 document ID로 결정론적으로 전진한다.
3. 기존 checkpoint cursor가 있으면 함께 저장된 `scan_cutoff`를 그대로 재사용한다. 실행 중 현재시각으로 cutoff를 움직이지 않는다.
4. Candidate의 저장 `receipt_id`가 document ID와 다르거나 tenant/reservation key/state가 손상됐으면 그 advisory item만 invalid로 집계하고 document cursor는 전진시킨다.
5. 유효 candidate도 fresh sweeper attempt ID로 `ClaimRecoveryLease`를 transaction 호출한다. `Acquired`와 exact owner/fence가 모두 유효할 때만 R6를 호출한다.
6. Held·NotDue·DeadlineElapsed·NotEligible은 bounded skip한다. Claim error나 응답 불명확은 unknown으로 기록하고 즉시 retry·reclaim·R6 실행을 하지 않는다.
7. Candidate page read만 bounded exponential full-jitter retry할 수 있다. Claim과 mutation은 동일 호출을 추정 replay하지 않는다.
8. Page가 더 있으면 cursor+fixed cutoff를 advisory CAS checkpoint로 저장한다. 저장 오류나 CAS conflict는 이후 checkpoint write만 끄며 현재 receipt 처리와 claim 권한을 바꾸지 않는다.
9. Tail 소진 시 cursor와 cutoff를 함께 reset한다. 다음 run은 새 cutoff로 head부터 새 epoch를 시작한다.
10. Item 오류·panic은 다음 후보와 격리하되 panic breaker와 page/item/run budget이 열리면 마지막으로 완전히 검사한 cursor까지만 checkpoint한다.
11. Parent cancel 뒤 새 claim은 금지한다. 이미 `Acquired`가 반환된 started attempt만 2분 상한의 detached context에서 R6 finalizer까지 drain한다.
12. `status` 또는 `next_recovery_at`이 누락되거나 query 비호환 type인 문서는 이 query에 보이지 않는다. 별도 integrity audit가 없으면 scan complete를 전체 receipt 무결성 complete로 해석하지 않는다.
13. Page query는 하나의 Firestore snapshot이 아니다. Page 사이 state/due 변경 또는 cutoff 이하 backfill/create가 있으면 중복 처리나 다음 epoch까지의 지연이 가능하므로, fresh claim과 reset 뒤 head wrap을 유지하고 scan complete를 snapshot complete로 기록하지 않는다.

## 4. 최초 대응 순서

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

## 5. 분류별 운영 판단

| 분류 | forward action | cleanup action | escalation |
| --- | --- | --- | --- |
| no artifact | client replay 대기, bounded backoff | `verified_empty/planned` dry-run target; `expired` 권한 아님 | 반복 age 증가 |
| valid raw-only | payload/receipt 재검증, manifest 생성, fenced finalizer | 새 manifest 금지, raw exact `delete_candidate/planned`; 실행 승인 아님 | deadline 근접 |
| valid complete | cross-lineage 후 fenced finalizer | raw·manifest exact `delete_candidate/planned`; 실행 승인 아님 | finalizer 반복 실패 |
| manifest-only | write 0, hold | exact manifest `delete_candidate/planned`; 삭제 capability 아님 | 즉시 integrity review |
| raw content conflict | exact bytes 자체 digest·decompressed body hash·strict lineage 위반 증거 후 fenced reject; recompression mismatch 단독 reject 금지 | `hold/hold`, 소유 불명 object 자동삭제 금지 | security/integrity review |
| metadata conflict | reject 금지, hold | `hold/hold`, delete 금지 | provider/IAM review |
| generation drift | 재조회로 latest 선택 금지, hold | `hold/hold`, delete 금지 | versioning/lifecycle review |
| provider unavailable | bounded release/retry | immutable target 미생성, takeover/retry | 반복 시 provider review |
| stored-missing, artifact expiry 전 | downstream 차단, finding | 자동삭제 금지 | high severity integrity alert |
| stored-missing, artifact expiry 후 | 승인된 lifecycle/deletion evidence 대조 | 정상 deletion workflow 또는 exact integrity cleanup | 증거 불일치 시 escalation |
| consent invalid pending | 새 artifact·finalizer 0, consent-invalid hold | 철회만으로 자동삭제 금지; 명시적 요청/retention expiry만 cleanup | privacy owner 확인 |

## 6. Hold에서 확인할 근거

- receipt: state, revision, immutable input, reservation/artifact/purge timestamps, last fence
- index: reservation/client-batch 두 문서가 같은 receipt·batch·body hash를 가리키는지
- manifest: exact bytes/hash/CRC/size/generation과 referenced raw generation
- raw: exact compressed bytes/hash/CRC/size/generation, strict decoded identity와 capture bounds
- provider: bucket versioning, lifecycle, retention, IAM 변경 이력
- deletion: 기존 cleanup target, 승인자, step별 outcome
- worker: attempt ID, worker version, owner kind, fence, error class, timing

문서·티켓·스크린샷에는 좌표, raw payload, ID token, App Check token, 사람 이름·전화번호·주소를 넣지 않는다.

## 7. 절대 실행하지 않는 조치

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

## 8. Expiry cleanup 재개 규칙

expiry cleanup은 일반 finalizer가 시작하지 않는다. `BeginCleanupTransition`이 `reserved + deadline 경과 + active lease 없음 + 3-way linkage 정상`을 transaction에서 확인한다. 만료 lease에 recovery attempt count가 있으면 exact nested attempt의 owner·token·version·started time을 함께 검증하고 `started`를 같은 transaction에서 `failed/lease_expired`로 닫은 뒤 token을 증가시켜 `cleanup_pending`으로 전환한다. Attempt가 누락·변조·completed이면 receipt도 바꾸지 않고 조사 가능한 lease 증거를 보존한다. Application·receipt·attempt read clock 중 가장 이른 시각이 deadline/lease expiry 전이면 `not_ready`다. 현재 구현은 `reservation_expiry + reserved` origin의 transition, cleanup claim, `absent -> planned|hold` dry-run target, generation-pinned local delete/complete-empty audit와 cleanup attempt progress persistence foundation까지다. [ADR-0023](../decisions/ADR-0023-fenced-cleanup-lease-claim.md), [EVD-20260722-031](../evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim), [ADR-0024](../decisions/ADR-0024-immutable-cleanup-dry-run-target.md), [EVD-20260722-032](../evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target), [ADR-0025](../decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md), [EVD-20260722-033](../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit), [ADR-0026](../decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md), [EVD-20260722-034](../evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence)을 따른다. 별도 absence-audit capability와 terminal code evidence는 아직 없다.

R8c delete/audit component와 R8d ledger persistence foundation은 local·synthetic 범위에서 구현됐지만 **현재 runtime 실행 금지**다. Delete 전송과 complete-empty audit를 분리하고 raw audit 전 manifest delete를 금지한다. 아래 상태는 immutable target이 아니라 exact cleanup attempt의 execution ledger다. 현재 Firestore store는 `planned`, dispatch와 delete outcome을 보존하지만 별도 read-only capability 없이는 absence-confirmed phase를 저장하지 않는다. Phase executor, progress-bearing takeover, terminal finalizer, reserved-origin hold, accepted deletion과 rejected side cleanup은 아직 구현되지 않았다.

```text
planned
  -> raw_dispatch_recorded
  -> raw_outcome_recorded
  -> raw_absence_confirmed
  -> manifest_dispatch_recorded
  -> manifest_outcome_recorded
  -> manifest_absence_confirmed
  -> completed
```

아래 항목은 구현 후 적용할 재개 규칙이다. 현재 구현 근거는 phase/revision 검증, target create-only 보존, dispatch/delete outcome persistence와 exact replay write-zero까지다. Crash audit-first orchestration, progress-aware takeover, absence-audit 승인과 finalization은 아직 실행 가능한 절차가 아니다.

- raw dispatch 뒤 crash: mutation을 바로 반복하지 않고 expected raw path의 complete inventory audit부터 수행한다.
- manifest dispatch 뒤 crash: 같은 target과 durable phase를 확인하고 expected manifest path의 audit부터 수행한다.
- delete RPC outcome과 absence audit outcome은 별도 field로 보존하며 `not_found_observed`를 `confirmed_absent`로 해석하지 않는다.
- target은 create-only로 유지하고 execution phase를 target status로 기록하지 않는다.
- target 생성 뒤 lease renewal은 target의 immutable revision·heartbeat·expiry binding을 깨므로 금지한다.
- progress가 있는 attempt의 lease가 만료되면 next claim은 prior target·plan hash와 단조 phase를 함께 검증하고, progress를 보존한 `failed/lease_expired` closure와 새 fence·attempt를 한 transaction에 기록한다. Malformed residue나 disposition이 있으면 takeover write는 0이다.
- `verified_empty` target과 dispatch 응답 유실은 delete capability가 아닌 별도 read-only absence-audit capability로 두 expected path를 fresh 감사한다. Classification 당시 empty였다는 이유만으로 `expired` 처리하지 않는다.
- Finalization은 attempt phase를 `completed`로 바꾸고 execution revision도 정확히 1 증가시키는 write를 receipt·두 index와 같은 transaction에 포함한다.
- target 생성 뒤 새 generation 발견: 기존 target을 교체하지 않고 hold한다.
- 삭제 뒤 version-aware live generation을 다시 검사하고, late generation이 있으면 `expired` 완료를 금지한다.
- exact generation NotFound: 단독 성공으로 기록하지 않는다. Regular-version과 soft-deleted inventory가 모두 complete empty인지 확인한 뒤에만 `confirmed_absent`로 분류한다.
- target generation이 soft-deleted 상태이거나 같은 path에 다른 generation이 있으면 기존 target을 바꾸지 않고 hold/finding으로 분리한다.
- artifact expiry 전 stored-missing만 즉시 data-loss finding으로 분류한다. expiry 이후에는 승인된 lifecycle/deletion evidence와 `deleting/deleted` workflow를 먼저 대조한다.
- 만료 전 hold는 review due를 artifact expiry보다 앞에 둔다. 이미 만료된 finding은 review due를 현재시각으로 두고 즉시 integrity cleanup 대상으로 보내며, cleanup이 막히면 privacy/operations incident로 escalate한다.
- artifact cleanup·감사가 끝날 때까지 `purge_eligible_at`은 null이다. 완료 transaction이 두 index와 receipt에 같은 eligibility를 설정하되 독립 TTL 삭제는 사용하지 않는다.
- eligibility 뒤 purge job은 receipt 하위 attempt와 linked cleanup target·integrity finding을 bounded cursor로 먼저 삭제한다. 세 집합이 empty임을 증명한 뒤에만 마지막 transaction으로 두 index와 receipt를 함께 삭제한다. Firestore parent delete가 subcollection을 지운다고 가정하지 않는다.

## 9. Local/CI 검증 체크리스트

R5 read-only classifier의 독립 완료 기준은 [ADR-0018](../decisions/ADR-0018-generation-pinned-read-only-classifier.md)을 따른다. 아래 목록은 이후 reconciler·cleanup까지 포함한 전체 recovery checklist이며 R5 하나의 완료 조건으로 해석하지 않는다.

2026-07-21 local R5 classifier gate는 [EVD-20260721-023](../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier), current forward authorization gate는 [EVD-20260721-024](../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization)에서 통과했다. 전자는 synthetic classifier matrix와 pinned official Storage testbench reader, 후자는 provider-neutral policy와 Firestore Emulator의 claim→authorization→consent withdrawal을 검증한다. 둘을 하나의 startup worker로 연결하거나 staging authorization·lifecycle과 아래 전체 recovery checklist를 완료한 증거는 아니다.

2026-07-22 bounded candidate query·fixed-cutoff checkpoint·claim outer loop의 local/Emulator gate는 [EVD-20260722-029](../evidence/2026-07.md#evd-20260722-029--bounded-forward-recovery-worker와-cross-run-checkpoint)에 기록한다. 이는 executable startup·scheduler·production index/IAM을 연결한 운영 검증이 아니다.

2026-07-22 cleanup transition의 expired forward attempt 원자 종료와 missing-attempt rollback은 [EVD-20260722-030](../evidence/2026-07.md#evd-20260722-030--cleanup-transition의-expired-forward-attempt-원자-종료)에서 local/Emulator로 검증했다.

같은 날 [ADR-0023](../decisions/ADR-0023-fenced-cleanup-lease-claim.md)에 따라 transition time·quiescence·mode·origin·policy를 immutable하게 기록하고, `11분 > 최대 lease 5분 + StoreBatch 전체 5분` 뒤 cleanup-only owner가 claim하도록 구현했다. First claim과 expired takeover는 exact `started` attempt를 transaction으로 만들고 forward mutation port를 거부한다. Local/Emulator/pinned testbench 근거는 [EVD-20260722-031](../evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim)이다. 이 claim은 target·artifact read/delete·`expired`·purge 권한이 아니며 runtime에 연결하지 않는다.

이어 [ADR-0024](../decisions/ADR-0024-immutable-cleanup-dry-run-target.md)의 cleanup 전용 read capability, full classification evidence seal과 immutable dry-run target을 구현했다. Concurrent same command는 target 1개와 created/replayed 각 1개로 수렴하고 conflicting target은 receipt·attempt를 포함해 write 0이다. [EVD-20260722-032](../evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target)의 local/Emulator 근거이며 actual delete와 completion 권한은 없다.

이어 [ADR-0025](../decisions/ADR-0025-generation-pinned-cleanup-delete-and-audit.md)의 concrete Firestore delete grant, exact generation+metageneration GCS delete와 complete regular/soft-deleted empty audit도 local component로 구현했다. Raw unknown/error 뒤 manifest mutation 0, raw-only/manifest-only counterpart audit, soft-deleted/late generation과 incomplete inventory fail-closed, inspect/delete 404 분리를 [EVD-20260722-033](../evidence/2026-07.md#evd-20260722-033--generation-pinned-cleanup-delete와-complete-empty-audit)에서 local/Emulator/pinned testbench/clean CI로 확인했다. Success observation은 shape-only이며 completion 권한이 아니다.

이어 [ADR-0026](../decisions/ADR-0026-fenced-cleanup-execution-ledger-and-expiry-finalization.md)의 pure ledger와 Firestore progress persistence foundation을 구현했다. Plan·target·fence·receipt revision과 phase revision을 묶고, planned initialize와 dispatch/delete outcome을 exact cleanup attempt에 저장하며 semantic replay는 write 0이다. Generic progress API는 별도 read-only evidence 없이 absence-confirmed phase를 저장하지 않는다. [EVD-20260722-034](../evidence/2026-07.md#evd-20260722-034--fenced-cleanup-execution-ledger와-firestore-progress-persistence)의 local/Emulator 범위이며 phase executor·terminal finalizer·runtime 권한은 아니다.

- [x] fake clock으로 lease exact-expiry boundary 재현
- [x] 두 request/sweeper/cleanup의 concurrent claim winner 1명
- [ ] recovery claim 대 `BeginCleanupTransition` 경계 경쟁에서 cleanup/recovery 중 허용된 winner만 1명
- [x] expired forward `started` attempt와 reserved cleanup transition이 같은 transaction에서 terminal+pending으로 commit되고 missing·malformed attempt는 write 0
- [x] cleanup takeover 뒤 stale forward renew/release/stored/rejected update 0
- [x] quiet boundary·immutable policy 뒤 cleanup first claim과 expired takeover가 attempt ledger와 함께 한 winner로 수렴
- [ ] raw-only → same raw generation manifest → stored
- [ ] no-artifact → Storage write 0
- [ ] forward/pre-expiry manifest-only·stored-missing → create/delete/finalize 0
- [ ] consent invalid pending → forward write 0
- [ ] timeout/403/429 → missing 분류 0
- [x] cleanup dry-run target의 path·generation·hash, classification·inventory와 receipt revision/fence 고정
- [x] shape-valid classification result tamper 거부, concurrent create/replay target 1개, conflicting target write 0과 receipt·attempt 불변
- [x] concrete Firestore current-state grant의 zero/forgery·stale revision/fence·terminal attempt·exact expiry 거부
- [x] raw exact generation+metageneration delete 뒤 complete empty audit 전 manifest mutation 0
- [x] raw timeout/cancel/unavailable 재감사 뒤 manifest delete 0, permission/quota/412 bounded fail-closed
- [x] inspect/delete 404와 complete-empty를 분리하고 soft-deleted·late counterpart generation을 완료로 해석하지 않음
- [x] cleanup ledger plan/target/fence와 phase revision 계약
- [x] Firestore ledger initialize·dispatch/delete outcome progress·exact replay write-zero
- [ ] read-only absence-audit capability와 audited phase persistence
- [ ] progress-aware expired cleanup takeover
- [ ] atomic completed/expired/purge-eligibility finalization
- [ ] `committed|not_committed|unverifiable` response-loss correlation
- [ ] 복수 manifest generation은 bytes 동일 여부와 관계없이 자동 선택 0
- [ ] post-expiry hold가 즉시 integrity cleanup 또는 incident escalation으로 연결
- [ ] accepted deletion 중 replay-complete, rejected side cleanup 중 replay-rejected 유지
- [ ] rejected artifact는 ownership+보안 승인 없으면 target/delete 0
- [ ] attempt가 transaction limit보다 많아도 resumable purge 뒤 orphan 0
- [x] actual delete test는 pinned official testbench의 synthetic generation만 사용
- [ ] recovery ledger와 log privacy scan 통과
- [x] Firestore Emulator와 GitHub clean runner 통과

## 10. Escalation과 문서 스트림

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
