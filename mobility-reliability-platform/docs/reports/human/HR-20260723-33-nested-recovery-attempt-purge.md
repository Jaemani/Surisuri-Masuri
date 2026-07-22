---
id: HR-20260723-33
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: R8k-b - bounded nested recovery-attempt purge
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Nested recovery-attempt bounded purge

## 한눈에 보기

- 이번 회차의 사전 목적: R8k-a receipt fence 뒤 nested recovery attempt를 parent-first delete 없이 bounded transaction으로 정리하고 중단·경쟁·응답 유실 뒤에도 job progress를 설명 가능하게 만든다.
- 보고 기준일의 실제 상태: Commit `24c0050a69e0e31a65281638ee8f8bd6924949e0`에서 bounded attempt query, transaction current-page 재조회와 exact child reread/delete, cursor/count/revision atomic progress, poison hold, attempts phase 전환과 read-only response-loss correlation을 local·Firebase demo Emulator로 구현했고 clean [CI run 29961473929](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29961473929)가 성공했다.
- 가장 중요한 차이 또는 위험: Synthetic nested attempt는 테스트에서 삭제했지만 target·finding·receipt·두 uniqueness index는 삭제하지 않았고 runtime도 미연결이다. `linked_documents_purging`은 전체 purge 완료 상태가 아니다.
- 사람에게 필요한 결정·확인: R8k-c 전에 inverse-link registry/backfill 범위와 out-of-band Admin/IAM writer inventory를 확정해야 한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8k-b metadata lifecycle gate
- 계획한 기술 주제: Bounded page discovery, exact transactional authority reconstruction, strict historical ledger validation, atomic delete progress, poison hold, phase exhaustion과 response-loss provenance
- 예상 산출물: Attempt contract/digest, Firestore query·page/phase transaction, sealed outcome query와 pure/unit/race·Emulator regression
- 검토할 질문: Query 뒤 삽입된 prefix를 건너뛰는가, child delete와 cursor/count가 갈라지는가, concurrent worker가 double-count하는가, malformed child가 skip되는가, valid cleanup takeover history를 오탐하는가, stale empty가 다음 phase를 여는가
- 계획 완료 조건: Local Go full gate, 분리된 Firestore Emulator admission+attempt suite, workspace gate, clean CI와 independent review를 통과하고 runtime은 미연결로 둔다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Attempt query | `local 검증됨` | Current job status/revision/cursor에 결합된 `page_size+1` ordered observation | Advisory이며 delete capability 아님 | WSL2 Docker Go + Firebase demo Emulator |
| Exact page transaction | `local 검증됨` | Current page 재조회, exact child reread, delete+cursor/count/revision 한 commit | Target/finding page는 없음 | Firebase demo Firestore Emulator |
| Attempt validation | `local 검증됨` | Raw map digest, known field type, owner별 terminal union과 valid cleanup historical failure | Historical target/plan은 structural digest shape만 확인 | Go unit/race + Emulator |
| Hold 경계 | `local 검증됨` | Malformed·foreign·unsupported·started·post-fence, linkage/fence drift와 count overflow에서 delete 0 hold | Operator hold release 없음 | Go unit/race + Emulator |
| Concurrency | `local 검증됨` | Same-page worker 중 한 transaction만 progress | Multi-phase runtime worker는 없음 | Firestore Emulator concurrent test |
| Phase transition | `local 검증됨` | `planned -> attempts_purging`, fresh exact-empty 뒤 `linked_documents_purging` | Linked document purge는 없음 | Go unit/race + Emulator |
| Response loss | `local 검증됨` | Page·phase error path status empty, mutation 재호출 없이 read-only `committed|not_committed|unverifiable` | 실제 provider/network fault injection은 아님 | Go unit/race + Emulator |
| Local full gate | `통과` | Go vet/race/build와 workspace check/build/test | Clean runner와 구분 | WSL2 Docker + local workspace |
| Clean CI | `통과` | Run 29961473929 success | 사람 리포트 검토는 별도 | GitHub Actions |
| Runtime·제품 | `미연결` | `cmd/server`·scheduler·startup/readiness·HTTP·사용자 경로 변화 없음 | 의도된 격리 | Source composition |

### 실제 결과 상세

- Implementation commit: `24c0050a69e0e31a65281638ee8f8bd6924949e0` (`feat: purge receipt recovery attempts`)
- Query는 최대 page 100과 별도 lookahead 한 건만 관측하고 document ID 오름차순·cursor regression을 검증한다.
- Transaction이 current page+lookahead를 다시 조회하므로 stale observation이나 discovery 뒤 prefix 삽입은 child delete·progress write 0이다.
- Exact terminal child만 삭제하며 committed set의 마지막 document ID와 실제 delete 수만 cursor/count에 반영한다.
- Raw Firestore document digest와 strict type/terminal-union 검증이 optional field drift, forged cleanup progress와 post-fence child를 hold로 전환한다.
- Progress-aware cleanup takeover가 prior execution phase를 보존한 valid `failed/lease_expired` attempt는 삭제 가능하다. 이 shape를 오탐하던 초기 validator를 owner별 historical union으로 정정하고 7개 nonterminal phase를 회귀검사했다.
- Same-page 경쟁은 한 winner로 수렴하며 count overflow는 delete 전에 hold, max revision은 write 0이다.
- Fresh empty는 completion transaction에서 다시 조회한다. Last non-empty page delete만으로 다음 phase를 열지 않는다.
- Ambiguous mutation은 성공 status를 반환하지 않고 pre-state-bound query를 보존한다. Correlation은 read-only이며 새로운 delete·phase 권한을 만들지 않는다.
- Validation data는 synthetic control document와 Firebase demo Firestore Emulator뿐이다. 실제 GPS, 사용자·기관 데이터와 staging·production Firebase/GCS를 사용하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| R8k-b attempt query/delete/progress, hold, phase와 outcome | [EVD-20260723-042](../../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) | `generated` — local full gate/Emulator/clean CI 통과, 사람 검토 대기 | Codex + independent review / 2026-07-23 |
| R8k-a purge admission과 writer fence | [EVD-20260723-041](../../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) | `verified` — local/Emulator/clean CI | Codex + delegated task + independent review / 2026-07-23 |
| Progress-aware cleanup historical ledger 선행 경계 | [EVD-20260722-036](../../evidence/2026-07.md#evd-20260722-036--progress-aware-expired-cleanup-takeover) | `verified` — local/Emulator/clean CI | Codex + independent review / 2026-07-22 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0033](../../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md) — R8k-a/b만 구현됐으므로 전체 결정 status는 계속 `proposed`
- 실제 제품 업데이트: [UPD-20260723-08](../../product-updates/UPD-20260723-08-nested-recovery-attempt-purge.md) — local control-plane component이며 배포·사용자 화면 변화 없음
- 인시던트: 해당 없음 — synthetic local/Emulator 검증이며 production·staging·field 영향이 없다.
- 열린 위험: R8k-c inverse-link registry/backfill, linked child·final linkage delete, Rules/indexes, global writer inventory, scheduler/startup/readiness와 staging IAM이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8k-c target·finding inverse-link registry와 final linkage purge
- 실제 상태를 반영한 다음 검증: Top-level child+inverse link의 atomic create, legacy well-formed child backfill, bounded link+child delete, fresh empty/unregistered inventory와 final job+receipt+two-index transaction을 분리해 검증한다.
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
- [x] Attempt phase 완료를 linked document·receipt/index purge 완료로 표현하지 않았다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] Response-loss query를 mutation 권한으로 표현하지 않았다.
- [x] 제품 업데이트·인시던트·회의 상태를 각각 명시했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [x] Clean CI run의 최종 성공 상태를 확인했다.
- [ ] 사람이 리포트 내용을 검토했다.
