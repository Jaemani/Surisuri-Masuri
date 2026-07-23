---
id: HR-20260723-38
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: mobile durable batch materialization partial
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: 모바일 업로드 배치 materializer

## 한눈에 보기

- 계획상 위치: 7월의 앱→서버 전달 경계 중 모바일 배치 생성·보존.
- 실제 결과: 한 session의 pending GPS를 최대 500개 canonical body로 만들고 SHA-256, item과 함께 SQLite에 원자 저장하는 local runtime을 구현했다.
- 현재 제한: server-bound session, lease·HTTP·ACK와 실기기 검증이 없어 실제 서버 업로드는 발생하지 않는다.

## 8개월 로드맵 대비 현재 위치

| 월 | 계획한 결과 | 보고 시점 실제 상태 |
| --- | --- | --- |
| 5월 | 신규 앱·구조·GPS·지도 | 앱·문서·계약·foreground code. 실제 경로 지도 없음 |
| 6월 | background GPS·SQLite offline sync | foreground GPS event log, schema v3와 durable batch local runtime. Background·실기기·재연결 미완료 |
| 7월 | 앱→서버·gateway·정제 경로·히트맵 | Server ingest 안전성 기반, mobile wire protocol·ledger·materializer 구현. Token·HTTP·ACK·지도 미완료 |
| 8~12월 | ML→Twin→AI report→실증→통합 | 미착수 |

## 계획과 실제

| 항목 | 7월 계획 | 실제 | 남은 범위 |
| --- | --- | --- | --- |
| Batch 생성 | GPS event 최대 500개 canonical 묶음 | Single-session ordered materializer 구현 | Native file·강제종료 |
| 재시작 | 같은 body를 재사용 | 기존 active batch 우선 재발견, generator 재호출 0 | App lifecycle 실기기 |
| 무결성 | Partial batch 금지 | Batch+items+outbox 한 transaction, injected failure rollback | Native SQLite locking |
| Digest | exact body SHA-256 | Node/Expo provider 경계와 lowercase 검증 | Lease 전 재검증 |
| Migration | 기존 local data upload 금지 | V1 local 고정 유지, v2 NULL gap 탐지, schema v3 차단 | Android/iPhone fixture |
| 전송 | Auth·App Check·HTTP·ACK | 미구현 | 다음 개발 gate |

개발 중 독립 리뷰에서 v2 trigger의 SQL NULL 우회, Expo v57 transaction connection의 FK 미상속과 cached connection 재사용 가능성을 발견했다. Schema v3 fail-closed migration, malformed existing-row gate, `{ useNewConnection: true }`, FK 이중 확인과 regression으로 정정했다. 최종 재검토에서 잔여 High/Medium은 없었다. 이는 미배포 local 개발 중 발견된 문제로 incident 기준에는 해당하지 않는다.

## 근거

- [EVD-20260723-047](../../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3) — WSL2, Node SQLite, synthetic fixture, static bundle
- 구현 commit: `7ef9dd19ca9608c42d3aabcd313f4152c19d48d2`

## 결정·제품 변화·인시던트

- 결정: [ADR-0035](../../decisions/ADR-0035-single-flight-mobile-batch-materialization.md)
- 제품 변화: [UPD-20260723-13](../../product-updates/UPD-20260723-13-mobile-upload-materializer.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음

## 다음 회차

1. Stored body의 SHA-256을 lease 직전에 재검증하고 pending→leased 전이를 구현한다.
2. Network failure의 leased→pending, exact ACK의 batch/outbox terminal transaction을 구현한다.
3. Firebase Auth/App Check와 server-managed assignment/current consent로만 새 server-bound session을 만든다.
4. Android synthetic offline→reconnect→202→replay 200 시나리오를 HTTPS staging에서 검증한다. iPhone은 HTTPS staging 전 server E2E 완료로 승격하지 않는다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 참석자·사진·지출: 해당 없음
- 이 문서는 실제 개발 결과 요청 리포트이며 회의록이 아니다.

## 발행 전 검토

- [x] 로드맵 계획과 실제 구현을 분리했다.
- [x] Node SQLite·static export와 Expo native·실기기·서버 E2E를 분리했다.
- [x] 실제 사용자 GPS, 복지관 성과와 production 배포를 주장하지 않았다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 실제 주장과 근거를 검토했다.
