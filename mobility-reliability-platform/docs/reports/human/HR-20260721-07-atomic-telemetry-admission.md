---
id: HR-20260721-07
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Atomic Firestore Telemetry Admission
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: 권한 검증과 receipt reservation 원자 경계

## 한눈에 보기

- 이번 회차의 사전 목적: GPS 원본 저장 전에 현재 권한을 다시 확인하고 두 고유 index와 receipt를 하나의 Firestore transaction에서 예약하도록 ingest 경계를 통합한다.
- 보고 기준일의 실제 상태: `AdmissionStore` 경계와 `FirestoreAdmissionStore` production adapter 코드를 구현했고 local fake transaction seam에서 callback retry·철회·replay·conflict·손상된 linkage·finalizer 상태를 race test로 검증했다.
- 가장 중요한 차이 또는 위험: actual Firestore Emulator·ADC/IAM·동시 transaction integration은 미검증이며 adapter를 executable에 연결하지 않았다. Cloud Storage adapter와 recovery lease·sweeper도 없다.
- 사람에게 필요한 결정·확인: 이번 단계에서는 readiness를 열지 않고, 다음 gate에서 Storage object/manifest 계보와 pending reservation 복구 정책을 먼저 확정한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 Trusted Telemetry Platform 기술 gate이며 실제 성과와 분리한다.

- 로드맵상 위치: M3 / Firebase authorization 이후 원자적 telemetry admission
- 계획한 기술 주제: 현재 authorization snapshot과 idempotency index 두 개, server receipt의 단일 transaction 경계
- 예상 산출물: atomic admission interface, Firestore transaction adapter, replay/conflict state machine, receipt lineage·retention, ADR와 local 검증 증거
- 검토할 질문: 철회가 reservation보다 먼저 반영되는가, callback retry가 batch ID나 Storage side effect를 중복 생성하지 않는가, partial index와 손상 receipt를 fail-closed로 처리하는가
- 계획 완료 조건: actual Firestore transaction과 Cloud Storage generation precondition을 포함한 integration 검증 후 verifier·admission·Storage를 executable에 연결하고 readiness gate를 통과

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Ingest admission 경계 | `검증됨` | 별도 authorizer와 receipt reservation을 `AuthorizeAndReserve` 하나로 통합 | production runtime에는 미연결 | WSL2 Docker Go / synthetic |
| Firestore production adapter 코드 | `진행 중` | authorization exact read, 두 index read와 신규 3-way create, replay/conflict/corruption 분류를 구현 | actual Firestore transaction 의미론은 미검증 | local fake transaction seam |
| Transaction retry·철회 | `검증됨` | callback retry마다 result를 초기화하고 authorization snapshot을 재평가하며, retry에서 동의 철회 시 create/update 없이 거절 | 실제 concurrent transaction·철회 integration은 후속 | local race test / synthetic |
| Receipt lineage·retention | `검증됨` | 기기·trip·installation·consent·schema lineage, revision과 30일 `expires_at`을 reservation에 포함 | Storage hash·generation·manifest는 미구현 | local unit/race test |
| Finalizer | `검증됨` | linkage를 재확인하고 `stored` 또는 `object_conflict` rejection에서 revision과 새 `updated_at`을 기록 | 실제 Cloud Storage 성공 결과와 연결되지 않음 | local fake transaction seam |
| Actual Firebase integration | `미착수` | Emulator·ADC/IAM·동시 create와 실제 document decode 증거 없음 | 계획 완료 조건 미충족 | 미검증 |
| Storage와 복구 | `미착수` | production Storage adapter, generation precondition, manifest, lease·sweeper 없음 | 다음 gate로 이월 | 미검증 |
| Runtime 연결 | `미착수` | `cmd/server`에 verifier·admission·Storage를 주입하지 않음 | 의도적으로 fail-closed 유지 | 기존 local container 경계 |

### 실제 결과 상세

- 처리 순서를 `validation -> stable body hash/server batch ID -> authorization+3-way Firestore transaction -> commit 후 Storage -> receipt finalizer`로 고정했다.
- authorization은 replay/conflict index보다 먼저 평가한다. 철회되거나 관계가 맞지 않는 호출자에게 기존 receipt 존재 여부를 노출하지 않는다.
- 신규 reservation은 tenant-scoped `ingestIdempotency`, `ingestClientBatches`, `ingestReceipts` 세 문서를 같은 callback에서 create하도록 구현했다.
- 같은 body의 replay, idempotency body conflict, client-batch conflict, partial index, missing receipt, linkage mismatch와 unknown receipt state를 서로 구분하되 손상·provider 오류는 generic unavailable로 닫는다.
- local fake seam은 callback 재실행과 철회 상태를 결정론적으로 재현한다. 이는 실제 Firestore transaction 직렬화, 네트워크 retry, IAM 또는 동시 client 동작의 증거가 아니다.
- 데이터 유형: `synthetic | test`; 실제 GPS, Firebase UID/App ID, 복지관·사용자 데이터 없음
- 현재 executable은 새 adapter를 사용하지 않으므로 `/healthz=200`, `/readyz=503`, ingest `503 adapters_unconfigured`인 fail-closed 경계를 유지해야 한다. 새 adapter가 포함된 최종 container smoke 결과는 EVD-014에 기록하기 전까지 별도 확인 대상이다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| atomic admission interface와 Firestore adapter 코드가 존재하고 local fake seam race test·clean CI를 통과 | [EVD-20260721-014](../../evidence/2026-07.md#evd-20260721-014--원자적-telemetry-admission과-receipt-lineage) | `verified` — local contract와 clean CI 범위 | Codex / 2026-07-21 |
| transaction retry에서 authorization을 재평가하고 철회 시 write를 중단 | [EVD-20260721-014](../../evidence/2026-07.md#evd-20260721-014--원자적-telemetry-admission과-receipt-lineage) | `verified` — fake retry seam 범위 | local fake seam / 2026-07-21 |
| 두 key, 세 경로, replay/conflict와 receipt lineage가 고정됨 | [ADR-0015](../../decisions/ADR-0015-atomic-telemetry-admission.md) | `accepted` decision; runtime 증거 아님 | 문서 검토 필요 |
| actual Firestore atomic concurrency와 production admission이 활성화됨 | 확인 필요 — 현재 활성화·검증하지 않음 | `미검증` | 해당 없음 |
| Cloud Storage generation·manifest와 reserved receipt 복구가 동작함 | 확인 필요 — 현재 구현하지 않음 | `미검증` | 해당 없음 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0015](../../decisions/ADR-0015-atomic-telemetry-admission.md)
- 실제 제품 업데이트: 해당 없음 — runtime·사용자·운영 동작에는 연결하지 않음
- 인시던트: 해당 없음 — production·field 배포와 사용자 영향 없음
- 열린 위험: Firestore/Storage partial failure와 reserved receipt 복구, pending replay의 중복 Storage 시도, actual Firestore transaction·ADC/IAM 미검증, startup wiring 미완료. [RSK-10](../../plans/RISK_REGISTER.md)은 계속 active다.

## 다음 회차

- 8개월 계획상 다음 주제: Cloud Storage `DoesNotExist` adapter, object SHA-256·generation, immutable manifest와 receipt 계보
- 실제 상태를 반영한 다음 검증: actual Firestore Emulator concurrent same-batch·철회 경쟁 test, Storage 성공 후 finalizer 실패·재시도, pending reservation lease와 sweeper recovery
- 필요한 사람의 결정·지원: 원본 위치 lifecycle의 30일 기본값과 최대 90일 예외 승인 절차, staging Firebase project·App Check·service account 일정

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] actual Firestore·Storage·runtime 미검증을 완료로 표현하지 않았다.
- [x] 합성·테스트와 field 데이터를 구분했다.
- [x] 제품 업데이트와 인시던트를 생성하지 않았다.
- [x] 참석자·사진·지출을 생성하거나 추정하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] EVD-20260721-014의 실제 명령·commit·CI 결과를 확인했다.
- [ ] reviewer와 발행일을 사람이 확정했다.
