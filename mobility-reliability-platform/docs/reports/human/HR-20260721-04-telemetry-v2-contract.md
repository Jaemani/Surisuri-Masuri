---
id: HR-20260721-04
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Authenticated Telemetry v2 Contract
author: Codex draft
reviewer: human review pending
audience: project owner
---

# 기술 리포트: Firebase 연결 전 identity/reference 계약 정렬

## 한눈에 보기

- 이번 회차의 사전 목적: production Firebase adapter가 Firebase UID, domain actor UUID, local session과 server trip, 정책 version과 실제 동의를 혼동하지 않게 wire·Rules·gateway 경계를 정렬한다.
- 보고 기준일의 실제 상태: `telemetry-batch.v2`, server UUIDv7, derived idempotency key, canonical membership/Rules와 synthetic 회귀검사가 구현됐다.
- 가장 중요한 차이 또는 위험: 실제 Firebase token·App Check와 Firestore authorizer는 아직 연결되지 않아 executable ingest는 계속 `503`이다.
- 사람에게 필요한 확인: 운영 전 session-start command가 trip UUIDv7을 발급하고 실제 기기 배정·정밀위치 consent revision을 검증하도록 유지해야 한다.

## 1. 계획

> 이 섹션은 8개월 로드맵에 따른 계획이며 실제 성과가 아니다.

- 로드맵상 위치: 7월 Trusted Telemetry Platform의 identity·authorization contract gate.
- 계획한 기술 주제: Firebase principal 분리, pseudonymous wire, client/server ID lineage, exact consent revision, Firestore canonical path, server-only mutation.
- 예상 산출물: v2 schema/fixture, gateway v2 decoder, UUIDv7 generator, Rules와 Emulator test, ADR.
- 검토할 질문: raw GPS에 UID가 없는가, local ID를 domain ID로 오인하지 않는가, 실제 consent revision을 참조하는가, adapter 전 쓰기가 닫히는가.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 아직 아닌 것 | 검증 환경 |
| --- | --- | --- | --- | --- |
| wire v2 | `검증됨` | actor/UID 제거, trip·client session·installation·consent revision 분리 | mobile assembler 미구현 | `synthetic fixture` |
| server identity | `검증됨` | UUIDv7 version/variant/time·clock rollback test | 분산 환경 유일성 검증 아님 | `Docker Go` |
| idempotency | `검증됨` | cross-language SHA-256 vector, client batch·server batch 분리 | Firestore transaction 미구현 | `Docker Go` |
| Firestore 경계 | `검증됨` | memberships/roles/validity/tenant/server-only 경계 12건 | production Rules 배포 아님 | `Firebase Emulator` |
| runtime | `부분 검증` | v2 kernel build 후 container `200/503/503` | token·authorizer·storage adapter 없음 | `local Docker` |

### 실제 결과 상세

- v1 schema와 fixture는 compatibility 기록으로 남기고 production gateway 대상은 v2로 전환했다.
- Firestore field는 snake_case, JSON wire는 camelCase로 분리하고 adapter mapping을 명시했다.
- Go 43 test/subtest, Firebase Rules 14건, contract 6 case, 양 플랫폼 bundle과 Docker build가 통과했다.
- 실제 사용자, 실제 GPS, production Firebase/GCP 자격증명은 사용하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| v2 contract·UUIDv7·Rules·gateway 회귀검사 | [EVD-20260721-010](../../evidence/2026-07.md#evd-20260721-010--telemetry-v2-identityreference-계약-정렬) | `generated` — production adapter 전 | Codex / 2026-07-21 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0010](../../decisions/ADR-0010-authenticated-telemetry-references.md)
- 실제 제품 업데이트: 해당 없음 — production adapter와 mobile uploader가 없어 사용자·운영자가 사용할 변화가 아님
- 인시던트: 해당 없음 — synthetic local/emulator 개발이며 사용자 영향 없음
- 열린 위험: App Check는 Go emulator가 없고, Rules가 허용하는 tenant domain read는 역할·소유자 단위 최소권한을 production 전 더 세분화해야 한다.

## 다음 회차

- Firebase ID token과 App Check dual verifier를 fake seam과 실제 SDK adapter로 구현한다.
- authorizer는 membership, server trip, device assignment, installation과 consent revision을 Firestore 현재 상태에서 검증한다.
- receipt adapter는 derived-key index와 server batch receipt를 한 transaction으로 예약한다.

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: `아니오`
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 사람 확인 필요

> 참석자, 사진, 지출 및 시각은 자동 생성하거나 추정하지 않았다.

## 발행 전 검토

- [x] 계획과 실제를 분리했다.
- [x] v1 compatibility와 v2 production target을 구분했다.
- [x] synthetic/emulator와 production 검증을 구분했다.
- [x] 제품 업데이트 발행 조건을 충족하지 않음을 표시했다.
- [x] 참석자·사진·지출·현장 성과를 생성하지 않았다.
- [ ] 사람 검토 후 발행 상태와 발행일을 확정한다.
