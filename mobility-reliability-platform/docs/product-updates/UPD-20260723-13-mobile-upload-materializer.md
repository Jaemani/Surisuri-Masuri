---
id: UPD-20260723-13
date: 2026-07-23
status: draft
version_or_deployment: mobile-upload-materializer-v1-sqlite-v3
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: 모바일 불변 업로드 배치 materializer

## 요약

Pending server-bound GPS event를 session별 최대 500개 canonical batch로 한 번만 만들고, exact body와 SHA-256을 SQLite에 원자 저장하는 materializer를 추가했다. 앱 재호출 시 새 body를 만들지 않고 기존 활성 batch를 metadata로 재발견한다. 이 변경은 local synthetic 검증 범위이며 현재 사용자 flow는 local-only라 실제 전송을 시작하지 않는다.

## 변경 전 문제

- SQLite v2에는 body·digest·item table이 있었지만 이를 실제로 채우는 runtime이 없었다.
- Batch insert 뒤 item insert가 중단되면 partial ledger가 남을 수 있었다.
- Materializer 재호출이 같은 event를 다른 body·ID로 다시 만들 위험이 있었다.
- V2 초기 batch trigger는 JSON scope의 `NULL` 값이 SQL 3값 논리로 검증을 우회할 수 있었다.
- Expo v57의 새 transaction connection은 기본 connection의 FK 설정을 상속하지 않는다.

## 변경 후 동작

- 기존 `pending|leased` batch가 있으면 generator와 hasher를 호출하지 않고 그 batch reference를 반환한다.
- 활성 batch가 없을 때 한 server-bound session의 pending GPS만 sequence 순으로 최대 500개 묶는다.
- Canonical body, lowercase SHA-256, batch와 item을 전용 SQLite connection의 한 `BEGIN IMMEDIATE` transaction에 기록한다.
- Item trigger가 outbox를 `batched`로 바꾸며 최종 count를 확인한다. 중간 실패는 batch, item과 outbox를 모두 rollback한다.
- Public 결과에는 좌표와 body를 포함하지 않는다. Digest provider 오류도 stable code로 바꾼다.
- Schema v3는 `NULL` 검증 우회를 차단하고 기존 v2 malformed batch를 migration 전에 탐지한다.
- 화면의 server upload 대기 수는 materialize 전 pending event와 materialize 후 활성 batch sample을 함께 계산한다.

## 범위

- 포함: materializer core, Expo Crypto SHA-256 wrapper, 전용 SQLite connection과 FK gate, schema v3, v1→v2→v3 migration, Node SQLite integration regression.
- 제외: server-bound session 발급, Firebase token/App Check, lease·backoff·ACK store, HTTP transport, 실제 서버 replay, background GPS, 실기기 native migration·강제종료.
- 배포 환경: `local source/static bundle`
- 데이터 유형: `synthetic GPS fixtures only`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 |
| --- | --- | --- | --- |
| Canonical body·실제 SHA 저장 | Node SQLite + Node SHA-256 | `pass` | [EVD-20260723-047](../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3) |
| 재호출 시 body 재생성 없음 | dependency call guard·stored row 비교 | `pass` | [EVD-20260723-047](../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3) |
| 500 cap·session 분리·local 배제 | actual v3 schema integration | `pass` | [EVD-20260723-047](../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3) |
| Item 중간 실패 전체 rollback | injected second-item failure | `pass` | [EVD-20260723-047](../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3) |
| V2 NULL 우회 탐지·v3 차단 | old-schema fixture + migration regression | `pass` | [EVD-20260723-047](../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3) |
| Android·iOS native connection | development build·실기기 | `미검증` | 후속 gate |

## 배포와 롤백

- 앱스토어, staging과 production 배포는 수행하지 않았다.
- 현재 코드가 생성하는 session은 모두 `development_local_only`이므로 materializer 대상이 아니다.
- Native v3 file이 생성된 뒤 source만 v2로 되돌리는 rollback은 지원하지 않는다. 실기기 승격 전 v1/v2 fixture forward migration과 backup/restore를 검증한다.
- 기존 v2에 malformed batch가 감지되면 자동 삭제·수정하지 않고 migration을 중단한다.

## 알려진 제한과 후속 작업

- Node SQLite와 static export는 Expo native connection, JSON1, file locking과 OS 강제종료를 증명하지 않는다.
- Lease 전에 stored body의 SHA-256을 다시 계산하고, exact ACK 뒤 batch와 outbox를 함께 terminal 처리해야 한다.
- Server-managed scope가 없는 기존 local session을 uploadable로 승격하지 않는다.
- HTTP·정제 경로·지도·H3는 아직 미연결이다.

## 관련 기록

- 결정: [ADR-0035](../decisions/ADR-0035-single-flight-mobile-batch-materialization.md)
- 증거: [EVD-20260723-047](../evidence/2026-07.md#evd-20260723-047--모바일-single-flight-upload-materializer와-sqlite-v3)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
- 사람 대상 리포트: [HR-20260723-38](../reports/human/HR-20260723-38-mobile-upload-materializer.md)
- 대체하는 업데이트: 없음 — [UPD-20260723-12](./UPD-20260723-12-mobile-upload-ledger.md)의 후속 runtime 증분

## 검토

- 검토자: Codex + delegated independent read-only reviews, 사람 검토 대기
- 실제 주장과 근거 일치 여부: WSL2 Node SQLite·local static bundle 범위에서 일치
- 검토 메모: 실기기 offline reconnect, HTTP upload, 서버 ACK 또는 7월 전체 게이트 완료로 표현하지 않는다.
