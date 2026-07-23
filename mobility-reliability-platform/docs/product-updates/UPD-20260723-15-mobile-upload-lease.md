---
id: UPD-20260723-15
date: 2026-07-23
status: draft
version_or_deployment: mobile-upload-lease-v1-sqlite-v3
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: 모바일 exact-body upload lease

## 요약

SQLite에 저장된 telemetry body를 전송 직전에 exact bytes로 다시 hash하고, due인
pending batch 또는 만료된 lease에만 bounded transport authority를 발급하는 local
upload lease를 추가했다. Body/digest mismatch, malformed stored digest 또는
control metadata 오류가 있으면 network 전송을 시작하지 않고 batch와 연결된
outbox를 원자 hold한다.

## 변경 전 문제

- Materializer가 저장한 body와 digest를 전송 직전에 재검증하는 runtime이 없었다.
- Persisted retry/lease time을 SQL TEXT로 비교하면 malformed 값이 authority를
  조기에 얻거나 영구 정지할 수 있었다.
- SQLite `attempt_count`가 safe integer인지 확인하지 않았다.
- GPS writer와 exclusive sync transaction의 connection별 busy policy가 달랐다.

## 변경 후 동작

- Active batch가 없으면 clock, hash와 lease authority provider를 호출하지 않는다.
- Stored retry/lease metadata를 canonical UTC epoch로 검증한 뒤 due/expired를 판정한다.
- Stored attempt를 SQLite storage class와 exact text로 읽고 safe integer만 허용한다.
- Exact stored body hash와 lowercase digest가 같을 때만 최대 5분 lease를 발급한다.
- Digest·retry·lease·attempt metadata 오류는 bounded reason의 parent/child atomic
  hold로 수렴한다.
- Provider failure, invalid new owner/expiry와 schema cardinality failure는 rollback한다.
- Main·materializer·lease SQLite connection 모두 5초 busy timeout을 사용한다.

## 범위

- 포함: upload digest 공통 validator, lease core, Expo Crypto/SQLite wrapper,
  pending lease, expired takeover, control metadata hold, Node SQLite regression.
- 제외: 실제 HTTP request, network retry disposition, ACK terminal transaction,
  Firebase Auth/App Check, server-managed session, native two-connection contention,
  background GPS와 staging/production 배포.
- 배포 환경: `local source/static bundle`
- 데이터 유형: `synthetic GPS fixtures only`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 |
| --- | --- | --- | --- |
| Exact stored body 재검증 | Node SHA-256 hasher input·returned body equality | `pass` | [EVD-20260723-049](../evidence/2026-07.md#evd-20260723-049--모바일-exact-body-upload-lease와-control-metadata-hold) |
| Digest mismatch parent/child hold | Actual v3 schema transaction | `pass` | EVD-20260723-049 |
| Provider failure write 0 | Injected hash failure·malformed result | `pass` | EVD-20260723-049 |
| Retry/lease/attempt metadata fail-closed | Canonical boundary·REAL/TEXT/unsafe integer fixtures | `pass` | EVD-20260723-049 |
| Pending·expired authority boundary | Future, exact-now, expired, max-5-minute cases | `pass` | EVD-20260723-049 |
| Native connection contention | Android development build 필요 | `미검증` | 후속 gate |

## 배포와 롤백

- App Store, staging과 production 배포는 수행하지 않았다.
- 현재 UI session은 `development_local_only`이므로 이 lease runtime을 호출하지 않는다.
- Schema version은 바꾸지 않았다. Source rollback은 아직 field data가 없는 local
  단계에서만 가능하며 이미 held된 row를 자동 pending으로 되돌리지 않는다.

## 알려진 제한과 후속 작업

- 현재 concurrency test는 single in-memory connection 앞의 exclusive queue로
  logical single winner만 검증한다. 실제 Expo 두 connection의 busy wait/loser 결과는
  development build에서 검증한다.
- SHA-256 equality는 MAC이 아니며 body와 digest 동시 변조를 인증하지 않는다.
- 다음 gate는 leased→pending retry와 exact ACK의 batch/outbox terminal transaction이다.
- 그 뒤 Firebase Auth/App Check, server-managed scope와 HTTP transport를 연결한다.

## 관련 기록

- 결정: [ADR-0036](../decisions/ADR-0036-fail-closed-mobile-upload-lease.md)
- 증거: [EVD-20260723-049](../evidence/2026-07.md#evd-20260723-049--모바일-exact-body-upload-lease와-control-metadata-hold)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
- 사람 대상 리포트: [HR-20260723-40](../reports/human/HR-20260723-40-mobile-upload-lease.md)
- 대체하는 업데이트: 없음 — [UPD-20260723-13](./UPD-20260723-13-mobile-upload-materializer.md)의 후속 transport-authority 증분

## 검토

- 검토자: Codex + delegated independent read-only review, 사람 검토 대기
- 실제 주장과 근거 일치 여부: WSL2 Node SQLite·local static bundle 범위에서 일치
- 검토 메모: Native contention, HTTP upload, ACK 또는 7월 sync gate 완료로 표현하지 않는다.
