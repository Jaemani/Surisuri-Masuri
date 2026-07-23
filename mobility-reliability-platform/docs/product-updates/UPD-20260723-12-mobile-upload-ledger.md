---
id: UPD-20260723-12
date: 2026-07-23
status: draft
version_or_deployment: mobile-sqlite-upload-ledger-v2
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: 모바일 SQLite upload ledger v2

## 요약

모바일 SQLite를 version 2로 올리고 기존 local GPS를 절대 업로드 대상으로 승격하지 않는 migration, immutable upload batch ledger와 retry·ACK 상태 제약을 추가했다. 이는 local 공학 기반이며 batch를 실제 생성·전송하는 runtime은 아직 연결하지 않았다.

## 변경 전 문제

- v1 outbox는 lifecycle·reject·GPS event를 모두 `pending`으로 표시했지만 무엇이 wire upload 대상인지 분리하지 않았다.
- exact JSON body, digest, batch 구성 event와 retry·ACK 상태를 앱 재시작 뒤 보존할 schema가 없었다.
- 기존 local session을 후속 코드가 server-bound로 바꾸거나 다른 event를 ACK 처리할 위험을 DB가 막지 않았다.

## 변경 후 동작

- Fresh database 생성과 v1→v2 migration은 transaction 안에서 수행하고 commit 전에 foreign-key integrity를 확인한다.
- 기존 v1 session·event·installation metadata는 보존하되 server scope와 과거 delivery metadata를 버리고 `development_local_only`·`local_only/not_applicable`로 고정한다.
- Session ID와 tenant/device/trip/consent/installation scope, append-only event, batch ID/body/digest/item은 생성 후 수정·삭제할 수 없다.
- Server-bound GPS는 반드시 upload delivery로, local-only GPS는 반드시 non-deliverable로 생성된다.
- Batch JSON의 top-level scope와 각 position의 sample ID·sequence·시간·좌표·sensor 값이 같은 immutable event와 일치할 때만 item으로 묶인다.
- Batch와 outbox는 pending→lease→retry/ACK/hold의 허용된 전이만 사용하며 item 수와 position이 sample count와 맞아야 lease 이후로 전진한다.
- UI의 두 번째 수치는 local event 수가 아니라 실제 server upload 대기 수로 명명했다.

## 범위

- 포함: SQLite schema v2, atomic v1 migration, upload scope·event·batch·item·state invariants, Node SQLite migration/state tests, UI count label.
- 제외: server-bound session 생성 API, batch materializer, SHA-256 계산·재검증, sync runner, token·HTTP transport, Expo native migration, 실기기·staging·field.
- 배포 환경: `local source/static bundle`
- 데이터 유형: `synthetic unit fixture`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 |
| --- | --- | --- | --- |
| Fresh v2와 v1 migration 무결성 | Node 22 SQLite in-memory | `pass` | [EVD-20260723-046](../evidence/2026-07.md#evd-20260723-046--모바일-sqlite-upload-ledger-v2) |
| Local 승격·event/body/item mismatch 차단 | SQLite trigger regression | `pass` | [EVD-20260723-046](../evidence/2026-07.md#evd-20260723-046--모바일-sqlite-upload-ledger-v2) |
| Retry·ACK 정상 상태 흐름 | SQLite state regression·독립 리뷰 | `pass` | [EVD-20260723-046](../evidence/2026-07.md#evd-20260723-046--모바일-sqlite-upload-ledger-v2) |
| Workspace regression | mobile 90, Firebase Rules 24, contract fixture 6 | `pass` | [EVD-20260723-046](../evidence/2026-07.md#evd-20260723-046--모바일-sqlite-upload-ledger-v2) |
| Android·iOS static bundle | Expo export | `pass` | [EVD-20260723-046](../evidence/2026-07.md#evd-20260723-046--모바일-sqlite-upload-ledger-v2) |
| Expo native v1 file→v2 restart | Android/iPhone development build | `미검증` | 후속 gate |

## 배포와 롤백

- 앱스토어·staging·production 배포는 수행하지 않았다.
- 현재 UI는 local-only session만 생성하므로 실제 upload batch row가 생기지 않는다.
- Source rollback만으로 이미 native device에서 v2가 생성된 경우를 안전하게 되돌릴 수 없으므로, 실기기 배포 전 forward migration·backup/restore gate가 필요하다.

## 알려진 제한과 후속 작업

- Node SQLite 결과는 Expo native `execAsync`와 OS 강제종료 복구 증거가 아니다.
- Materializer가 exact body와 SHA-256을 한 번 저장하고 lease 직전 재검증하는 코드가 필요하다.
- Firebase Auth/App Check와 server-managed assignment·current consent 없이는 server-bound session을 생성하지 않는다.
- 실제 HTTP upload와 gateway executable은 계속 미연결이다.

## 관련 기록

- 결정: [ADR-0034](../decisions/ADR-0034-immutable-mobile-upload-body.md)
- 증거: [EVD-20260723-046](../evidence/2026-07.md#evd-20260723-046--모바일-sqlite-upload-ledger-v2)
- 인시던트: 해당 없음 — local synthetic·static bundle 범위
- 사람 대상 리포트: [HR-20260723-37](../reports/human/HR-20260723-37-mobile-upload-ledger.md)
- 대체하는 업데이트: 해당 없음 — [UPD-20260723-11](./UPD-20260723-11-mobile-upload-protocol.md)의 후속 증분

## 검토

- 검토자: Codex + delegated independent reviews, 사람 검토 대기
- 실제 주장과 근거 일치 여부: Node SQLite·local static bundle 범위에서 일치
- 검토 메모: 이를 실기기 migration, offline reconnect, server upload 또는 7월 종단간 데모 완료로 표현하지 않는다.
