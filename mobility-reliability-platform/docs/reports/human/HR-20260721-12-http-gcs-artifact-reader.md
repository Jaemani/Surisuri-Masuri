---
id: HR-20260721-12
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: HTTP GCS Artifact Inventory Reader
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: HTTP GCS artifact inventory reader

## 한눈에 보기

- 이번 회차의 사전 목적: ADR-0018의 provider-neutral read port를 HTTP Cloud Storage adapter로 구현하고, exact path의 모든 일반·soft-deleted generation을 bounded inventory로 관찰한 뒤 exact generation만 읽는 경계를 만든다.
- 보고 기준일의 실제 상태: 현재 worktree에서 HTTP-only factory, 분리된 `Versions`/`SoftDeleted` query, exact structural validation, generation+metageneration precondition, bounded raw/manifest read와 typed provider error가 구현됐다. WSL2 Docker의 local synthetic 전체 Go gate는 통과했다.
- 가장 중요한 차이 또는 위험: evidence와 구현 commit은 pending이다. fake backend가 query·error 계약을 검증했지만 official Storage testbench, staging bucket과 Cloud Run runtime에서는 확인하지 않았다.
- 사람에게 필요한 결정·확인: 구현 diff와 EVD-020을 검토하고 commit·clean CI 이후 official HTTP Storage integration 증분으로 진행할지 확정한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한 계획이다.

- 계획한 기술 주제: version-aware exact-path inventory, soft-delete coverage, exact generation inspect/read, compressed raw byte 보존과 provider error normalization.
- 검토할 질문:
  - claim과 별개인 artifact read adapter가 임의 transport·bucket handle을 받아 권한 경계를 우회하지 않는가.
  - prefix sibling, 복수 generation, soft-deleted generation과 provider 장애를 artifact 없음으로 오분류하지 않는가.
  - read 중 generation 또는 metadata drift를 missing과 구분할 수 있는가.
- 이 증분의 완료 조건: local synthetic test에서 query·bound·precondition·error 계약이 재현되고 전체 Go gate가 통과한다.
- 전체 R5 완료 조건: current authorizer, exact manifest/raw validator, final classification precedence와 official Storage integration까지 완료해야 한다.

## 2. 실제 상태

| 항목 | 상태 | 확인된 결과 | 아직 아닌 것 | 검증 환경 |
| --- | --- | --- | --- | --- |
| HTTP factory | `local 구현` | default HTTP Storage client와 read-only scope를 adapter가 직접 소유 | 실제 ADC·IAM·client lifecycle | WSL2 Docker / synthetic |
| version inventory | `local 구현` | 일반 `Versions:true`와 `SoftDeleted:true` query를 분리하고 exact `Prefix`·`MatchGlob` 적용 | official provider pagination·soft-delete semantics | WSL2 Docker / fake iterator |
| structural fail-closed | `local 통과` | prefix sibling, 잘못된 attrs·soft marker·duplicate generation을 incomplete/unverifiable로 거부 | 실제 provider 손상 응답 | WSL2 Docker / synthetic |
| bounded observation | `local 통과` | inventory `limit+1`, read `maxBytes+1`, overflow 시 truncated 또는 typed limit error | 대형 실제 object·pagination 비용 | WSL2 Docker / synthetic |
| exact read | `local 구현` | generation과 metageneration 조건, manifest uncompressed/raw compressed flag 분리 | official HTTP compressed byte parity | WSL2 Docker / fake backend |
| error boundary | `local 통과` | direct 404와 list 404 분리, permission/quota/timeout/cancel/unavailable/precondition typed mapping | 실제 provider별 응답 matrix | WSL2 Docker / synthetic |
| 전체 Go gate | `local 통과` | gofmt, module tidy/verify, vet, race test, server build exit 0 | commit·clean CI | WSL2 Docker / Go 1.26.5 |
| classifier/runtime | `미구현·미연결` | reader는 provider-neutral port만 구현 | grant authorizer, validator, classification, worker wiring | 해당 없음 |

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 환경·확인 |
| --- | --- | --- | --- |
| exact version inventory와 bounded reader adapter가 현재 worktree에 존재함 | [EVD-20260721-020](../../evidence/2026-07.md#evd-20260721-020--http-only-gcs-generation-inventory-reader) | `generated` | WSL2 Docker local synthetic / commit pending |
| request/grant와 strict manifest decoder 선행 계약 | [EVD-20260721-019](../../evidence/2026-07.md#evd-20260721-019--read-only-artifact-classification-계약과-strict-manifest-decoder) | `verified` | local + clean CI |
| reader가 따라야 할 권한·transport·분류 경계 | [ADR-0018](../../decisions/ADR-0018-generation-pinned-read-only-classifier.md) | `accepted` | 설계 결정 |

## 4. 이번 증분의 기술 경계

```text
ArtifactInventoryReader
  ├─ HTTP-only read-scope factory
  ├─ exact path Versions inventory (bounded limit+1)
  ├─ exact path SoftDeleted inventory (bounded limit+1)
  ├─ generation inspect
  ├─ generation+metageneration manifest read
  └─ generation+metageneration compressed raw read (maxBytes+1)

이 증분에 없음
  ├─ current authorization grant 발급
  ├─ manifest/raw cross-lineage validation
  ├─ final classification
  ├─ receipt/attempt mutation
  └─ reconciler·cleanup·sweeper runtime
```

## 5. 중요한 결정과 위험

- 결정 유지: latest object를 읽지 않고 version inventory에서 exact generation을 고정한다.
- 결정 유지: list 404는 missing 근거가 아니며 direct exact-generation 404만 `generation_not_found` 후보로 사용한다.
- 결정 유지: raw read는 HTTP transport의 compressed-byte flag가 필요하므로 arbitrary client/bucket injection을 production constructor에 열지 않는다.
- 열린 위험: `SoftDeleted:true`와 version query 조합, HTTP metageneration precondition과 raw compressed byte는 official testbench에서 별도 확인해야 한다.
- 열린 위험: reader capability 자체는 authorization이 아니다. trusted authorizer와 classifier composition 전에는 scheduler나 worker에 주입할 수 없다.
- 열린 위험: EVD-020은 현재 commit이 없는 generated evidence이므로 외부 발행이나 완료 주장에 사용할 수 없다.

## 6. 다음 작업

1. 사람 검토 후 구현·문서 commit을 고정하고 clean CI를 확인한다.
2. pinned official Storage testbench에서 일반/noncurrent/soft-deleted generation inventory와 exact read precondition을 검증한다.
3. manifest-first cross-lineage validator와 raw compressed digest·bounded decompression·strict payload registry를 구현한다.
4. current system authorization grant와 classifier composition을 구현하되 runtime readiness는 계속 닫아 둔다.

## 7. 관련 기록

- 결정: [ADR-0018](../../decisions/ADR-0018-generation-pinned-read-only-classifier.md)
- 증거: [EVD-20260721-020](../../evidence/2026-07.md#evd-20260721-020--http-only-gcs-generation-inventory-reader)
- 선행 리포트: [HR-20260721-11](./HR-20260721-11-read-only-artifact-classifier-contract.md)
- 제품 업데이트: 해당 없음 — 사용자·운영·runtime 변화가 아님
- 인시던트: 해당 없음 — production·staging·field 영향 없음

## 8. 발행 전 확인

- [x] plan과 local synthetic 실제 결과를 분리했다.
- [x] official testbench·staging·runtime 미검증을 명시했다.
- [x] 참석자·사진·지출·사용자 수를 생성하지 않았다.
- [x] Product Update 또는 Incident를 만들지 않았다.
- [ ] 구현 commit과 clean CI를 연결한다.
- [ ] reviewer와 `issued_at`을 사람이 확정한다.
