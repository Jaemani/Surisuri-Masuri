---
id: HR-20260721-14
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Generation-pinned Read-only Artifact Classifier
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: generation-pinned read-only artifact classifier

## 한눈에 보기

- 이번 회차의 사전 목적: GCS generation inventory와 strict content validator를 결합해 pending recovery와 accepted integrity audit의 artifact 상태를 write 없이 분류한다.
- 보고 기준일의 실제 상태: 열 classification, purpose별 missing 의미, complete inventory, exact generation의 read 전후 snapshot, grant/fence deadline, provider 오류와 combined content precedence가 `main`에 구현됐다. WSL2 Docker 전체 gate, official Storage testbench, 독립 리뷰·재리뷰와 GitHub clean CI를 통과했다.
- 가장 중요한 차이 또는 위험: local R5 classifier는 완성됐지만 current authorizer가 grant를 발급하지 않고 startup·worker에 연결되지 않았다. staging의 version·soft-delete·IAM·retention 의미도 아직 검증하지 않았다.
- 사람에게 필요한 결정·확인: EVD-023의 local/test 범위를 확인하고, runtime을 열지 않은 채 R6 forward reconciler와 current recovery authorizer 설계로 진행할지 확정한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한 계획이다.

- 계획한 기술 주제: version-aware inventory, manifest-first exact read, accepted receipt pin, read 전후 drift, fail-closed classification과 authorization deadline.
- 검토할 질문:
  - 복수·soft-deleted·사라진 generation을 latest fallback 없이 구분하는가.
  - permission·quota·timeout·incomplete coverage를 artifact missing으로 바꾸지 않는가.
  - accepted 감사에서 receipt가 고정한 raw·manifest lineage 외 generation을 권위값으로 채택하지 않는가.
  - grant 또는 forward fence가 만료되면 새 provider call과 최종 결과 반환을 중지하는가.
  - result에 raw body·좌표·object path·token·UID/App ID가 남지 않는가.
- 이 증분의 완료 조건: 열 classification/reason matrix, forward·accepted happy/missing/drift/error 경로, actual deadline, privacy shape, 전체 Go gate, official reader testbench와 clean CI가 통과한다.

## 2. 실제 상태

| 항목 | 상태 | 확인된 결과 | 아직 아닌 것 | 검증 환경 |
| --- | --- | --- | --- | --- |
| forward classification | `local·clean CI 통과` | none, valid_raw_only, valid_complete, manifest_only와 conflict/drift/unavailable 분리 | R6 write/finalizer | WSL2 Docker / synthetic |
| accepted audit | `local·clean CI 통과` | stored·queued·projected exact lineage, stored_missing, soft-deleted pin, 다른 generation drift | deletion/lifecycle auditor | WSL2 Docker / synthetic |
| stable generation read | `local·clean CI 통과` | inventory→pre-inspect→exact read→post-inspect, generation/metageneration/attrs drift | staging bucket semantics | fake + official Storage testbench reader |
| authorization | `local·clean CI 통과` | request/grant binding 재검사, caller/grant/fence 중 가장 이른 deadline, 만료 시 domain error | current policy authorizer와 startup wiring | WSL2 Docker / synthetic |
| reason precedence | `local·clean CI 통과` | unavailable→generation drift→metadata→manifest→raw→missing→valid 경계와 accepted combined content 검증 | 운영 alert/action mapping | WSL2 Docker / synthetic |
| bounded result | `local·clean CI 통과` | count·coverage·generation digest만 반환하고 source path/body/identity 제외 | metrics·운영 log 전체 scan | WSL2 Docker / reflection/value scan |
| 독립 리뷰 | `완료` | unknown-purpose fail-open과 accepted 조기 precedence Medium을 수정한 뒤 잔여 High/Medium 없음 | staging security review | read-only review + race/vet |
| runtime | `미연결` | classifier constructor는 package-private이고 create/delete/receipt mutation 없음 | authorizer, worker, scheduler, readiness | 해당 없음 |

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 환경·확인 |
| --- | --- | --- | --- |
| 최종 classifier가 local 전체 gate·독립 재리뷰·clean CI를 통과함 | [EVD-20260721-023](../../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier) | `verified` | WSL2 Docker synthetic + GitHub runner |
| exact generation HTTP reader와 official testbench | [EVD-20260721-021](../../evidence/2026-07.md#evd-20260721-021--http-gcs-exact-generation-reader-official-testbench) | `verified` | pinned official Storage testbench |
| strict manifest/raw content와 reason precedence | [EVD-20260721-022](../../evidence/2026-07.md#evd-20260721-022--telemetry-artifact-content-lineage-validator) | `verified` | WSL2 Docker synthetic + clean CI |
| request/grant와 classification 계약 | [EVD-20260721-019](../../evidence/2026-07.md#evd-20260721-019--read-only-artifact-classification-계약과-strict-manifest-decoder) | `verified` | WSL2 Docker synthetic + clean CI |

## 4. 이번 증분의 기술 경계

```text
ArtifactReadAuthorizationGrant + ArtifactClassificationRequest
  -> manifest exact-path inventory
  -> unique generation pre-inspect/read/post-inspect
  -> strict manifest validation
  -> referenced or receipt-pinned raw inventory
  -> unique generation pre-inspect/read/post-inspect
  -> combined content validation
  -> classification + bounded reason/evidence

이 증분에 없음
  -> grant를 발급하는 current recovery/integrity authorizer
  -> object create/delete
  -> receipt/index/attempt mutation
  -> forward reconciler·sweeper·cleanup
  -> startup/runtime wiring과 readiness 활성화
```

## 5. 중요한 수정과 위험

- classification outcome helper가 앞선 request validation에 기대어 unknown purpose에도 일부 결과를 허용하던 문제를 26-case matrix가 발견했다. helper 자체에서 허용 purpose를 다시 검사하도록 수정했다.
- accepted 감사가 manifest conflict를 먼저 반환해 raw metadata 또는 provider unavailable을 관찰하지 못하던 조기 종료 문제를 독립 리뷰가 발견했다. 두 exact artifact를 stable read한 뒤 combined validator를 호출하도록 바꾸고 동시 손상·provider 실패 회귀 테스트를 추가했다.
- caller가 전달한 fence·accepted lineage pointer를 provider call 사이에 바꿔도 내부 request가 변하지 않도록 진입 시 복제하고 회귀 테스트로 고정했다.
- 열린 위험: 실제 GCS version/soft-delete inventory 의미와 IAM list 범위는 staging에서 확인하지 않았다.
- 열린 위험: result value scan은 통과했지만 runtime metrics·structured log·attempt ledger가 아직 연결되지 않아 end-to-end privacy scan은 남아 있다.
- 열린 위험: classifier는 action이 아니다. valid_raw_only나 conflict를 받아 write/hold/delete를 수행하는 R6 이후 정책은 별도 fence와 테스트가 필요하다.

## 6. 다음 작업

1. current tenant·installation·trip·assignment·precise-location consent를 재평가한 뒤에만 opaque grant를 발급하는 system recovery authorizer를 구현한다.
2. `valid_raw_only`이면 같은 raw generation으로 canonical manifest를 조건부 생성하고 current fence로 finalizer를 호출하는 R6 forward reconciler를 설계한다.
3. classification 뒤 recovery attempt의 completed/failed ledger를 bounded reason과 함께 갱신한다.
4. staging에서 GCS versioning·soft-delete·retention·IAM과 list/read operation 의미를 검증한다.
5. 위 조건 전에는 `/readyz=503`, ingest `503`과 worker/scheduler 미연결 상태를 유지한다.

## 7. 관련 기록

- 결정: [ADR-0018](../../decisions/ADR-0018-generation-pinned-read-only-classifier.md)
- 증거: [EVD-20260721-023](../../evidence/2026-07.md#evd-20260721-023--generation-pinned-read-only-artifact-classifier)
- 선행 리포트: [HR-20260721-13](./HR-20260721-13-artifact-content-validator.md)
- 제품 업데이트: 해당 없음 — 사용자·운영·runtime 변화가 아님
- 인시던트: 해당 없음 — production·staging·field 영향 없음

## 8. 발행 전 확인

- [x] plan과 synthetic/testbench 실제 결과를 분리했다.
- [x] local R5와 staging·runtime 미완료를 분리했다.
- [x] 검증 중 발견·수정한 결함과 독립 재리뷰 결과를 포함했다.
- [x] 참석자·사진·지출·사용자 수를 생성하지 않았다.
- [x] Product Update 또는 Incident를 만들지 않았다.
- [x] 구현 commit과 clean CI를 연결했다.
- [ ] reviewer와 `issued_at`을 사람이 확정한다.
