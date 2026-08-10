---
id: HR-20260811-01
report_type: requested
status: draft
period_start: 2026-08-11
period_end: 2026-08-11
issued_at: TBD
roadmap_month: M3 (계획상 7월)
technical_gate: mobile upload disposition and resilient local state
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: 모바일 upload disposition과 resilient local state

## 한눈에 보기

- 8개월 로드맵상 위치: M3, 앱 local outbox의 lease·retry·ACK·hold recovery 경계.
- 실제 기록일: 2026-08-11. 7월 계획 gate를 code commit `a9a57cc`에서 정리했다.
- 확인한 결과: parent batch와 child outbox의 atomic disposition, v4 state integrity,
  bounded due fallback, commit-response correlation을 local SQLite에서 검증했다.
- 제한: HTTP, Firebase Auth/App Check, 실제 Expo multi-connection contention,
  Android/iPhone 실기기 E2E, 배포와 현장 사용은 확인하지 않았다.

## 로드맵 대비 위치

| 월 | 계획 | 이번 보고서와의 관계 |
| --- | --- | --- |
| 5~6월 | 앱·GPS·background·offline local 기반 | 선행 기반 |
| 7월 (M3) | auth/upload/ACK·복구 경계 | 이번 보고서의 계획상 gate |
| 8~12월 | ML·Digital Twin·AI report·실증·통합 | 이번 보고서에서는 미검증 |

## 확인한 기술 변화

1. Lease authority를 canonical validation과 CAS로 재검증한다.
2. ACK는 parent batch와 정확히 bound된 child outbox를 한 transaction에서 전이한다.
3. Hold는 parent/child 전체를 같은 error code로 닫고, retry는 최대 15분 backoff와
   함께 pending으로 되돌린다.
4. 첫 100개 active row의 bounded FIFO/integrity scan 뒤 global due SQL prefilter를
   사용하되, 후보의 authority는 canonical JS 검사로 다시 판정한다.
5. Schema v4 migration에서 terminal binding, child position 연속성, writer-lock 뒤
   version read를 검사한다.
6. COMMIT이 불명확할 때 새 query-only reader로 상태를 상관하고, 확인 불가하면
   성공으로 처리하지 않는다.

## 검증 범위와 결과

| 항목 | 결과 |
| --- | --- |
| Mobile test files/tests | 15 files / 229 tests pass |
| TypeScript | pass |
| Android·iOS Expo static export | pass |
| Workspace check/test | pass |
| Firebase Rules | 24 tests pass |
| Source commit | [`a9a57cc`](https://github.com/Jaemani/Surisuri-Masuri/commit/a9a57ccb424ad6b6b66983e201436990d426e970) |
| 실행 환경 | WSL2 local source/Node SQLite/static export |

이 결과는 코드 계약과 synthetic/test fixture의 local 검증이다. 실제 단말, HTTP 서버,
Firebase 인증/앱 무결성, staging/production, 복지관 현장 결과로 확대하지 않는다.

## 중대한 오류·사고 여부

Production·staging·field 영향이 있는 심각한 오류나 사고는 없었다. 개발 중의 bounded
starvation, migration binding, transaction response-loss 경계는 local regression으로
정정·검증했으며 별도 인시던트로 분류하지 않았다.

## 다음 확인 지점

- Native development build에서 두 SQLite connection의 lock 경쟁과 busy timeout을
  측정한다.
- HTTP offline→reconnect와 server acknowledgment를 연결한다.
- Firebase Auth/App Check 및 server-managed scope를 붙인다.
- Android와 iPhone 실기기에서 background·sync·권한 흐름을 확인한다.

## 회의·증빙 확인

- 실제 회의 여부: 없음
- 참석자·사진·지출: 없음
- 이 문서는 사람에게 전달하는 기술 결과 보고이며 회의록이나 참석 증빙이 아니다.

## 근거와 검토

- [EVD-20260811-001](../../evidence/2026-08.md#evd-20260811-001--모바일-upload-disposition과-v4-state-integrity)
- [ADR-0036](../../decisions/ADR-0036-fail-closed-mobile-upload-lease.md)
- [ADR-0039](../../decisions/ADR-0039-atomic-mobile-upload-disposition.md)
- [UPD-20260811-01](../../product-updates/UPD-20260811-01-mobile-upload-disposition.md)
- 사람이 실제 주장과 근거를 확인하기 전까지 `draft`를 유지한다.
