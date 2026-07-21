---
id: HR-20260721-15
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Current-state Forward Recovery Authorization
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: current-state forward recovery authorization

## 한눈에 보기

- 이번 회차의 사전 목적: sweeper lease를 위치 artifact 권한으로 사용하지 않고, current control-plane 관계를 다시 확인한 뒤 authoritative receipt에 결합된 짧은 read grant만 발급한다.
- 보고 기준일의 실제 상태: provider-neutral system authorizer와 read-only Firestore transaction adapter가 `main`에 구현됐다. Unit/race/vet/build, Firestore Emulator current-consent 철회와 기존 admission 경쟁 suite, workspace 회귀 검증, 독립 재리뷰와 GitHub clean CI를 통과했다.
- 가장 중요한 차이 또는 위험: current authorizer는 구현됐지만 classifier·worker·scheduler와 executable에 연결하지 않았다. Firestore authorization read와 첫 Storage read 사이에는 최대 30초보다 짧은 bounded TOCTOU window가 남는다.
- 사람에게 필요한 결정·확인: EVD-024의 local/Emulator 범위를 확인하고, readiness를 닫은 채 R6 forward reconciler와 startup composition으로 진행할지 확정한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한 계획이다.

- 계획한 기술 주제: service-side current authorization, authoritative request construction, Firestore transaction snapshot, opaque capability와 expiry clamp.
- 검토할 질문:
  - claim이 처리 소유권만 주고 artifact read permission은 별도로 확인되는가.
  - worker가 Firebase 사용자를 가장하지 않고 beneficiary relation을 current fact로 검증하는가.
  - caller가 stale receipt revision, path 또는 fence를 넣어 request를 조립할 수 없는가.
  - consent withdrawal, installation/membership/assignment 비활성화와 stale fence를 provider call 전에 차단하는가.
  - UID, App ID, person ID와 provider detail이 request/grant/result/error로 새지 않는가.
- 완료 조건: pure policy table, error·expiry boundary, same-transaction exact read, consent withdrawal Emulator, 기존 admission race 회귀와 독립 재리뷰가 통과한다.

## 2. 실제 상태

| 항목 | 상태 | 확인된 결과 | 아직 아닌 것 | 검증 환경 |
| --- | --- | --- | --- | --- |
| authoritative request | `local 통과` | caller DTO 없이 current receipt에서 request·deterministic path·fence 생성 | worker composition | WSL2 Docker / synthetic |
| current system policy | `local·clean CI 통과` | tenant·beneficiary·installation·trip·assignment·consent revision/state 검증 | production identity/IAM | WSL2 Docker / GitHub runner |
| Firestore snapshot | `local·clean CI 통과` | index 2개+receipt+관계 7종을 read-only transaction에서 확인 | production contention/clock | Firestore Emulator / race |
| capability | `local 통과` | unexported forward issuer, full request binding, accepted purpose 재사용 차단 | integrity-audit issuer | WSL2 Docker / unit |
| 시간 경계 | `local 통과` | ±5초 clock skew, 30초 이하 TTL과 8개 expiry source clamp | staging latency tuning | fake clock |
| 철회 경계 | `Emulator 통과` | claim 뒤 current consent withdrawal 시 새 grant 0, receipt mutation 0 | grant 발급 직후 race의 완전 제거 | Firestore Emulator |
| 독립 리뷰 | `완료` | stale state error 의미 Medium 수정 뒤 잔여 High/Medium 없음 | staging security review | read-only review + 재리뷰 |
| runtime | `미연결` | startup/worker가 authorizer·classifier를 사용하지 않음 | reconciler, scheduler, readiness | 해당 없음 |

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 환경·확인 |
| --- | --- | --- | --- |
| current authorizer와 Firestore adapter가 local/Emulator/clean CI gate를 통과함 | [EVD-20260721-024](../../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization) | `verified` | WSL2 Docker + Firestore Emulator + GitHub runner |
| lease/fencing claim과 transaction 경쟁의 선행 증거 | [EVD-20260721-018](../../evidence/2026-07.md#evd-20260721-018--recovery-claimrenewcleanup-transition과-read-time-coherence) | `verified` | Firestore Emulator + clean CI |
| grant를 소비하는 read-only classifier | [EVD-20260721-023](../../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier) | `verified` | WSL2 Docker + official reader testbench |

## 4. 이번 증분의 기술 경계

```text
tenant_id + reservation_key + sweeper LeaseGrant
  -> Firestore read-only transaction
     -> idempotency index + client-batch index + receipt linkage
     -> tenant + installation + trip + consent revision
     -> beneficiary membership + assignment + consent state
  -> provider-neutral current-state policy
  -> authoritative ArtifactClassificationRequest
  -> short-lived opaque forward read grant

이 증분에 없음
  -> Storage inventory/read 실행
  -> manifest create 또는 raw rewrite
  -> receipt/index/attempt mutation
  -> forward reconciler·bounded sweeper
  -> startup/runtime wiring과 readiness 활성화
```

## 5. 중요한 수정과 위험

- 첫 구현은 authoritative receipt를 request로 변환한 뒤 stale state를 검사해 정상 stored/rejected/cleanup 전환을 malformed/unavailable로 잘못 분류할 수 있었다. 독립 리뷰에서 Medium으로 확인했다.
- Request 변환 전에 eligibility를 분리했다. Known non-reserved·released·current non-sweeper와 structurally valid stale fence는 unauthorized, unknown enum·partial lease·trusted state 손상만 unavailable이다. 회귀 test와 재리뷰를 통과했다.
- Firebase UID는 installation과 membership의 관계 확인에만 사용하고 domain request·grant로 복사하지 않는다. Worker가 synthetic principal을 만들지 않는다.
- 열린 위험: authorization transaction 직후 consent가 바뀌는 TOCTOU를 완전히 없애지 못한다. Grant를 30초 이하로 제한하고 fence·request binding을 모든 Storage 경계에서 다시 검사한다.
- 열린 위험: Emulator는 production ADC/IAM, latency, retry/contention과 clock behavior를 증명하지 않는다.
- 열린 위험: authorizer와 classifier가 각각 검증됐지만 하나의 production startup composition으로 연결되지 않았다.

## 6. 다음 작업

1. Current authorizer→classifier만 가능한 package-private forward reconciler composition을 설계한다.
2. `valid_complete`는 같은 fence로 finalizer, `valid_raw_only`는 pinned raw에서 canonical manifest를 조건부 생성한 뒤 finalizer로 연결한다.
3. `none`, conflict, drift, unavailable을 receipt mutation과 분리한 action policy와 recovery-attempt completion ledger를 구현한다.
4. Staging Firebase/GCS에서 ADC/IAM, transaction contention, version/soft-delete와 grant TTL latency를 검증한다.
5. 위 gate 전에는 `/readyz=503`, ingest `503`과 scheduler 미연결을 유지한다.

## 7. 관련 기록

- 결정: [ADR-0019](../../decisions/ADR-0019-current-forward-recovery-authorization.md)
- 증거: [EVD-20260721-024](../../evidence/2026-07.md#evd-20260721-024--current-state-forward-recovery-authorization)
- 선행 리포트: [HR-20260721-14](./HR-20260721-14-read-only-artifact-classifier.md)
- 제품 업데이트: 해당 없음 — 사용자·운영·runtime 변화가 아님
- 인시던트: 해당 없음 — production·staging·field 영향 없음

## 8. 발행 전 확인

- [x] plan과 local/Emulator 실제 결과를 분리했다.
- [x] current authorization과 production runtime 완료를 분리했다.
- [x] 검증 중 발견·수정한 Medium과 독립 재리뷰 결과를 포함했다.
- [x] 참석자·사진·지출·사용자 수를 생성하지 않았다.
- [x] Product Update 또는 Incident를 만들지 않았다.
- [x] 구현 commit과 CI run을 연결했다.
- [ ] reviewer와 `issued_at`을 사람이 확정한다.
