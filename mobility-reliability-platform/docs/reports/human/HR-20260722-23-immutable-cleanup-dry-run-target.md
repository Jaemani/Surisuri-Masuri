---
id: HR-20260722-23
report_type: requested
status: draft
period_start: 2026-07-22
period_end: 2026-07-22
issued_at: 2026-07-22
roadmap_month: M3
technical_gate: R8b - sealed classification and immutable cleanup dry-run target
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Immutable cleanup dry-run target

## 한눈에 보기

- 이번 회차의 사전 목적: Cleanup lease를 삭제 권한으로 바로 사용하지 않고, read-only artifact 분류 결과와 exact generation을 한 attempt의 불변 dry-run target으로 고정한다.
- 보고 기준일의 실제 상태: Commit `b322661`에서 cleanup 전용 read capability, classifier evidence seal, create-once Firestore target과 동시성·conflict 검증을 구현했다. Local Go race와 Firebase demo Firestore Emulator 결과는 [EVD-20260722-032](../../evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target)에 기록한다.
- 가장 중요한 차이 또는 위험: Target은 future delete의 입력 근거일 뿐 삭제 권한이 아니다. GCS delete, receipt `expired`, cleanup attempt completion, scheduler와 staging 운영은 아직 없다.
- 사람에게 필요한 결정·확인: R8c actual delete는 staging bucket의 versioning·soft-delete·lifecycle·retention·IAM을 확인하고 exact-generation post-delete audit 계약을 별도 승인한 뒤 시작한다.

## 1. 계획

> 이 섹션은 8개월 계획의 기술 발전 축이다. 아래 항목은 실제 현장·운영 성과를 뜻하지 않는다.

- 로드맵상 위치: M3 telemetry control plane의 R8 expiry cleanup
- 계획한 기술 주제: purpose-limited artifact capability, generation-pinned read-only classification, sealed evidence, immutable create-once target, Firestore optimistic concurrency
- 예상 산출물: Cleanup authorizer·target domain contract, Firestore target adapter, Rules deny, tamper·replay·concurrent create unit/Emulator tests
- 검토할 질문: Cleanup lease와 artifact read가 분리되는가, 공개 result 변조가 target을 오염시키지 않는가, stale fence와 conflicting replay가 write 0인가
- 계획 완료 조건: Target까지 local·clean CI로 검증하고 GCS delete·`expired`·runtime은 별도 gate로 유지한다.

## 2. 실제

> 보고 기준일에 코드·테스트로 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Cleanup read authority | `검증됨` | `cleanup_pending`의 exact cleanup fence·attempt에만 최대 30초 read grant 발급 | Public Firestore read-authorizer Emulator 경로와 accepted/held cleanup 미구현 | local unit/race + stubbed current-state store |
| Classification evidence | `검증됨` | Request와 classification·inventory·pinned lineage·time 전체를 unexported seal에 binding | In-process capability만 검증 | local unit/race |
| Immutable target | `검증됨` | Attempt ID path에 exact generation/hash와 bounded inventory를 create-once 저장 | Delete outcome field 미구현 | local + Firestore Emulator |
| 경쟁·replay | `검증됨` | Concurrent same command가 target 1개, created 1/replayed 1로 수렴; conflict는 write 0 | Production contention 미검증 | Firebase demo Firestore Emulator |
| Client boundary | `검증됨` | `ingestCleanupTargets` direct client read/write를 명시적으로 거절 | Admin SDK/IAM staging 미검증 | Firebase Rules 24 tests |
| Runtime composition | `미착수` | Scheduler·startup·readiness·GCS delete port 변경 없음 | 의도적 차단 | fail-closed executable boundary |

### 실제 결과 상세

- Commit: `b322661` (`feat: pin immutable cleanup dry-run targets`)
- Purpose 분리: `cleanup_dry_run`은 forward와 accepted audit grant를 재사용할 수 없고 cleanup fence만 가진다.
- Current-state authorization: Linked indexes, receipt와 exact `started` cleanup attempt를 읽어 revision, owner/token/expiry, mode/origin/policy, transition·quiescence를 다시 확인한다.
- Read expiry: Artifact read grant는 30초 TTL과 cleanup lease expiry 중 이른 시각까지다. Reader boundary마다 request/grant/fence expiry를 다시 검사한다.
- Evidence seal: Classification, reason, retention phase, 두 inventory summary, raw/manifest digest·CRC·size·generation·metageneration, validator version과 observed time을 exact request binding과 함께 봉인한다.
- 리뷰 정정: 첫 구현은 request binding만 확인해 genuine result의 공개 field를 shape-valid generation으로 바꿀 수 있었다. 독립 리뷰에서 High finding으로 확인했고 generic evidence seal을 추가해 cleanup target과 기존 forward planner 모두 같은 검증을 사용하게 했다.
- Tamper regression: 64자리 다른 digest, generation, classification tuple과 observed time을 구조적으로 유효하게 바꿔도 target capability와 current-state read가 0임을 확인했다.
- Target decision: Empty는 `verified_empty`, valid raw/complete/manifest-only는 `delete_candidate`, conflict/drift는 `hold`, unavailable은 target 미생성으로 분리한다.
- Target provenance: Receipt·attempt·revision·fence·transition·lease, classification, inventory와 실제 pin이 있는 exact path·generation·hash를 canonical target hash에 묶는다.
- Target ID: Cleanup attempt ID를 deterministic document ID로 사용해 한 claim당 target 하나를 보장한다.
- Firestore transaction: Receipt linkage, exact started attempt와 existing target을 모두 읽은 뒤 current state를 재검증한다. Create 외 receipt·index·attempt update는 0이다.
- Concurrent Emulator: 두 creator는 target 1개와 created/replayed 각 1개로 수렴했다. Conflicting target은 기존 document와 receipt·attempt를 바꾸지 않았다.
- Privacy: Target/attempt/test result에는 좌표, raw body, Firebase UID/App ID, credential과 provider 원문 오류가 없다. Server-only exact path/hash는 사람 리포트에 값으로 복사하지 않았다.
- Data 유형: Synthetic receipt·attempt·artifact lineage와 Firebase demo project만 사용했다. 실제 GPS·사용자·복지관 데이터는 사용하지 않았다.
- 알려진 제한: GCS delete와 cleanup execution, cleanup renewal/release/completion, receipt `expired`, purge, staging IAM·lifecycle과 runtime wiring은 구현하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Sealed classification과 immutable target | [EVD-20260722-032](../../evidence/2026-07.md#evd-20260722-032--sealed-classification과-immutable-cleanup-dry-run-target) | `verified` — local/Emulator/clean CI | Codex + delegated independent code·documentation review / 2026-07-22 |
| Cleanup claim과 quiet-period 선행조건 | [EVD-20260722-031](../../evidence/2026-07.md#evd-20260722-031--immutable-quiescence와-fenced-cleanup-lease-claim) | `verified` — local/Emulator/testbench/clean CI | Codex / 2026-07-22 |

근거가 없는 staging·production·field 성과와 실제 사용자·기관 결과는 이 리포트에 포함하지 않았다.

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0024](../../decisions/ADR-0024-immutable-cleanup-dry-run-target.md), [ADR-0023](../../decisions/ADR-0023-fenced-cleanup-lease-claim.md)
- 실제 제품 업데이트: 해당 없음 — cleanup target adapter가 executable·사용자·운영 경로에 연결되지 않았고 Storage delete를 수행하지 않는다.
- 인시던트: 해당 없음 — evidence seal 결함은 uncommitted local review에서 발견·수정됐고 production·staging·field 영향이 없다.
- 열린 위험: R8c delete capability, current fence/provider generation 재검증, raw-first deletion, post-delete live generation audit, cleanup completion·purge와 staging IAM·lifecycle이 남아 있다.

## 다음 회차

- 8개월 계획상 다음 주제: R8c generation-pinned cleanup executor와 post-delete audit
- 실제 상태를 반영한 다음 검증: Persisted target만 믿지 않고 current receipt/fence와 provider exact generation을 다시 결합하며 raw 실패 뒤 manifest delete가 0인지 확인
- 필요한 사람의 결정·지원: Staging GCS versioning·soft-delete·lifecycle·retention·IAM과 삭제 복구 창을 승인하기 전 actual delete를 활성화하지 않는다.

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] 실제 주장마다 근거가 있거나 제한을 표시했다.
- [x] Synthetic·Emulator와 staging·production·field를 구분했다.
- [x] 실제 회의가 없음을 표시했고 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·EVD를 원문으로 링크했다.
- [x] GitHub clean CI 결과를 EVD-20260722-032에 최종 반영했다.
- [ ] 사람이 리포트 내용을 검토했다.
