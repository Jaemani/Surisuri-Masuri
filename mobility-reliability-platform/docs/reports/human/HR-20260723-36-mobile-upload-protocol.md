---
id: HR-20260723-36
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: mobile telemetry upload protocol partial
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: 모바일 telemetry 업로드 계약 첫 증분

## 한눈에 보기

- 이번 회차의 목적: 6월에 저장한 휴대폰 GPS를 7월 서버 업로드 경로로 연결하기 전에, 재시도해도 변하지 않는 batch 원문과 ACK 판정 규칙을 고정한다.
- 실제 상태: pure batch builder와 response classifier를 구현하고 local synthetic unit 83건 전체와 TypeScript 검사를 통과했다.
- 가장 중요한 제한: SQLite에 batch body를 영속화하거나 HTTP로 전송하지 않았다. 실제 앱→서버·지도 결과는 계속 미완료다.

## 8개월 로드맵 대비 현재 위치

| 월 | 계획한 결과 | 보고 시점 실제 상태 |
| --- | --- | --- |
| 5월 | 새 앱·데이터 구조, 휴대폰 GPS 수집·지도 | 앱 골격·데이터 계약·foreground 수집 코드 완료, 실제 경로 지도 증거 없음 |
| 6월 | Android/iPhone background GPS, SQLite offline 보존·재동기화 | foreground GPS·SQLite event log 구현. background·실기기·재연결 미완료 |
| 7월 | 앱→서버 업로드, Go gateway, 위치 정제·익명 히트맵 | 서버 수신 안전성 기반과 모바일 wire/ACK protocol 구현. SQLite durable batch, 실제 HTTP 업로드·정제 지도·히트맵 미완료 |
| 8월 | PyTorch·ONNX 주행 품질 모델 | 미착수 |
| 9월 | Digital Twin·부품 점검시점 예측 | 미착수 |
| 10월 | 근거 연결형 AI 리포트 | 미착수 |
| 11월 | 실증·접근성·운영 | 미착수 |
| 12월 | 통합 데모·발표 | 미착수 |

## 1. 계획

- 서버 contract와 일치하는 `telemetry-batch.v2` mobile DTO를 정의한다.
- GPS sample 외 lifecycle·reject event가 wire body에 섞이지 않는 경계를 둔다.
- 재시도 때 같은 `clientBatchId`와 exact body를 유지한다.
- 정확히 일치하는 서버 receipt만 local ACK로 승인한다.
- 기존 `development_local_only` 데이터를 자동 승격하지 않는다.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 남은 범위 |
| --- | --- | --- | --- |
| Canonical wire body | `local 검증됨` | 고정 field projection, optional 정규화, extra field 제거 | SQLite body·digest 영속화 |
| Sample gate | `local 검증됨` | 1~500개, UUID·UTC·범위·sequence 검사 | corrupted SQLite integration |
| ACK boundary | `local 검증됨` | ID·count·state가 맞는 200/202만 승인 | 실제 gateway response |
| Failure disposition | `local 검증됨` | auth/hold/retry와 409 세 종류 구분 | backoff·lease state machine |
| Privacy | `local 코드 검토` | 오류에 좌표·server detail을 복사하지 않음 | transport log end-to-end scan |
| Runtime | `미연결` | recorder·SQLite·HTTP에서 호출하지 않음 | Firebase token·transport·gateway startup |

독립 검토에서 객체 spread가 런타임 extra field와 key order를 body에 보존해 raw-body idempotency를 깨뜨릴 수 있는 문제를 발견했다. 커밋 전에 explicit projection, body-only return, activity/mock runtime validation과 bounded 409 분류로 정정하고 회귀 테스트를 통과했다. 이 실패는 미배포 local 코드 검토 단계에서 발견돼 incident 기준에 해당하지 않는다.

## 3. 근거

| 실제 주장 | 증거 | 검증 상태 |
| --- | --- | --- |
| Pure mobile upload protocol과 독립 리뷰 정정 | [EVD-20260723-045](../../evidence/2026-07.md#evd-20260723-045--모바일-immutable-telemetry-upload-protocol) | `generated` — local unit/typecheck, 사람 검토 대기 |

## 결정·제품 변화·인시던트

- 결정: [ADR-0034](../../decisions/ADR-0034-immutable-mobile-upload-body.md)
- 제품 변화: [UPD-20260723-11](../../product-updates/UPD-20260723-11-mobile-upload-protocol.md) — local source-only engineering foundation
- 인시던트: 해당 없음 — staging·production·field 영향 없음

## 다음 회차

1. exact body와 digest를 SQLite에 영속화하고 앱 재시작 뒤 같은 bytes를 복원한다.
2. pending→lease→retry/hold/ack 전이를 구현한다.
3. Firebase Auth/App Check와 server-managed scope가 연결되기 전에는 transport를 닫아 둔다.
4. 이후 Android synthetic 경로에서 offline→reconnect→202/replay 200을 검증한다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 참석자·사진·지출: 해당 없음
- 이 문서는 개발 결과 요청 리포트이며 회의록이 아니다.

## 발행 전 검토

- [x] 계획과 실제를 분리했다.
- [x] local synthetic와 실기기·field를 구분했다.
- [x] 기존 GPS 업로드 또는 앱→서버 완료로 표현하지 않았다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 실제 주장과 근거를 검토했다.
