---
id: HR-20260721-09
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Fenced Telemetry Forward Admission
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: Lease·fencing 기반 telemetry forward path

## 한눈에 보기

- 이번 회차의 사전 목적: 동일한 `reserved` receipt를 여러 request가 동시에 처리하지 못하게 하고, lease takeover 뒤 stale worker가 Firestore 상태를 변경하지 못하게 한다.
- 보고 기준일의 실제 상태: ADR-0017 실행계획의 R1 immutable reservation input, R2 lease/fence domain contract와 R3 중 최초 lease·active replay·expired takeover·fenced finalizer forward path가 local worktree에 구현됐다.
- 가장 중요한 차이 또는 위험: `RenewLease`, 별도 `ClaimRecoveryLease`, attempt ledger, generation-pinned classifier/reconciler, bounded sweeper와 cleanup transition은 아직 없다. executable startup에도 연결하지 않아 readiness와 ingest는 계속 `503`이어야 한다.
- 사람에게 필요한 결정·확인: 이번 local evidence와 clean CI를 확인한 뒤 R4 HTTP status/retry 계약과 R3 잔여 primitive 중 무엇을 다음 구현 단위로 고정할지 검토해야 한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한다.

- 로드맵상 위치: M3 / immutable artifact lineage 이후 reservation 처리 소유권과 stale mutation 차단
- 계획한 기술 주제: immutable reservation input, initial request lease, monotonic fencing token, active replay short-circuit, expired lease takeover, fenced finalizer
- 예상 산출물: provider-neutral lease contract, Firestore atomic lease transaction, stale owner 경쟁 test, local Emulator evidence
- 검토할 질문: 한 receipt의 active owner가 하나뿐인가, active replay가 Storage를 호출하지 않는가, takeover 뒤 이전 token의 finalizer가 update 0으로 끝나는가
- 전체 계획 완료 조건: R1~R9의 classifier, reconciler, sweeper, cleanup, staging과 runtime gate까지 완료해야 하며 이번 회차는 그 전체 완료를 의미하지 않는다.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| R1 reservation input | `local 구현` | `reservation_deadline`, `artifact_expires_at`, `receipt_retention_floor`, nullable `purge_eligible_at`, sample bounds와 validator version을 분리 | purge workflow는 미구현 | Docker Go / synthetic |
| R2 lease/fence contract | `local 구현` | owner kind·proposal·grant·fence, 30초~5분 duration과 timestamp ordering, client wire field 거부를 고정 | external HTTP retry contract는 미확정 | unit test |
| 최초 admission lease | `부분 검증` | authorization·index 2개·receipt와 initial `fencing_token=1` lease를 같은 Firestore transaction에서 생성 | production Firestore·ADC/IAM 미검증 | local fake seam + Firestore Emulator |
| active replay | `부분 검증` | 유효 lease가 있으면 `replay_in_progress`로 반환하고 artifact adapter call을 만들지 않음 | HTTP status·retry hint 미구현 | service unit test |
| expired takeover | `부분 검증` | 만료 lease replay가 owner를 바꾸고 token·revision을 1 증가시킴 | 별도 sweeper claim은 없음 | unit/Emulator test |
| fenced finalizer | `부분 검증` | `MarkStored`·`MarkRejected`가 current owner/token/deadline과 receipt server read time을 확인하고 stale·expired fence mutation을 거부 | `RenewLease`와 cleanup 경쟁 primitive 미구현 | unit/Emulator test |
| transient release | `local 구현` | transient artifact/finalizer 실패에서 현재 fence를 확인해 lease를 release하고 recovery backoff를 기록 | attempt ledger·worker 재처리 없음 | service unit test |
| runtime·staging | `미착수` | startup dependency wiring을 하지 않음 | `/healthz` 외 readiness·ingest는 fail-closed 유지 | 미검증 |

### 실제 결과 상세

- 하나의 `expires_at`이 처리 deadline, artifact expiry와 receipt 보존을 동시에 뜻하지 않도록 필드를 분리했다.
- 최초 요청의 owner ID는 server-side proposal이며 client payload의 `leaseOwnerId`·`fencingToken`은 unknown field로 거부한다.
- active lease replay는 기존 receipt를 읽기만 하고 artifact 저장을 수행할 권한을 얻지 않는다.
- lease가 만료된 replay만 새 owner와 다음 fencing token을 얻는다. 이전 owner가 늦게 끝나도 같은 receipt를 stored/rejected로 바꾸지 못한다.
- app clock이 lease 전을 가리키더라도 Firestore receipt read time이 expiry 뒤면 finalizer·release는 update 0으로 닫힌다. 양쪽 clock skew가 5초를 넘는 경우도 fail-closed한다.
- stored sample count가 reservation의 expected count와 다르거나 replay receipt의 device·trip·consent·captured bounds가 현재 request와 다시 결합되지 않으면 artifact write·terminal update를 수행하지 않는다.
- 이미 terminal인 receipt의 동일 전체 lineage 또는 동일 rejection code replay는 mutation·revision 증가 없이 read-only로 기존 결과를 반환할 수 있다.
- 데이터 유형: `synthetic | test`; 실제 GPS, UID/App ID, 이용자·복지관 데이터 없음

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| lease·fencing·cleanup의 전체 목표와 불변조건이 결정됨 | [ADR-0017](../../decisions/ADR-0017-fenced-ingest-recovery.md) | `accepted` decision; 구현 증거 아님 | 문서 검토 필요 |
| R1/R2와 R3 forward path가 local worktree와 test에서 관찰됨 | [EVD-20260721-017](../../evidence/2026-07.md) | `generated` — 최신 전체 local gate와 clean CI 확인 전 | 사람 검토 필요 |
| recovery 전체 R1~R9가 구현되고 staging에서 동작함 | 확인 필요 — 현재 해당하지 않음 | `미검증` | 해당 없음 |
| executable이 인증된 production telemetry를 처리함 | 확인 필요 — 현재 활성화하지 않음 | `미검증` | 해당 없음 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0017](../../decisions/ADR-0017-fenced-ingest-recovery.md), [ADR-0016](../../decisions/ADR-0016-immutable-telemetry-artifact-lineage.md), [ADR-0015](../../decisions/ADR-0015-atomic-telemetry-admission.md)
- 실행계획: [Telemetry Reservation Recovery 실행계획](../../plans/TELEMETRY_RECOVERY_PLAN.md)
- 실제 제품 업데이트: 해당 없음 — runtime·사용자·운영 경로에는 연결하지 않음
- 인시던트: 해당 없음 — production·field 배포와 사용자 영향 없음
- 열린 위험: owner heartbeat 부재, recovery worker·attempt ledger 부재, deadline cleanup 경쟁 미검증, external HTTP retry 계약 미확정, staging clock skew·IAM 미검증

## 다음 회차

- 8개월 계획상 다음 주제: R3 잔여 `RenewLease`·명시적 recovery claim·cleanup transition 경쟁을 닫고 R4 request ownership 계약으로 진행
- 실제 상태를 반영한 다음 검증: renew 대 takeover, 두 recovery owner 경쟁, stale finalizer 대 cleanup transition, deadline 경계와 양방향 clock skew
- 필요한 사람의 결정·지원: local EVD·clean CI 검토, staging Firebase/GCP project와 service account 일정

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획 전체와 현재 R1/R2+R3 forward 부분 구현을 분리했다.
- [x] local synthetic·Emulator와 staging/production을 구분했다.
- [x] runtime 미연결 변경을 제품 업데이트로 기록하지 않았다.
- [x] local test 실패를 사용자 영향 인시던트로 기록하지 않았다.
- [x] 참석자·사진·지출을 생성하거나 추정하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [ ] EVD-20260721-017의 최신 전체 local gate와 clean CI 결과를 확인했다.
- [ ] reviewer와 발행일을 사람이 확정했다.
