---
id: UPD-20260723-05
date: 2026-07-23
status: draft
version_or_deployment: cleanup-control-r8i-local
roadmap_month: M3
owner: project owner
reviewed_at: 2026-07-23
---

# 제품 업데이트: Cleanup retry·hold control plane

## 요약

Cleanup execution 실패를 success로 오인하지 않고 실제 진행 phase와 bounded 원인을 durable하게 남기는 local control-plane component를 추가했다. Retry는 old provider fence가 만료된 뒤 exact evidence를 다시 검증한 경우에만 pristine attempt로 재개하고, hold는 사람이 검토하기 전 자동 재개하지 않는다. Executable·scheduler·사용자 화면과 staging·production에는 배포하지 않았다.

## 변경 전 문제

- Artifact delete outcome이 `unknown`이어도 timeout인지 response-unverifiable인지 process 종료 뒤 durable state만으로 구분할 수 없었다.
- Retry·hold enum은 있었지만 terminal persistence와 receipt cursor가 없어 실패 phase를 보존한 종료·재개 계약이 없었다.
- Lease를 제거한 직후 새 cleanup claim을 허용하면 old provider capability와 새 fence가 겹칠 수 있었다.
- Commit 응답이 유실됐을 때 disposition mutation을 반복하지 않고 결과를 판별할 전용 correlation이 없었다.

## 변경 후 동작

- Ambiguous outcome과 exact bounded error class를 같은 ledger revision에 저장한다.
- 10개 error class를 retry 15/30/60분 또는 24시간 review hold로 exhaustive mapping한다.
- Retry·hold attempt는 실제 마지막 phase/revision을 보존한 `completed/cleanup_retry|cleanup_hold`로 닫는다.
- Firestore transaction은 attempt와 receipt 두 문서만 갱신하고 immutable cleanup target과 두 uniqueness index는 쓰지 않는다.
- Receipt는 exact terminal attempt ID, disposition, error class와 retry 또는 hold cursor 하나만 저장한다.
- Retry cursor는 old fence expiry와 class backoff 중 늦은 시각이다.
- Claim은 old terminal attempt·target·evidence·fence를 다시 검증하고 cursor를 clear한 뒤 state residue가 없는 새 attempt를 원자 생성한다.
- Hold review due는 자동 claim 시간이 아니며 due가 지나도 write 0으로 정지한다.
- Commit 응답 유실 query는 fresh read-only correlation에서 `committed|not_committed|unverifiable`만 반환하고 mutation을 재실행하지 않는다.

## 범위

- 포함: Pure retry·hold policy, durable error class, receipt cursor schema/validation, Firestore 2문서 transaction, response-loss outcome read, retry claim과 hold stop.
- 제외: Phase executor terminal wiring, operator hold release, scheduler/startup/readiness/HTTP, accepted·held·rejected cleanup, nested purge, actual object delete, staging·production 배포.
- 배포 환경: `local component + Firebase demo Firestore Emulator`
- 데이터 유형: `synthetic control documents`; 실제 위치·사용자·기관 데이터 없음

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| 10-class policy와 phase preservation | Go domain table/contract tests | `pass` | [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition) |
| Attempt+receipt atomic commit, target/index write 0 | Firestore Emulator transaction suite | `pass` | [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition) |
| Retry exact-boundary single winner·pristine attempt | Unit/race + Firestore Emulator | `pass` | [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition) |
| Hold review due 뒤 auto-claim 0 | Contract test | `pass` | [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition) |
| Actual commit 뒤 response-loss correlation | Firestore Emulator wrapped runner + read-only outcome | `pass` | [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition) |
| 전체 Go gate와 clean runner | tidy/format/vet/race/build + GitHub Actions | `pass` | [CI 29945654886](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29945654886) |

## 배포와 롤백

- Firebase/GCS staging·production, 앱스토어와 사용자 환경 배포는 수행하지 않았다.
- `cmd/server` composition과 scheduler가 이 component를 호출하지 않아 현재 executable의 cleanup 동작은 바뀌지 않는다.
- Local code rollback은 구현 커밋 `318f3b5`의 revert로 가능하지만 durable production 문서가 생성되지 않았으므로 데이터 rollback 절차는 이번 업데이트 범위에 없다.
- 실제 runtime 연결 전에는 Firestore field/index/rules, IAM, bucket lifecycle과 operator hold workflow를 별도 release gate로 검증한다.

## 알려진 제한과 후속 작업

- Phase executor의 typed error와 disposition command가 아직 합성되지 않았다.
- Hold를 승인·해제하는 operator capability, UI, audit log와 runbook이 없다.
- Accepted/held/rejected origin cleanup과 nested attempt/target/finding purge는 구현하지 않았다.
- Firestore Emulator는 staging contention, quota, IAM, bucket versioning·soft-delete와 lifecycle 의미를 증명하지 않는다.
- Response-loss correlation은 사실 확인 전용이며 provider delete 또는 재시도 권한이 아니다.

## 관련 기록

- 결정: [ADR-0031](../decisions/ADR-0031-phase-preserving-cleanup-retry-hold-disposition.md)
- 증거: [EVD-20260723-039](../evidence/2026-07.md#evd-20260723-039--phase-preserving-cleanup-retryhold-disposition)
- 사람 대상 리포트: [HR-20260723-30](../reports/human/HR-20260723-30-cleanup-retry-hold-disposition.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: Codex와 independent contract/test review — 사람 검토 필요
- 실제 주장과 근거 일치 여부: local domain/Firestore adapter, Firebase demo Emulator와 clean CI 범위에서 일치
- 검토 메모: Durable retry·hold component를 deployed runtime, actual deletion 또는 현장 성과로 확대 해석하지 않는다.
