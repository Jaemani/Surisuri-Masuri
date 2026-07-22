---
id: UPD-20260723-06
date: 2026-07-23
status: draft
version_or_deployment: cleanup-control-r8j-local
roadmap_month: M3
owner: project owner
reviewed_at: 2026-07-23
---

# 제품 업데이트: Cleanup terminal orchestration

## 요약

Durable cleanup phase execution과 이미 분리돼 있던 success finalizer·retry/hold disposition을 하나의 local orchestration 경계로 합성했다. Phase 결과가 exact sealed terminal intent를 가질 때만 finalization 또는 disposition 중 하나를 invocation당 최대 한 번 호출한다. Commit 응답 유실은 mutation을 반복하지 않고 짧은 read-only correlation으로만 확인한다. Executable·scheduler·사용자 화면과 staging·production에는 배포하지 않았다.

## 변경 전 문제

- R8g phase executor는 성공 시 `ready_for_finalization`에서 멈췄고 R8h finalizer를 직접 호출하지 않았다.
- R8i retry·hold store는 구현됐지만 typed execution failure를 어느 terminal로 전달할지 합성 경계가 없었다.
- Exported result나 Go error를 호출자가 해석하면 generic control error, persistence ambiguity와 provider failure를 혼동할 수 있었다.
- Success와 failure terminal을 독립 재시도하면 같은 invocation에서 mutation surface가 두 개 열릴 수 있었다.
- Parent cancellation이 terminal commit 응답 유실의 원인이면 같은 context의 correlation도 즉시 취소될 수 있었다.

## 변경 후 동작

- `PhaseExecutor.Execute` public signature는 유지하고 package-private sealed intent만 terminal 권한으로 인정한다.
- Exact success ledger는 finalizer로, exact bounded failure 또는 stored durable unknown class는 retry·hold disposition으로만 전달한다.
- Success finalizer와 failure disposition의 mutation 호출 합계는 invocation당 최대 1회다.
- Outcome·audit persistence ambiguity, unknown mutation status, generic/internal error, conflicting multi-class error와 sentinel 사칭은 terminal mutation 0이다.
- Incomplete inventory를 다른 unavailable과 구분하는 전용 bounded sentinel을 추가하되 malformed·duplicate·identity mismatch는 incomplete로 축소하지 않는다.
- Terminal commit response loss는 valid pre-state query가 있을 때만 parent cancellation과 분리한 최대 5초 read-only correlation을 수행한다.
- Direct·correlated result의 digest, revision, fence, cursor, purge와 lease residue를 다시 검증한다.
- 외부 결과는 full receipt·command·query·path·Firebase UID·user/device/trip/person identity record·좌표를 제외한 bounded `TerminalResult`만 사용한다. Control correlation용 attempt ID는 허용한다.

## 범위

- 포함: Sealed terminal intent, bounded failure mapper, durable unknown restart, same-control-store constructor, single terminal routing, read-only response-loss correlation, bounded result와 unit/race tests.
- 별도 확인: R8h finalization과 stale R8i disposition의 Firestore terminal-store 경쟁, immutable target·atomic commit lineage.
- 제외: `TerminalOrchestrator.Run`의 concrete Firestore/GCS end-to-end vertical slice, operator hold release, held/accepted/rejected cleanup, nested purge, scheduler/startup/readiness, actual object delete.
- 배포 환경: `local component + Firebase demo Firestore Emulator terminal-store tests`
- 데이터 유형: `synthetic cleanup control documents`; 실제 위치·사용자·기관 데이터 없음

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| Sealed success/disposition과 single terminal mutation | Go unit/race orchestration tests | `pass` | [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration) |
| Public result·generic/mixed error·persistence ambiguity에서 mutation 0 | Go negative-path unit/race tests | `pass` | [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration) |
| Durable unknown class 복원과 inventory completeness 분리 | Phase executor·GCS adapter tests | `pass` | [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration) |
| Parent cancellation 뒤 detached read-only correlation | Go fake-resolver race tests | `pass` | [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration) |
| Cross-terminal Firestore atomic lineage | Firebase demo Firestore Emulator store race | `pass` | [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration) |
| 전체 workspace·Go·container gate | GitHub Actions | `pass` | [CI 29949807881](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29949807881) |

## 배포와 롤백

- Firebase/GCS staging·production, 앱스토어와 사용자 환경 배포는 수행하지 않았다.
- `cmd/server` composition, startup, scheduler와 readiness가 이 orchestrator를 호출하지 않아 현재 executable 동작은 바뀌지 않는다.
- Local code rollback은 구현 커밋 `a340e8b`의 revert로 가능하다. Production durable document를 만들지 않았으므로 데이터 rollback 절차는 이번 업데이트 범위에 없다.
- Runtime 연결 전에는 concrete Firestore/GCS vertical slice, staging IAM·bucket lifecycle·writer exclusion과 operator workflow를 별도 release gate로 검증한다.

## 알려진 제한과 후속 작업

- Orchestrator unit/race와 terminal stores의 Emulator 경쟁은 확인했지만 concrete PhaseExecutor→Firestore/GCS→terminal 전체 vertical slice는 없다.
- Hold를 승인·해제하는 operator capability, UI, audit log와 runbook이 없다.
- Held/accepted/rejected-origin cleanup과 nested attempt/target/finding purge는 구현하지 않았다.
- Scheduler/startup/readiness, metrics exporter와 actual object delete runtime은 미연결이다.
- Firestore Emulator는 staging contention, quota, IAM, bucket versioning·soft-delete와 lifecycle 의미를 증명하지 않는다.

## 관련 기록

- 결정: [ADR-0032](../decisions/ADR-0032-bounded-cleanup-terminal-orchestration.md)
- 증거: [EVD-20260723-040](../evidence/2026-07.md#evd-20260723-040--bounded-cleanup-terminal-orchestration)
- 사람 대상 리포트: [HR-20260723-31](../reports/human/HR-20260723-31-cleanup-terminal-orchestration.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: Codex와 independent contract/test review — 사람 검토 필요
- 실제 주장과 근거 일치 여부: local orchestration unit/race, Firebase demo terminal-store Emulator 경쟁과 clean CI 범위에서 일치
- 검토 메모: Local composition을 deployed cleanup runtime, actual deletion 또는 현장 성과로 확대 해석하지 않는다.
