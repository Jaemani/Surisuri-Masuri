---
id: UPD-20260811-01
date: 2026-08-11
status: draft
version_or_deployment: mobile-upload-state-v4-local
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: 모바일 upload disposition과 resilient local state

## 요약

M3(계획상 7월 sync·recovery gate)의 local SQLite upload state를 보강했다. Lease를
얻은 batch의 ACK·retry·hold 응답을 parent upload batch와 bound outbox child에 원자
적용하고, commit 응답 유실을 fresh read-only snapshot으로 상관한다. Schema v4
migration은 terminal state와 batch-item 연속성을 검사하며, 첫 bounded scan 뒤 due
row가 가려지는 경우를 위한 global due prefilter를 추가했다.

이 문서는 local source/static bundle에서 검증된 공학 증분이다. 앱스토어,
staging/production 배포, 실제 사용자·복지관 이용 가능성을 의미하지 않는다.

## 변경 전·후

- 변경 전: parent와 child disposition의 원자 경계, commit 응답 유실 판정, bounded
  window 뒤 due row fallback, terminal position audit가 부족했다.
- 변경 후: authority·binding·digest를 CAS로 확인한 하나의 exclusive transaction에서
  ACK/hold/retry를 적용한다. 첫 100개 FIFO/integrity scan 뒤에만 global due SQL
  prefilter를 사용하고, 후보는 canonical JS 검사를 다시 통과한다. Migration은
  writer lock 뒤 version을 읽고 position `0..sample_count-1` 연속성을 확인한다.

## 범위와 제외

| 구분 | 내용 |
| --- | --- |
| 포함 | React Native/Expo mobile local SQLite, upload lease·disposition core, schema v4 migration, retry policy, synthetic regression fixtures |
| 환경 | WSL2 local source, Node SQLite/static export gate |
| 데이터 | synthetic/test fixture only |
| 제외 | HTTP transport, offline→reconnect E2E, Firebase Auth/App Check, server scope, 실제 Expo multi-connection contention, Android/iPhone 실기기 E2E, staging/production/field |

## 검증

| 완료 조건 | 결과 | 근거 |
| --- | --- | --- |
| ACK·hold·retry parent/child atomic transition | pass | [EVD-20260811-001](../evidence/2026-08.md#evd-20260811-001--모바일-upload-disposition과-v4-state-integrity) |
| Commit response loss fresh-read correlation | pass | EVD-20260811-001 |
| Bounded scan 뒤 global due fallback 및 FIFO/integrity | pass | EVD-20260811-001 |
| v4 migration terminal/binding/position audit | pass | EVD-20260811-001 |
| Mobile tests | 15 files / 229 tests pass | EVD-20260811-001 |
| TypeScript, Android/iOS Expo static export | pass | EVD-20260811-001 |
| Workspace check/test, Firebase Rules | pass / Rules 24 tests | EVD-20260811-001 |
| Native multi-connection·HTTP·Auth/App Check·physical-device E2E | 미검증 | 후속 gate |

## 배포·롤백

- 배포: 수행하지 않음. 결과는 local source/static bundle 범위다.
- 롤백 기준: code commit [`a9a57cc`](https://github.com/Jaemani/Surisuri-Masuri/commit/a9a57ccb424ad6b6b66983e201436990d426e970) 이전 source. 실제 field database에 migration을 적용한 배포는 없었다.
- 실제 사용자 데이터, 기관 운영, staging/production runtime 영향은 관측되지 않았다.

## 제한과 다음 gate

- 실제 Expo `useNewConnection` 두 connection의 lock 경쟁과 busy-timeout loser는
  측정하지 않았다.
- Commit response loss 뒤 다른 worker가 재lease하면 lineage가 사라져
  `unverifiable`로 닫힐 수 있다. 재lease 후 lineage ledger는 후속 설계다.
- 다음 gate는 native SQLite contention, HTTP upload/reconnect, Firebase Auth/App Check,
  Android/iPhone 실기기 E2E다.

## 관련 기록

- 결정: [ADR-0036](../decisions/ADR-0036-fail-closed-mobile-upload-lease.md), [ADR-0039](../decisions/ADR-0039-atomic-mobile-upload-disposition.md)
- 증거: [EVD-20260811-001](../evidence/2026-08.md#evd-20260811-001--모바일-upload-disposition과-v4-state-integrity)
- 사람 대상 리포트: [HR-20260811-01](../reports/human/HR-20260811-01-mobile-upload-disposition.md)
- 인시던트: 해당 없음 — 미배포 local 구현·검증 단계
