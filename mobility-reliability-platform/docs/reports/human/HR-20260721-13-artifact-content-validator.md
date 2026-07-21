---
id: HR-20260721-13
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Artifact Content Lineage Validator
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: telemetry artifact content lineage validator

## 한눈에 보기

- 이번 회차의 사전 목적: Storage에서 generation-pinned bytes를 읽은 뒤 manifest와 raw가 receipt의 immutable expectation에 속하는지 side effect 없이 판별하는 strict content boundary를 만든다.
- 보고 기준일의 실제 상태: manifest/raw 독립 validator, canonical manifest rebuild, compressed raw digest, 2MiB bounded single-stream gzip, strict telemetry payload, validator·codec registry와 reason precedence가 `main`에 구현됐다. WSL2 Docker 전체 Go gate, 독립 리뷰·재리뷰와 GitHub clean CI를 통과했다.
- 가장 중요한 차이 또는 위험: content bytes는 검증할 수 있지만 GCS inventory/read와 결합한 final classifier, current authorization, pre/post read drift와 열 classification matrix는 아직 없다.
- 사람에게 필요한 결정·확인: EVD-022의 pure validation 범위를 확인하고 read-only classifier orchestration 증분으로 진행할지 확정한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한 계획이다.

- 계획한 기술 주제: manifest-first cross-lineage, exact compressed digest, bounded decompression, strict payload, version-pinned validator·codec registry.
- 검토할 질문:
  - manifest가 receipt와 다른 사용자·기기·동의·raw generation을 가리킬 때 구분되는가.
  - gzip bomb, concatenated stream, trailing data와 noncanonical manifest를 조용히 수용하지 않는가.
  - 과거 validator 또는 compressor profile을 현재 구현으로 추정 대체하지 않는가.
- 이 증분의 완료 조건: pure synthetic matrix, registry golden self-test, reason precedence와 전체 Go gate가 통과한다.
- 전체 R5 완료 조건: current authorizer, GCS inventory orchestration, pre/post attrs drift, 열 classification matrix, official integration과 privacy scan까지 완료해야 한다.

## 2. 실제 상태

| 항목 | 상태 | 확인된 결과 | 아직 아닌 것 | 검증 환경 |
| --- | --- | --- | --- | --- |
| manifest validation | `local·clean CI 통과` | strict decode, receipt identity, accepted lineage, exact canonical bytes 분리 | actual GCS bytes와 end-to-end composition | WSL2 Docker / synthetic |
| raw integrity | `local·clean CI 통과` | compressed SHA-256·CRC32C·size, decompressed body hash 분리 | provider pre/post attrs drift | WSL2 Docker / synthetic |
| gzip boundary | `local·clean CI 통과` | corrupt·trailing·multistream·2MiB+1 overflow 거부 | adversarial fuzz corpus | WSL2 Docker / synthetic |
| strict payload | `local·clean CI 통과` | telemetry v2 decode·validate와 receipt identity·count·time bounds 비교 | 실제 사용자 GPS payload | WSL2 Docker / synthetic |
| validator registry | `local·clean CI 통과` | explicit version, duplicate fail-closed, profile max, literal codec golden | prior validator compatibility image | WSL2 Docker / synthetic |
| reason precedence | `local·clean CI 통과` | unavailable→metadata→manifest→raw 합성 및 동시 손상 재현 | final ten-class orchestration | WSL2 Docker / synthetic |
| 독립 리뷰 | `완료` | High 없음, Medium 3건 수정 후 재리뷰 잔여 High/Medium 없음 | staging security review | read-only code review + race test |
| runtime | `미연결` | pure validator는 side effect 없음 | authorizer, classifier, worker, scheduler | 해당 없음 |

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 환경·확인 |
| --- | --- | --- | --- |
| content validator·registry가 full local gate·independent rereview·clean CI를 통과함 | [EVD-20260721-022](../../evidence/2026-07.md#evd-20260721-022--telemetry-artifact-content-lineage-validator) | `verified` | WSL2 Docker synthetic + GitHub runner |
| HTTP exact-generation bytes·precondition 경계 | [EVD-20260721-021](../../evidence/2026-07.md#evd-20260721-021--http-gcs-exact-generation-reader-official-testbench) | `verified` | pinned official Storage testbench + clean CI |
| request/grant·classification shape와 strict manifest decoder 선행 계약 | [EVD-20260721-019](../../evidence/2026-07.md#evd-20260721-019--read-only-artifact-classification-계약과-strict-manifest-decoder) | `verified` | WSL2 Docker synthetic + clean CI |

## 4. 이번 증분의 기술 경계

```text
TelemetryArtifactContentValidator
  ├─ ValidateManifest(request, snapshot, bytes)
  │    └─ bounded referenced raw lineage
  ├─ ValidateRaw(request, snapshot, compressed bytes)
  │    ├─ exact compressed integrity
  │    ├─ bounded single-stream gzip
  │    └─ strict telemetry v2 lineage
  └─ explicit validator + codec registry

이 증분에 없음
  ├─ current authorization grant 발급
  ├─ GCS inventory·inspect·read orchestration
  ├─ final ten-class classification
  ├─ receipt·attempt mutation
  └─ reconciler·cleanup·sweeper runtime
```

## 5. 중요한 결정과 위험

- 결정 유지: exact compressed digest와 decompressed body hash는 서로 다른 계보로 검증한다.
- 결정 유지: raw body가 valid해도 compressor golden과 다르면 content conflict로 추정하지 않고 codec unavailable로 hold할 수 있게 한다.
- 결정 유지: manifest-only와 raw-only를 판별할 수 있도록 두 validator를 독립 호출 가능하게 하고, manifest 성공 결과는 source bytes 없이 bounded raw reference만 반환한다.
- 결정 유지: duplicate validator version은 등록 순서와 무관하게 활성화하지 않는다.
- 열린 위험: final classifier가 provider error, incomplete inventory, multiple/soft-deleted generations와 content reason을 ADR precedence로 결합해야 한다.
- 열린 위험: 현재 privacy test는 content result shape에 한정된다. final result·metrics·log의 raw body·좌표·token·UID/App ID scan이 남아 있다.
- 열린 위험: synthetic test는 actual GCS IAM, staging lifecycle 또는 field telemetry 증거가 아니다.

## 6. 다음 작업

1. grant·fence expiry보다 provider context deadline이 늦지 않도록 clamp하는 read-only classifier service를 구현한다.
2. manifest와 raw exact-path inventory를 purpose별로 해석하고 unique generation만 pre-inspect/read/post-inspect한다.
3. provider error·incomplete coverage·generation drift·content reason을 열 classification matrix로 합성한다.
4. classifier result·metrics·log privacy scan과 official Storage composition test를 추가한다.
5. current system recovery/integrity authorizer 전에는 runtime readiness를 계속 닫아 둔다.

## 7. 관련 기록

- 결정: [ADR-0018](../../decisions/ADR-0018-generation-pinned-read-only-classifier.md)
- 증거: [EVD-20260721-022](../../evidence/2026-07.md#evd-20260721-022--telemetry-artifact-content-lineage-validator)
- 선행 리포트: [HR-20260721-12](./HR-20260721-12-http-gcs-artifact-reader.md)
- 제품 업데이트: 해당 없음 — 사용자·운영·runtime 변화가 아님
- 인시던트: 해당 없음 — production·staging·field 영향 없음

## 8. 발행 전 확인

- [x] plan과 synthetic 실제 결과를 분리했다.
- [x] full R5·staging·runtime 미완료를 명시했다.
- [x] independent review에서 수정한 Medium과 재리뷰 결과를 포함했다.
- [x] 참석자·사진·지출·사용자 수를 생성하지 않았다.
- [x] Product Update 또는 Incident를 만들지 않았다.
- [x] 구현 commit과 clean CI를 연결했다.
- [ ] reviewer와 `issued_at`을 사람이 확정한다.
