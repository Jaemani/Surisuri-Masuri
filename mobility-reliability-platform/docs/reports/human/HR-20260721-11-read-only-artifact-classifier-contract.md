---
id: HR-20260721-11
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Read-only Artifact Classification Contract
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: Read-only artifact classifier 계약

## 한눈에 보기

- 이번 회차의 사전 목적: recovery claim을 artifact read 권한과 분리하고, exact receipt/fence에 결합된 읽기 권한과 generation-pinned 분류기의 입력·출력 계약을 먼저 닫는다.
- 보고 기준일의 실제 상태: provider-neutral request/result/inventory 계약, purpose별 opaque grant와 strict manifest decoder가 local synthetic test·race·vet·전체 Go build gate를 통과했다.
- 가장 중요한 차이 또는 위험: GCS reader와 최종 classifier orchestration은 아직 없다. 이 코드는 object 존재·무결성·복구 가능 여부를 실제로 분류하지 않으며 runtime에도 연결되지 않았다.
- 사람에게 필요한 결정·확인: 구현 commit의 clean CI를 확인한 뒤 HTTP-only GCS inventory/read adapter 증분으로 진행할지 검토한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한다.

- 로드맵상 위치: M3 / R5 generation-pinned read-only classifier
- 계획한 기술 주제: purpose·state별 request shape, exact fence/lineage binding, opaque authorization grant, provider-neutral inventory/result, strict manifest parser
- 예상 산출물: ADR-0018과 일치하는 Go 계약·table test, generated local evidence
- 검토할 질문: invalid authorization 뒤 provider call이 0인가, forward와 accepted audit가 섞이지 않는가, security-relevant field mutation이 binding을 바꾸는가, parser가 ambiguous JSON과 unbounded input을 닫는가
- 전체 계획 완료 조건: GCS version inventory·exact read, strict raw/manifest cross-validation, classification matrix와 official testbench까지 통과해야 하며 이번 회차는 그 완료를 의미하지 않는다.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| classification contract | `local 구현` | 열 classification, reason, retention, coverage와 inventory/reader/result type 정의 | 실제 분류 service 없음 | Go unit/race |
| request shape | `local 구현` | forward reserved+fence와 accepted stored/queued/projected+lineage를 분리 | deleting/deleted/rejected/cleanup은 의도적으로 제외 | Go table test |
| authorization grant | `local 구현` | issuer/purpose·policy·time·request binding과 exact grant/fence expiry 검증 | production authorizer 없음 | Go unit/race |
| request binding | `local 구현` | identity·receipt·revision·lineage·path·time·optional fence 전체를 canonical SHA-256에 결합 | cross-process token 아님 | Go mutation test |
| manifest decoder | `local 구현` | 64KiB bound, UTF-8, duplicate/unknown/trailing JSON, version·content·timestamp shape 검사 | receipt/canonical comparison은 후속 | Go table test |
| provider read | `미착수` | reader interface만 존재 | GCS call 0 | 미검증 |
| runtime·staging | `미착수` | startup·worker에 연결하지 않음 | readiness와 ingest는 계속 `503` | 미검증 |

### 실제 결과 상세

- recovery claim은 작업 소유권일 뿐 Storage 접근 권한이 아니다. forward request에는 receipt revision과 current fence 전체가 포함되고 grant가 정확히 그 request에 결합된다.
- accepted integrity audit는 forward fence를 받지 않고 receipt가 고정한 raw·manifest generation lineage를 모두 요구한다. deleting/deleted/rejected와 cleanup 상태는 이 목적의 입력으로 허용하지 않는다.
- grant와 request는 reader boundary 전에 검증한다. zero/변조/만료 grant, 다른 purpose issuer, 변경된 revision·fence와 trusted clock이 authorization check 이전인 경우를 닫는다.
- manifest parser는 JSON tokenizer 단계에서 escaped duplicate key도 거부하며, typed decode에서는 unknown field를 거부한다. parser 오류는 입력 bytes를 노출하지 않는다.
- 데이터 유형: `synthetic | test`; 실제 GCS object, GPS, UID/App ID, 이용자·복지관 데이터 없음

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| R5 authorization·inventory·classification 경계가 결정됨 | [ADR-0018](../../decisions/ADR-0018-generation-pinned-read-only-classifier.md) | `accepted` decision; 구현 증거 아님 | 문서 검토 필요 |
| request/grant 계약과 strict manifest decoder가 local gate를 통과함 | [EVD-20260721-019](../../evidence/2026-07.md) | `generated` — clean CI 전 | 사람 검토 필요 |
| 실제 GCS artifact가 정확히 분류됨 | 확인 필요 — 현재 구현하지 않음 | `미검증` | 해당 없음 |
| recovery worker가 활성화됨 | 확인 필요 — 현재 활성화하지 않음 | `미검증` | 해당 없음 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0018](../../decisions/ADR-0018-generation-pinned-read-only-classifier.md), [ADR-0017](../../decisions/ADR-0017-fenced-ingest-recovery.md)
- 실행계획: [Telemetry Recovery Plan](../../plans/TELEMETRY_RECOVERY_PLAN.md)
- 실제 제품 업데이트: 해당 없음 — runtime·사용자·운영 경로에는 연결하지 않음
- 인시던트: 해당 없음 — production·staging·field 배포와 사용자 영향 없음
- 열린 위험: current system recovery authorizer 부재, GCS HTTP transport·inventory coverage 미구현, raw/manifest cross-lineage·codec registry·classification precedence 미구현, staging IAM·lifecycle 미검증

## 명시적 미구현 범위

- HTTP-only GCS artifact reader factory
- live/noncurrent `Versions:true`와 soft-deleted `SoftDeleted:true` exact-path inventory
- prefix sibling 제외, candidate/truncation/coverage 판정과 narrow NotFound/provider error mapping
- exact generation manifest/raw bounded read, strict raw decode와 validator/codec registry
- classification orchestration, forward reconciler, attempt completion/failure와 bounded sweeper
- object create/delete, receipt/index mutation, cleanup·purge와 startup wiring

## 다음 회차

- 8개월 계획상 다음 주제: GCS generation inventory와 exact compressed-byte reader
- 실제 상태를 반영한 다음 검증: HTTP transport 고정, exact path/version/soft-delete query 분리, max+1 bound, sibling 제외, direct NotFound와 provider failure 분리
- 필요한 사람의 결정·지원: local EVD·clean CI 검토, 향후 staging bucket IAM·lifecycle 검증 일정

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계약·parser 구현과 실제 provider 분류를 분리했다.
- [x] local synthetic 결과와 staging/production을 구분했다.
- [x] runtime 미연결 변경을 제품 업데이트로 기록하지 않았다.
- [x] local test를 사용자 영향 인시던트로 기록하지 않았다.
- [x] 참석자·사진·지출을 생성하거나 추정하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [ ] 구현 commit의 clean CI를 확인했다.
- [ ] reviewer와 발행일을 사람이 확정했다.
