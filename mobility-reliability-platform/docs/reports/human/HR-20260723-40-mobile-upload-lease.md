---
id: HR-20260723-40
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: mobile exact-body lease partial
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: 모바일 upload lease 무결성 경계

## 한눈에 보기

- 계획상 위치: 7월 앱→서버 sync 중 local batch의 전송권한 발급 단계.
- 실제 결과: Stored body를 exact bytes로 다시 hash하고 정상 due batch에만 최대
  5분 lease를 발급하는 local runtime을 구현했다.
- 중요한 제한: HTTP, retry·ACK terminal transaction과 server-managed scope는
  아직 없으며 현재 사용자 flow는 local-only다.

## 8개월 로드맵 대비 현재 위치

| 월 | 계획한 결과 | 보고 시점 실제 상태 |
| --- | --- | --- |
| 5월 | 신규 앱·foreground GPS | Android emulator native foreground smoke 완료 |
| 6월 | Background GPS·offline lifecycle | Foreground SQLite·restart 복구. Background 미구현 |
| 7월 | Auth·upload·ACK·정제 경로·지도 | Protocol·ledger·materializer·fail-closed lease 구현. HTTP·ACK·지도 미완료 |
| 8~12월 | ML→Twin→AI report→실증→통합 | 미착수 |

## 계획과 실제

| 항목 | 7월 계획 | 실제 | 남은 범위 |
| --- | --- | --- | --- |
| Body authority | 전송 전 immutable body 검증 | Exact stored string SHA-256 재검증 | HTTP 연결 |
| Pending lease | Due batch 단일 owner | UUID owner·최대 5분·attempt 원자 증가 | Native contention |
| Takeover | 만료 lease만 재획득 | Canonical epoch와 exact old-state CAS | 앱 restart native test |
| Corruption | 잘못된 local state 전송 금지 | Digest·retry·lease·attempt parent/child hold | Operator recovery UX |
| Writer contention | GPS와 sync 공존 | 모든 SQLite connection busy timeout 5초 | 실제 lock-time 측정 |
| Server result | Retry·ACK 원자 처리 | 미구현 | 다음 gate |

개발 중 독립 리뷰는 persisted timestamp를 SQL 문자열로 먼저 비교하면 malformed
low/high value가 조기 takeover 또는 영구 정지를 일으킬 수 있음을 발견했다. Active
row를 먼저 읽고 canonical epoch로 검증하도록 정정했다. 후속 리뷰는 main GPS
connection의 busy timeout 누락과 unsafe attempt count 강제 변환을 발견했다. 모든
writer의 timeout을 통일하고 attempt를 storage class+text로 검증해 REAL·TEXT·unsafe
integer를 hash 전에 hold하도록 정정했다. 최종 재리뷰에서 코드상 잔여 High/Medium은
없었다. 이는 미배포 local 구현 중 발견돼 Incident 기준에는 해당하지 않는다.

## 근거

- [EVD-20260723-049](../../evidence/2026-07.md#evd-20260723-049--모바일-exact-body-upload-lease와-control-metadata-hold)
- 구현 commit: `1132883`
- 결정: [ADR-0036](../../decisions/ADR-0036-fail-closed-mobile-upload-lease.md)
- 제품 변화: [UPD-20260723-15](../../product-updates/UPD-20260723-15-mobile-upload-lease.md)
- 인시던트: 해당 없음 — production·staging·field 영향 없음

## 검증 범위

- 모바일 Vitest 7개 파일 130건 통과
- TypeScript `tsc --noEmit` 통과
- Android·iOS Expo static export 통과
- Exact bytes, whitespace 차이, digest mismatch, provider error/result, future
  backoff, unexpired/exact-now/expired lease, invalid owner/expiry, malformed persisted
  time과 REAL·TEXT·unsafe 64-bit attempt fixture 통과
- Single-connection exclusive queue에서 concurrent 두 caller가 `leased` 1건과
  `none` 1건, attempt 1로 수렴

실제 Expo 두 native connection의 `BEGIN IMMEDIATE` 경쟁과 5초 timeout loser는
아직 검증하지 않았다. 이를 분산 또는 native concurrency 완료로 보고하지 않는다.

## 다음 회차

1. Transport failure의 leased→pending backoff와 terminal hold를 구현한다.
2. Exact ACK에서 batch를 먼저 acknowledged하고 bound outbox 전체를 같은
   transaction에서 acknowledged한다.
3. Commit-response loss 뒤 terminal state를 read-only로 상관한다.
4. Firebase Auth/App Check와 server-managed assignment/current consent scope를 연결한다.
5. Android development build에서 lease writer 대 GPS writer contention을 측정한다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 참석자·사진·지출: 해당 없음
- 이 문서는 실제 개발 결과 요청 리포트이며 회의록이 아니다.

## 발행 전 검토

- [x] 계획과 실제 구현을 분리했다.
- [x] Local SQLite test와 Expo native contention을 분리했다.
- [x] SHA equality를 MAC·보안 저장소로 과장하지 않았다.
- [x] 실제 사용자 GPS, 복지관 성과와 production 배포를 주장하지 않았다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 실제 주장과 근거를 검토했다.
