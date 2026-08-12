# UPD-20260813-23 — Firestore shadow projection promotion 계획

- 기준일: 2026-08-13
- 상태: implemented / local Firestore Emulator·Rules 검증
- 로드맵 위치: R09 Device Timeline & Reliability
- 대상: server-only device current state와 per-device checkpoint promotion

## 제품·공학 변화

pure deterministic replay가 만든 device state를 먼저 Firestore shadow projection으로 작성하고, 검증된 shadow 결과만 authoritative `state/current`와 per-device checkpoint로 한 transaction에서 승격한다.

동일 replay binding은 idempotent하게 수렴시키고, replayRun/device/tenant/projector/checksum/input boundary가 다르면 binding conflict로 거부한다. corrupt shadow, checkpoint drift, stale revision은 current를 유지한 채 fail-closed한다. client direct write와 external side effect는 허용하지 않는다.

## 완료 범위

- shadow state binding과 projector/checkpoint metadata
- current + per-device checkpoint atomic promotion transaction
- exact replay idempotency와 concurrent stale promotion conflict
- corrupt/mismatched shadow·checksum·binding fail-closed
- Firestore Rules client write deny와 purpose-limited read boundary
- promotion 중 FCM/SMS/외부 API/도메인 command/지원금 변경 0 검증

## 검증 결과와 경계

- domain-command Firestore Emulator command/projection/promotion 14 scenarios 통과
- source query bounded read, deterministic shadow create-once, current+checkpoint atomic transaction
- exact replay revision 유지, same run의 input drift와 tampered shadow fail-closed
- Rules Emulator 40 tests: source/shadow/checkpoint 모든 client 차단, current는 운영자 read-only
- promotion adapter는 FCM·SMS·외부 API·수리 command·지원금 ledger API를 호출하지 않음

이는 local Emulator 증거다. production 배포, background worker scheduling, 실제 복지관 사용이 완료됐다는 뜻이 아니다.

관련: [ADR-0053](../decisions/ADR-0053-firestore-shadow-projection-promotion.md), [ADR-0052](../decisions/ADR-0052-device-current-state-deterministic-replay.md), [R09](../reports/fixed/2026-09-15.md), [EVD-20260813-019](../evidence/2026-08-product.md#evd-20260813-019--firestore-shadow-projection-atomic-promotion)
