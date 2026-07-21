---
id: HR-20260721-05
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Firebase Dual-token Principal Verifier
author: Codex draft
reviewer: human review pending
audience: project owner
---

# 요청 기술 리포트: Firebase dual-token verifier 기반

## 한눈에 보기

- 이번 회차의 사전 목적: Firebase ID token과 App Check를 raw GPS body 밖에서 검증하고 provider-neutral principal로 변환하는 adapter 기반을 만든다.
- 보고 기준일의 실제 상태: strict header parser, Firebase Admin SDK wrapper, App ID allowlist, 오류 sanitization, production emulator guard factory가 local synthetic test를 통과했다.
- 가장 중요한 차이 또는 위험: verifier와 startup guard는 아직 `cmd/server`에 주입되지 않았고 membership·trip·device·consent authorizer도 없어 ingest는 계속 `503`이다.
- 사람에게 필요한 확인: 실제 Firebase staging 프로젝트와 등록 Android/iOS App Check debug provider를 사용한 E2E는 별도 gate로 남긴다.

## 1. 계획

> 이 섹션은 8개월 로드맵에 따른 계획이며 실제 성과가 아니다.

- 로드맵상 위치: 7월 Trusted Telemetry Platform의 principal verification 단계
- 계획한 기술 주제: dual token, strict header, app allowlist, safe errors, ADC production factory, emulator fail-closed
- 예상 산출물: Firebase adapter package, unit/HTTP contract test, Docker/CI 검증, ADR·Evidence
- 검토할 질문: malformed token이 SDK 전에 차단되는가, UID/App ID만 남는가, 401/403/503가 정보 누출 없이 구분되는가, production factory가 emulator 설정을 거부하는가

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 아직 아닌 것 | 검증 환경 |
| --- | --- | --- | --- | --- |
| header/parser | `local 검증` | 단일 Bearer/App Check, 중복·결합·제어문자·16KiB 상한 | 실제 proxy/header chain | Docker Go synthetic |
| SDK wrapper | `local 검증` | UID/App ID mapping, nil fail-closed, provider 오류 sanitization | 실제 ADC/JWKS/token | fake SDK seam |
| 오류 계약 | `local 검증` | HTTP 401/403/503와 generic body | deployed Cloud Run | httptest |
| production guard | `함수 검증` | emulator env가 SDK 생성 전에 실패 | server startup 미연결 | unit test |
| container | `local 검증` | no-cache image, health 200, ready/ingest 503 | registry/Cloud Run 배포 | WSL2 Docker |

### 실제 결과 상세

- Firebase Admin Go SDK `v4.21.0`을 고정하고 `go.sum`과 readonly module 검사를 추가했다.
- `.dockerignore`를 개별 Go 파일 목록에서 package 단위 allowlist로 바꿔 새 adapter 파일이 image에서 누락되는 경로를 줄였다.
- CI 계획에 `go mod tidy -diff`, `go mod verify`, readonly race test, Docker build와 fail-closed smoke를 추가했다. 이 변경의 clean-runner 결과는 push 후 별도 확인해야 한다.
- WSL local에서 Firebase Emulator를 사용하는 `pnpm check`와 `pnpm test`를 병렬 실행해 port conflict가 발생했으며, orphan process를 식별·종료한 뒤 CI와 같은 순차 실행에서 전체 test가 통과했다. 제품·사용자 데이터 영향은 없었다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| verifier·SDK wrapper·Docker·순차 회귀검사 | [EVD-20260721-011](../../evidence/2026-07.md#evd-20260721-011--firebase-dual-token-verifier와-production-factory-검증) | `generated` — clean CI·실제 Firebase 전 | Codex / 2026-07-21 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0012](../../decisions/ADR-0012-firebase-dual-token-verifier-policy.md)
- 실제 제품 업데이트: 해당 없음 — executable wiring과 authorizer가 없어 사용자·운영자가 사용할 변화가 아님
- 인시던트: 해당 없음 — local test process 충돌이며 사용자·production 영향과 SEV 조건이 없음
- 열린 위험: App Check SDK의 provider 오류 구분 한계, ID token 잔여 수명, startup wiring 누락, 실제 app registration/E2E 미검증

## 다음 회차

- Firestore membership, installation, device assignment, server trip, consent revision authorizer를 구현한다.
- inactive membership이 유효한 ID token보다 우선해 거부되는 integration test를 만든다.
- receipt transaction과 Cloud Storage `DoesNotExist` adapter를 연결하기 전까지 readiness를 열지 않는다.

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
- [x] fake SDK seam과 실제 Firebase E2E를 구분했다.
- [x] production guard 함수와 runtime wiring을 구분했다.
- [x] 제품 업데이트·인시던트 발행 조건을 충족하지 않음을 표시했다.
- [x] local emulator 충돌과 순차 재검증을 함께 기록했다.
- [ ] push 후 clean CI 결과를 Evidence에 추가한다.
- [ ] 사람 검토 후 발행 상태와 발행일을 확정한다.
