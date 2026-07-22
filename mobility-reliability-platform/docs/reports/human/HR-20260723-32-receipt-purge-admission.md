---
id: HR-20260723-32
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: 2026-07-23
roadmap_month: M3
technical_gate: R8k-a - receipt purge admission and writer fence
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Receipt purge admission과 writer fence

## 한눈에 보기

- 이번 회차의 사전 목적: Terminal receipt metadata를 parent-first delete 없이 정리하기 위한 durable purge owner와 writer fence를 먼저 만든다.
- 보고 기준일의 실제 상태: Commit `7a1d3ed`에서 bounded purge contract, deterministic key/hash, strict job codec, receipt fence, create-once Firestore admission, response-loss correlation과 기존 attempt/target writer 차단을 local·Emulator로 구현했다. Commit `5374be6`에서는 다음 gate의 advisory page discovery를 100개 상한·lookahead·ordered cursor로 제한했다.
- 가장 중요한 차이 또는 위험: Admission과 fence만 완료했으며 실제 nested attempt·target·finding 또는 receipt/index delete는 아직 0건이다.
- 사람에게 필요한 결정·확인: R8k-c와 staging 전까지 out-of-band Admin/IAM writer inventory와 least-privilege 정책을 확정해야 orphan-zero 또는 production purge를 주장할 수 있다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8k-a metadata lifecycle gate
- 계획한 기술 주제: Deterministic job identity, bounded durable state machine, receipt writer fencing, create-once transaction, response-loss provenance와 privacy-bounded result
- 예상 산출물: Pure purge contract, Firestore job codec, receipt fence, admission/outcome adapter, writer regression과 concurrent Emulator tests
- 검토할 질문: Index가 admission 때 바뀌는가, 두 요청이 job을 중복 생성하는가, partial fence가 child writer를 열어두는가, commit 오류를 성공으로 오인하는가, job이 진행된 뒤 replay가 불필요한 conflict가 되는가
- 계획 완료 조건: Local Go full gate, Firestore Emulator 전체 admission regression, workspace gate와 independent review를 통과하고 runtime은 미연결로 둔다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Purge contract | `검증됨` | Bounded job/status/cursor/count/hold validator와 deterministic key·linkage hash | Page DTO는 계약만 있고 worker 없음 | WSL2 Docker Go unit/race |
| Page discovery contract | `검증됨` | `page_size+1` 분리, 오름차순·중복·cursor regression 거부, caller slice 복사 | Advisory candidate일 뿐 transaction delete 권한 없음 | WSL2 Docker Go unit/race |
| Receipt fence | `검증됨` | 세 field all-or-none, expired terminal에서만 허용 | Integrity finding writer는 아직 없음 | Go validator tests |
| Atomic admission | `검증됨` | Job create+receipt fence/revision 한 transaction, 두 index write 0 | Child delete 0 | Firebase demo Firestore Emulator |
| Concurrency·replay | `검증됨` | Concurrent created 1/replayed 1, progressed valid job write-zero replay | Final linkage_deleted 뒤 replay는 이번 범위 밖 | Go race + Emulator |
| Response loss | `검증됨` | Error path status empty, mutation 재호출 없이 post-read trusted clock correlation | 실제 provider/network fault injection은 아님 | Go unit/race + review |
| Writer fence | `부분 검증됨` | Existing recovery/cleanup claim과 cleanup target create 차단 | Future integrity finding·inverse link writer 미구현 | Unit + Emulator partial-fence case |
| Runtime·제품 | `미연결` | `cmd/server`·scheduler·startup/readiness·HTTP·사용자 경로 변화 없음 | 의도된 격리 | Source composition + workspace gate |

### 실제 결과 상세

- Implementation commits: `7a1d3ed` (`feat: fence receipt purge admission`), `5374be6` (`feat: bound receipt purge page discovery`)
- Purge job은 tenant·receipt·post-fence revision·linkage hash와 최소 phase/cursor/count/timestamp만 보존한다. UID, device·trip·person ID, object path, payload와 좌표를 포함하지 않는다.
- Admission은 receipt와 두 uniqueness index를 읽어 exact linkage와 동일 `purge_eligible_at`, terminal state/residue, expected pre-revision을 다시 확인한다.
- Successful commit은 job과 receipt fence만 쓴다. Emulator에서 job/receipt `UpdateTime`이 같았고 두 index `UpdateTime`은 변하지 않았다.
- 같은 command가 경쟁하면 한 요청만 create하고 다른 요청은 replay한다. Job이 valid phase로 전진해도 immutable admission binding과 receipt fence가 일치하면 write 0 replay를 유지한다.
- Partial fence와 malformed job document는 fail-closed한다. Job decoder는 unknown field와 required field 누락을 거부한다.
- Commit 결과가 불명확한 오류에서는 `created`를 반환하지 않는다. Pre-write job/query만 남겨 fresh read가 결과를 분류하고 mutation은 반복하지 않는다.
- Independent review에서 발견한 status, clock, progressed replay 문제를 수정하고 재리뷰·focused test를 통과했다.
- Validation data는 synthetic control document와 Firebase demo Firestore Emulator뿐이다. 실제 GPS, 사용자·기관 데이터와 staging·production Firebase/GCS를 사용하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| R8k-a contract·admission·fence와 bounded page discovery | [EVD-20260723-041](../../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) | `verified` — local full gate/Emulator/clean CI | Codex + delegated task + independent review / 2026-07-23 |
| Terminal purge eligibility 선행 경계 | [EVD-20260722-038](../../evidence/2026-07.md#evd-20260722-038--atomic-cleanup-expiry-finalization과-response-loss-correlation) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |
| Terminal orchestration 선행 경계 | [EVD-20260723-040](../../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-23 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0033](../../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md) — R8k-a만 구현됐으므로 전체 결정 status는 계속 `proposed`
- 실제 제품 업데이트: [UPD-20260723-07](../../product-updates/UPD-20260723-07-receipt-purge-admission.md) — local control-plane component이며 배포·사용자 화면 변화 없음
- 인시던트: 해당 없음 — synthetic local/Emulator 검증이며 production·staging·field 영향이 없다.
- 열린 위험: R8k-b nested attempt paging, R8k-c inverse-link/final delete, Rules/indexes, global writer inventory, scheduler/startup/readiness와 staging IAM이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8k-b bounded nested recovery-attempt purge
- 실제 상태를 반영한 다음 검증: Advisory `page_size+1` query와 exact page transaction을 분리하고, delete set+cursor/count/job revision이 같은 commit에서 single winner로 수렴하는지 확인한다.
- 필요한 사람의 결정·지원: Production mutation 전 Admin/IAM writer 목록, legacy target·finding backfill 범위와 purge job 자체 보존기간을 확정한다.

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] 실제 주장마다 EVD를 연결했다.
- [x] Admission/fence를 child purge 완료 또는 actual delete로 표현하지 않았다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] Response-loss query를 mutation 권한으로 표현하지 않았다.
- [x] 제품 업데이트·인시던트·회의 상태를 각각 명시했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 리포트 내용을 검토했다.
