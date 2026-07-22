---
id: UPD-20260723-08
date: 2026-07-23
status: draft
version_or_deployment: receipt-purge-r8k-b-local
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: Nested recovery-attempt bounded purge

## 요약

R8k-a에서 생성한 receipt purge fence 뒤 nested `recoveryAttempts`를 bounded page로 발견하고, transaction 안에서 current page와 exact child를 다시 검증한 뒤 child delete와 job cursor/count/revision을 원자 commit하는 R8k-b local component를 구현했다. Attempt phase는 `planned -> attempts_purging`으로 시작하고 fresh exact-empty 확인 뒤 `linked_documents_purging`으로 전환할 수 있다. Cleanup target·integrity finding과 receipt·두 uniqueness index는 삭제하지 않으며 executable·scheduler와 staging·production에는 연결하지 않았다.

## 변경 전 문제

- R8k-a는 purge job과 receipt writer fence, bounded advisory page DTO만 만들었고 nested attempt를 실제로 지우는 transaction이 없었다.
- Transaction 밖 query 결과만 믿고 child를 지우면 discovery 뒤 삽입·변경된 page를 건너뛰거나 foreign/malformed document를 삭제할 수 있었다.
- Child delete와 cursor/count가 다른 commit이면 응답 유실·경쟁·중단 뒤 중복 집계나 skip이 생길 수 있었다.
- Progress-aware cleanup takeover가 남긴 historical failure ledger와 위조된 terminal residue를 구분하는 strict validation이 필요했다.
- 마지막 non-empty page를 삭제했다는 사실만으로 다음 phase를 열면 cursor 뒤의 새 문서를 놓칠 수 있었다.

## 변경 후 동작

- Page query는 purge key·tenant·receipt·expected status/revision/cursor와 최대 `page_size+1`에 결합된 advisory observation만 만든다.
- Commit transaction은 current job·receipt fence와 current page+lookahead를 다시 읽고 discovery observation과 exact 일치를 확인한다.
- Candidate attempt 각각을 다시 읽어 raw document digest, known field type, owner별 terminal union과 tenant/receipt binding을 검증한다.
- Valid terminal attempts만 삭제하고 `attempt_cursor`, committed delete count, job revision·updated time을 같은 transaction에서 전진시킨다.
- Same-page concurrent worker는 한 commit winner로 수렴한다. Stale observation, max revision과 unavailable read는 mutation 0이다.
- Malformed·foreign·unsupported·nonterminal·post-fence child, receipt/job linkage drift, partial fence와 count overflow는 child를 skip하지 않고 bounded hold로 남긴다.
- Progress-preserving `failed/lease_expired` cleanup attempt는 owner와 historical phase shape가 valid하면 purge할 수 있고 위조 phase/revision은 hold한다.
- Fresh exact-empty page를 completion transaction에서 다시 확인한 경우에만 attempts phase를 끝낸다.
- Page·phase commit 결과가 불명확하면 mutation을 반복하지 않고 sealed pre-state-bound query의 fresh read-only correlation만 수행한다.

## 범위

- 포함: Attempt state/digest contract, owner별 terminal/historical validation, bounded advisory query, exact transactional requery/reread/delete, atomic cursor/count/revision, poison hold, attempts phase begin/complete, read-only response-loss outcome과 unit/race·Firestore Emulator tests.
- 제외: Cleanup target·integrity finding inverse-link registry/backfill, linked child purge, unregistered child inventory, `ready`, final receipt+two-index delete, purge job retention, Rules/indexes, runtime·scheduler·production.
- 배포 환경: `local component + Firebase demo Firestore Emulator`
- 데이터 유형: `synthetic expired receipt, purge job and recovery attempts`; 실제 위치·사용자·기관 데이터 없음

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| Bounded page+lookahead와 stale page 차단 | Go pure/unit/race + Firebase demo Emulator pagination·insert regression | `local pass` | [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) |
| Exact reread/delete와 cursor/count/revision atomicity | Firestore Emulator page transaction | `local pass` | [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) |
| Same-page single winner와 poison hold | Go race + Emulator concurrent/negative paths | `local pass` | [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) |
| Owner별 terminal union과 progress-preserving cleanup failure | Unit matrix + Emulator valid delete/forgery hold | `local pass` | [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) |
| Fresh-empty phase transition과 response-loss correlation | Go unit/race + Emulator page/phase ambiguity paths | `local pass` | [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) |
| 전체 workspace·Go gate | Local WSL2/Docker | `pass` | [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) |
| Clean GitHub Actions | [CI 29961473929](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29961473929) | `success` | [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge) |

## 배포와 롤백

- Firebase/GCP staging·production, 앱스토어와 사용자 환경 배포는 수행하지 않았다.
- `cmd/server`, scheduler/startup/readiness가 R8k-a/b를 호출하지 않아 executable 동작은 바뀌지 않는다.
- Local code rollback은 commit `24c0050a69e0e31a65281638ee8f8bd6924949e0`의 revert로 가능하다. Production durable attempt delete나 phase mutation을 만들지 않아 이번 업데이트 범위의 운영 데이터 rollback은 없다.
- Runtime 연결 전 R8k-c inverse registry/backfill/final linkage, Rules/indexes, global writer inventory와 staging IAM gate가 필요하다.

## 알려진 제한과 후속 작업

- 이번 gate는 nested recovery attempt와 attempts phase만 처리한다. `linked_documents_purging`은 target/finding이 삭제됐거나 전체 purge가 완료됐다는 뜻이 아니다.
- Historical cleanup target/plan hash는 구조적 digest shape까지만 확인하며 historical fence expiry를 재구성하지 않는다.
- Integrity finding writer와 inverse registry가 아직 없어 future top-level child coverage가 완성되지 않았다.
- Out-of-band Admin/IAM writer는 application fence를 우회할 수 있으며 staging inventory·writer exclusion 전에는 orphan-zero를 주장할 수 없다.
- Next gate는 target·finding inverse-link registry, legacy backfill, bounded link+child purge와 final linkage transaction이다.

## 관련 기록

- 결정: [ADR-0033](../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md)
- 증거: [EVD-20260723-042](../evidence/2026-07.md#evd-20260723-042--bounded-nested-recovery-attempt-purge)
- 사람 대상 리포트: [HR-20260723-33](../reports/human/HR-20260723-33-nested-recovery-attempt-purge.md)
- 선행 업데이트: [UPD-20260723-07](./UPD-20260723-07-receipt-purge-admission.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: human-review-required
- 실제 주장과 근거 일치 여부: local contract/unit/race, Firebase demo Emulator, workspace gate와 clean CI 범위에서 일치한다.
- 검토 메모: Nested attempt purge를 target/finding purge, final receipt/index delete, runtime 배포 또는 현장 성과로 확대 해석하지 않는다.
