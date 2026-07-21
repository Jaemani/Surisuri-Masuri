---
id: HR-20260721-06
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Firestore Batch Authorization Snapshot
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: GPS 저장 전 권한 관계 검증

## 한눈에 보기

- 이번 회차의 사전 목적: Firebase로 검증한 사용자·앱이 특정 기관·전동보장구·trip·정밀위치 동의에 실제 권한을 갖는지 원본 저장 전에 확인한다.
- 보고 기준일의 실제 상태: provider-neutral Go 정책과 Firestore exact-read adapter, 현재 동의 projection 계약, client Rules 차단을 local synthetic 환경에서 구현·단위 검증했다.
- 가장 중요한 차이 또는 위험: 읽기 검증과 receipt 예약이 아직 한 transaction이 아니므로 철회 경쟁 조건이 남고 production server에는 연결하지 않았다.
- 사람에게 필요한 결정·확인: guardian 대신 upload를 초기 파일럿에 포함할지, 종료 후 지연 upload 기본 72시간과 최대 허용기간을 확정해야 한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 기술 gate이며 실제 성과와 분리한다.

- 로드맵상 위치: 7월 Trusted Telemetry Platform / 인증된 텔레메트리 데이터면
- 계획한 기술 주제: active membership·installation·trip·assignment·consent의 server-side authorization
- 예상 산출물: 권한 allow/deny matrix, Firestore adapter, Rules 회귀 test, ADR와 도메인 계약
- 검토할 질문: 유효 token만으로 충분한가, 철회된 동의를 어떻게 현재 상태로 판단하는가, 좌표 없이 필요한 시간 검사가 가능한가
- 계획 완료 조건: 허용 관계만 통과하고 모든 거절·장애 case가 receipt/object write 전에 닫히며 production 미준비 상태를 readiness가 노출하지 않음

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| 권한 데이터 계약 | `검증됨` | installation·assignment·trip 만료·현재 동의 projection 계약을 ADR-0013으로 고정 | guardian upload는 보수적으로 제외 | local review |
| Pure authorization policy | `검증됨` | 본인 beneficiary와 모든 exact relation·상태·시각을 검사 | 좌표 없이 min/max capturedAt만 추가 | Docker Go / synthetic |
| Firestore reader | `검증됨` | bounded exact GetAll, pseudonymous consent-state path, generic error mapping | 실제 Emulator document decode는 후속 | Docker Go / synthetic |
| Firebase client boundary | `검증됨` | current consent state client read/write 거절 | Admin SDK/IAM은 후속 | Firebase Rules Emulator |
| Tenant client read matrix | `계획 변경·검증됨` | 본인 person 범위와 case worker/admin 운영 범위로 축소, 24-case 통과 | 기존 active-member 전체 read를 제거 | Firebase Rules Emulator |
| Runtime 연결 | `미착수` | `cmd/server`는 계속 fail-closed | 원자적 reservation 전 의도적 보류 | local container |

### 실제 결과 상세

- Firebase token의 UID/App ID와 request scope를 tenant membership, app installation, server trip, exact device assignment, immutable consent revision, current consent state에 연결했다.
- initial allow matrix는 `beneficiary`이며 `membership.person_id == trip.person_id`인 본인 upload만 허용한다.
- batch 좌표는 authorizer로 전달하지 않고 sample의 최소·최대 RFC3339 시각만 사용한다.
- Firestore read timeout은 0초 초과 10초 이하로 제한하고 missing과 dependency/malformed document를 서로 다른 내부 분류로 처리한다. HTTP 응답은 세부 path·UID·App ID를 노출하지 않는다.
- 관측 수치: Go 전체 package readonly race test, module verify, vet, Linux build 통과. mobile 65건·Firestore Rules 15건·contract fixture 6건 통과. Android·iOS static bundle export 통과. container는 health 200, readiness·ingest 503을 유지했다.
- 데이터 유형: `synthetic | test`; field 데이터와 실제 좌표 없음
- 알려진 제한: authorization과 receipt create 사이 TOCTOU, actual Firestore integration·ADC/IAM·startup wiring 미검증

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| 관계·시각·상태 allow/deny policy가 local test와 clean CI를 통과 | [EVD-20260721-012](../../evidence/2026-07.md#evd-20260721-012--firestore-텔레메트리-권한-snapshot) | `verified` | Codex / 2026-07-21 |
| Firestore exact path·DTO·오류 sanitization이 단위 검증됨 | [EVD-20260721-012](../../evidence/2026-07.md#evd-20260721-012--firestore-텔레메트리-권한-snapshot) | `verified` | delegated review / 2026-07-21 |
| current consent state의 client direct access가 차단됨 | [EVD-20260721-012](../../evidence/2026-07.md#evd-20260721-012--firestore-텔레메트리-권한-snapshot) | `verified` | local Rules test + CI / 2026-07-21 |
| 타인·운영 projection의 광범위 client read가 owner/staff matrix로 축소됨 | [EVD-20260721-013](../../evidence/2026-07.md#evd-20260721-013--firestore-client-최소권한-read-matrix) | `generated` | local Rules Emulator / 2026-07-21 |
| production에서 실제 권한 검증이 활성화됨 | 확인 필요 — 현재 활성화하지 않음 | `미검증` | 해당 없음 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0013](../../decisions/ADR-0013-telemetry-authorization-snapshot.md)
- 실제 제품 업데이트: 해당 없음 — runtime·사용자·운영 동작에는 아직 연결하지 않음
- 인시던트: 해당 없음 — production·field 배포와 사용자 영향 없음
- 열린 위험: authorization read와 receipt reservation 사이 철회 race, current-state transaction producer 미구현, 실제 Firebase integration 미검증. [RSK-27](../../plans/RISK_REGISTER.md)은 local Rules에서 축소했지만 staging Rules 배포·실앱 query 검증 전까지 active

## 다음 회차

- 8개월 계획상 다음 주제: Firestore 3-way idempotency/receipt reservation과 authorization 재검사를 하나의 read-write transaction으로 통합
- 실제 상태를 반영한 다음 검증: authorization 직후 membership·installation·consent를 철회하는 경쟁 test에서 receipt/object write 0건 확인
- 필요한 사람의 결정·지원: guardian upload 범위, offline upload 기간, 파일럿 Firebase project와 App Check 등록 일정

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] 실제 주장마다 근거가 있거나 `확인 필요`로 표시했다.
- [x] 수치에 기간·모수·단위가 있다.
- [x] 합성·테스트·현장 데이터를 구분했다.
- [x] 참석자·사진·지출을 생성하거나 추정하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·UPD·EVD를 원문으로 링크했다.
