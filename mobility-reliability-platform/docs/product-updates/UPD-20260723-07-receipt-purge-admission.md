---
id: UPD-20260723-07
date: 2026-07-23
status: draft
version_or_deployment: receipt-purge-r8k-a-local
roadmap_month: M3
owner: project owner
reviewed_at: 2026-07-23
---

# 제품 업데이트: Receipt purge admission과 writer fence

## 요약

`purge_eligible_at`이 지난 terminal receipt에 deterministic purge job을 create-once하고 receipt writer fence를 같은 Firestore transaction으로 기록하는 R8k-a local control-plane component를 구현했다. Fence 뒤 기존 recovery/cleanup lease claim과 cleanup target create는 닫힌다. 다음 gate용 page discovery는 bounded advisory contract로 추가했지만 아직 linked document를 삭제하지 않으며 executable·scheduler와 staging·production에는 연결하지 않았다.

## 변경 전 문제

- `expired` receipt와 두 uniqueness index에 `purge_eligible_at`은 있었지만 실제 metadata purge의 owner, revision과 재개 상태가 없었다.
- Parent receipt를 먼저 지우면 nested attempt가 남고, cleanup target 같은 top-level child와의 관계를 복원할 수 없었다.
- Purge와 recovery/cleanup writer가 경쟁할 때 empty 확인 뒤 새 child가 생기는 것을 차단할 receipt-side fence가 없었다.
- Admission commit 응답 유실 뒤 mutation을 반복하지 않고 결과를 확인할 bounded query가 없었다.

## 변경 후 동작

- Tenant+receipt digest purge key와 post-fence receipt revision에 결합된 linkage hash를 계산한다.
- Purge job은 최소 control field만 보존하며 phase/revision·cursor/count·hold residue를 strict validator로 검사한다.
- Admission transaction은 receipt·두 index의 exact linkage와 동일 purge eligibility, terminal residue와 expected command를 다시 검증한다.
- Job create와 receipt의 세 purge fence field·revision update가 함께 commit되고 두 uniqueness index는 write 0이다.
- 동일 command 경쟁은 한 created와 한 replay로 수렴하고, valid progressed job도 immutable admission binding이 같으면 write-zero replay다.
- Partial fence, malformed/foreign job, command drift와 receipt/index drift는 fail-closed한다.
- Mutation error는 성공 status를 반환하지 않으며, pre-write outcome query를 사용한 fresh read-only correlation만 허용한다.
- Full fence 뒤 recovery/cleanup lease claim과 cleanup target create를 차단한다.
- Page discovery request는 최대 100개로 제한하고 query의 `page_size+1`번째 ID를 별도 lookahead로 보존한다. Candidate는 ordered document ID만 담은 advisory snapshot이며 delete 권한은 아니다.

## 범위

- 포함: Pure purge job/admission/outcome contract, deterministic key·linkage hash, strict Firestore job codec, receipt purge fence, create-once admission, progressed replay, response-loss outcome, 기존 attempt/target writer fence, bounded advisory page discovery와 unit/race·Emulator tests.
- 제외: Attempt paging/delete, target·finding inverse-link registry, legacy backfill, phase transition, final receipt/index delete, Rules/indexes, runtime·scheduler·production.
- 배포 환경: `local component + Firebase demo Firestore Emulator`
- 데이터 유형: `synthetic expired receipt and control indexes`; 실제 위치·사용자·기관 데이터 없음

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| Deterministic key/hash와 bounded contract | Go pure unit/race + reflection tests | `pass` | [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) |
| Job+receipt fence single transaction, index write 0 | Firebase demo Firestore Emulator concurrent admission | `pass` | [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) |
| Same/progressed replay와 partial fence fail-closed | Go unit/race + Emulator negative path | `pass` | [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) |
| Response-loss status·clock·read-only correlation | Go unit/race + independent review | `pass` | [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) |
| Bounded page discovery·lookahead·cursor validation | Go pure unit/race | `pass` | [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission) |
| 전체 workspace·Go gate | Local WSL2/Docker + clean GitHub Actions | `pass` | [CI 29955058278](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29955058278) |

## 배포와 롤백

- Firebase/GCP staging·production, 앱스토어와 사용자 환경 배포는 수행하지 않았다.
- `cmd/server`, scheduler/startup/readiness가 이 admission을 호출하지 않아 executable 동작은 바뀌지 않는다.
- Local code rollback은 admission/fence 커밋 `7a1d3ed`과 page-contract 커밋 `5374be6`의 revert로 가능하다. Production durable job·fence를 만들지 않아 이번 업데이트 범위의 데이터 rollback은 없다.
- Runtime 연결 전 R8k-b/c, Rules/indexes, global writer inventory와 staging IAM gate가 필요하다.

## 알려진 제한과 후속 작업

- 이번 gate는 purge를 시작할 소유권과 writer fence만 만들며 child document나 receipt/index를 삭제하지 않는다.
- Integrity finding writer와 inverse registry가 없어 future writer coverage가 완성되지 않았다.
- Out-of-band Admin/IAM writer는 application fence를 우회할 수 있으며 staging inventory·writer exclusion 전에는 orphan-zero를 주장할 수 없다.
- Next gate는 nested recovery attempt의 bounded page query/delete, cursor/count, phase transition과 response-loss correlation이다.

## 관련 기록

- 결정: [ADR-0033](../decisions/ADR-0033-fenced-resumable-receipt-linkage-purge.md)
- 증거: [EVD-20260723-041](../evidence/2026-07.md#evd-20260723-041--fenced-receipt-purge-admission)
- 사람 대상 리포트: [HR-20260723-32](../reports/human/HR-20260723-32-receipt-purge-admission.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: Codex와 independent read-only review — 사람 검토 필요
- 실제 주장과 근거 일치 여부: local contract/unit/race, Firebase demo Emulator와 workspace gate 범위에서 일치
- 검토 메모: Admission/fence를 purge 완료, actual delete 또는 현장 성과로 확대 해석하지 않는다.
