---
id: HR-20260723-37
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: mobile SQLite upload ledger partial
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: 모바일 SQLite upload ledger v2

## 한눈에 보기

- 목적: 6월 local GPS event와 7월 server upload 사이에 앱 재시작·중복·잘못된 ACK를 견디는 durable ledger를 둔다.
- 실제: SQLite v2 schema, v1 migration과 immutable scope/event/batch/item·state machine을 local Node SQLite에서 검증했다.
- 제한: 실제 app은 아직 local-only session만 만들며 materializer·digest·HTTP가 없어 batch row와 server upload가 발생하지 않는다.

## 8개월 로드맵 대비 현재 위치

| 월 | 계획한 결과 | 보고 시점 실제 상태 |
| --- | --- | --- |
| 5월 | 새 앱·데이터 구조, GPS 수집·지도 | 앱·계약·foreground code 완료, 실제 경로 지도 없음 |
| 6월 | background GPS, offline 보존·재동기화 | foreground event log와 v2 migration/ledger 구현. background·실기기·재연결 미완료 |
| 7월 | 앱→서버, gateway, 정제 경로·히트맵 | 모바일 wire protocol과 durable schema, 서버 ingest 안전성 기반 완료. Materializer·token·HTTP·지도·히트맵 미완료 |
| 8~12월 | ML→Digital Twin→AI report→실증→통합 발표 | 미착수 |

## 계획과 실제

| 항목 | 계획 | 실제 | 남은 범위 |
| --- | --- | --- | --- |
| 기존 데이터 | local GPS 자동 승격 금지 | v1 row를 local/non-deliverable로 migration하고 scope update 차단 | Expo native fixture test |
| Batch 원문 | body·digest·items durable 저장 | schema와 invariant 구현 | materializer·실제 digest 계산 |
| Event binding | 보낸 sample만 ACK | JSON position과 immutable event의 ID·sequence·좌표·sensor 결합 | runtime batch 생성 |
| Retry | exact body 재사용 | pending/leased/pending/ACK·hold 전이 허용 | lease runner·backoff |
| ACK | exact receipt 뒤 terminal | batch ACK 뒤 관련 outbox ACK만 허용 | HTTP response→transaction 연결 |
| 실기기 | Android/iPhone migration·restart | 미검증 | WSL+ADB/EAS development build |

개발 중 독립 리뷰에서 local scope 승격, event session 변경, 초기 terminal INSERT, item/body mismatch 등 DB 우회 경로를 순차적으로 발견했다. 모두 커밋 전에 trigger·composite FK·canonical JSON binding과 회귀 테스트로 정정했고 최종 재검토에서 잔여 High가 없었다. 미배포 local 설계 단계이므로 incident 기준에는 해당하지 않는다.

## 근거

- [EVD-20260723-046](../../evidence/2026-07.md#evd-20260723-046--모바일-sqlite-upload-ledger-v2) — `generated`, local/Node/static bundle, 사람 검토 대기
- 선행: [EVD-20260723-045](../../evidence/2026-07.md#evd-20260723-045--모바일-immutable-telemetry-upload-protocol)

## 결정·제품 변화·인시던트

- 결정: [ADR-0034](../../decisions/ADR-0034-immutable-mobile-upload-body.md)
- 제품 변화: [UPD-20260723-12](../../product-updates/UPD-20260723-12-mobile-upload-ledger.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음

## 다음 회차

1. Server-managed scope에서만 새 server-bound session을 생성하는 경계를 연결한다.
2. Pending event를 canonical body+SHA-256으로 한 번 materialize하고 재시작 뒤 같은 bytes를 복원한다.
3. Lease/backoff/ACK transaction과 transport interface를 연결한다.
4. Android development build에서 v1 fixture migration과 offline→reconnect를 검증한다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 참석자·사진·지출: 해당 없음
- 이 리포트는 실제 개발 결과 보고이며 회의록이 아니다.

## 발행 전 검토

- [x] 7월 최종 데모와 현재 partial ledger를 구분했다.
- [x] Node SQLite와 Expo native·field를 구분했다.
- [x] 기존 GPS upload, server 연결 또는 실기기 완료로 표현하지 않았다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 실제 주장과 근거를 검토했다.
