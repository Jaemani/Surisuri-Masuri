---
id: HR-20260721-03
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Fail-closed Telemetry Ingest Kernel
author: Codex draft
reviewer: human review pending
audience: project owner
---

# 기술 리포트: 서버 수집 경계 첫 kernel

## 한눈에 보기

- 이번 회차의 사전 목적: 모바일 batch를 받는 서버의 계약·권한·멱등성 경계를 외부 Firebase 자격증명과 분리해 검증한다.
- 보고 기준일의 실제 상태: Go wire validator, ingest service, HTTP boundary와 non-root container가 구현되고 test·race·vet·Docker smoke를 통과했다.
- 가장 중요한 차이 또는 위험: Firebase Auth/App Check·Firestore·Storage adapter가 없어 실제 ingest는 의도적으로 `503`이며 production 준비 상태가 아니다.
- 사람에게 필요한 결정·확인: 실제 Firebase project를 연결하기 전 IAM, App Check enforcement, receipt collection과 Storage lifecycle을 별도 승인해야 한다.

## 1. 계획

> 이 섹션은 8개월 로드맵에 따른 계획이며 실제 성과가 아니다.

- 로드맵상 위치: 7월 Trusted Telemetry Platform의 server boundary.
- 계획한 기술 주제: strict contract, verified principal과 batch scope, idempotency·batch 고유성, receipt·object 분리, safe errors, scale-to-zero container.
- 예상 산출물: Go service kernel, HTTP tests, Docker image, fail-closed smoke 결과.
- 검토할 질문: client tenant를 신뢰하지 않는가, replay와 conflict를 구분하는가, 좌표가 오류에 노출되지 않는가, adapter가 없을 때 닫히는가.
- 계획 완료 조건: synthetic test·race·vet·image build와 health/ready/ingest 상태 확인.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| wire contract | `검증됨` | duplicate key·invalid UTF-8 거부, 범위·최대 500·safe error test 통과 | schema 자동생성은 아님 | `Docker Go` |
| ingest kernel | `검증됨` | identity·scope authorizer·두 고유키·replay·terminal conflict·gzip·receipt recovery test 통과 | authorizer와 GCP adapter는 interface만 존재 | `Docker Go` |
| HTTP boundary | `검증됨` | 2MiB·status mapping·safe error test 통과 | Firebase verifier 없음 | `Docker Go` |
| container | `부분 검증` | non-root image build와 fail-closed smoke 통과 | Cloud Run 미배포 | `local Docker` |
| production adapter | `미착수` | readiness와 ingest가 503 | 다음 integration gate | 해당 없음 |

### 실제 결과 상세

- 결과: 인증 우회 없이 외부 adapter와 독립적으로 검증 가능한 server kernel이 생겼다.
- 관측 수치: 2026-07-21 기준 Go test/subtest 39건, race·vet 통과, local image 1개 build, smoke 3 endpoint 확인.
- 데이터 유형: synthetic repository fixture.
- 알려진 제한: Firebase/GCP·Cloud Run·mobile uploader와 실제 위치 batch는 검증하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Go test·race·vet·Docker build와 fail-closed smoke | [EVD-20260721-009](../../evidence/2026-07.md#evd-20260721-009--go-ingest-kernel과-fail-closed-container) | `generated` — adapter 검토 전 | Codex / 2026-07-21 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0009](../../decisions/ADR-0009-fail-closed-ingest-kernel.md)
- 실제 제품 업데이트: 해당 없음 — local-only kernel이며 운영 가능한 adapter가 없어 제품 업데이트 발행 조건을 충족하지 않음
- 인시던트: 해당 없음 — local synthetic 환경이며 사용자 영향 없음
- 열린 위험: token·App Check 검증, 실제 기기·세션·동의 조회, Firestore의 두 고유키 transaction, Storage precondition·lifecycle, Cloud Run IAM과 비용을 아직 검증하지 않았다.

## 다음 회차

- 8개월 계획상 다음 주제: Firebase/GCP telemetry adapter와 위치 privacy·비용 검증.
- 실제 상태를 반영한 다음 검증: Emulator 또는 별도 test project에서 receipt reservation과 Storage emulator/object adapter를 먼저 연결한다.
- 필요한 사람의 결정·지원: production project ID·service account를 저장소에 넣지 않는 secret/IAM 배포 경로의 승인.

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: `아니오`
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 사람 확인 필요

> 참석자, 사진, 지출 및 시각은 자동 생성하거나 추정하지 않았다.

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] adapter 미구현과 fail-closed 상태를 완료로 포장하지 않았다.
- [x] 수치에 측정일·모수·단위가 있다.
- [x] synthetic fixture와 실제 위치 데이터를 구분했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·EVD를 원문으로 링크했고, 제품 업데이트가 아직 없는 이유를 적었다.
- [ ] 사람 검토 후 발행 상태와 발행일을 확정한다.
