---
id: UPD-20260723-11
date: 2026-07-23
status: draft
version_or_deployment: mobile-upload-protocol-v0
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: 재시도 가능한 모바일 telemetry 원문 계약

## 요약

휴대폰 GPS sample을 `telemetry-batch.v2`의 고정 JSON 원문으로 만드는 모바일 protocol과 서버 응답 판정 규칙을 추가했다. 아직 SQLite batch store나 네트워크 전송에는 연결하지 않았으며 기존 `development_local_only` GPS는 계속 기기 밖으로 나가지 않는다.

## 변경 전 문제

- SQLite에는 위치 event가 쌓였지만 서버 wire batch를 만드는 코드가 없었다.
- 향후 재시도에서 객체를 다시 직렬화하면 서버의 raw-body 멱등성 정의와 어긋날 위험이 있었다.
- HTTP 성공 status만 보고 local sample을 지울 경우 다른 batch의 응답이나 불완전한 ACK를 잘못 승인할 수 있었다.

## 변경 후 동작

- 허용된 sample field만 고정 순서로 투영하고 canonical optional 값을 채워 exact JSON body를 만든다.
- 1~500개, UUID·UTC timestamp·좌표·sensor 범위와 strictly increasing sequence를 전송 전에 검사한다.
- 런타임 extra field와 입력 객체의 key 순서는 wire body에 포함되지 않는다.
- `200/202` 응답의 client batch ID, sample count, receipt ID, server batch ID와 state가 모두 맞을 때만 ACK로 분류한다.
- 인증, conflict, payload rejection, rate limit, server·network failure를 bounded disposition으로 구분하고 오류 detail이나 좌표를 결과에 넣지 않는다.

## 범위

- 포함: pure v2 batch builder, canonical body, response/failure classifier, unit tests.
- 제외: SQLite batch 영속화·digest, upload lease/backoff, Firebase Auth/App Check, HTTP transport, gateway runtime, 실기기·staging·field 검증.
- 배포 환경: `local source only`
- 데이터 유형: `synthetic unit fixture`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| canonical body와 strict sample gate | 모바일 Vitest | `pass` | [EVD-20260723-045](../evidence/2026-07.md#evd-20260723-045--모바일-immutable-telemetry-upload-protocol) |
| ACK·retry·hold 분류 | 모바일 Vitest | `pass` | [EVD-20260723-045](../evidence/2026-07.md#evd-20260723-045--모바일-immutable-telemetry-upload-protocol) |
| TypeScript 계약 | `tsc --noEmit` | `pass` | [EVD-20260723-045](../evidence/2026-07.md#evd-20260723-045--모바일-immutable-telemetry-upload-protocol) |
| 앱 재시작 뒤 exact body 재전송 | SQLite·실기기 | `미구현` | 후속 gate |

## 배포와 롤백

- 앱스토어, staging, Firebase 또는 Cloud Run 배포는 수행하지 않았다.
- 현재 recorder와 UI에서 이 protocol을 호출하지 않으므로 사용자 동작 변화는 없다.
- 롤백은 source commit revert이며 기기 DB migration은 아직 없다.

## 알려진 제한과 후속 작업

- exact body와 digest를 SQLite에 저장하고 앱 재시작 뒤 같은 bytes를 읽는 integration test가 필요하다.
- server-bound 새 session 생성에는 Firebase token, server-managed assignment와 current consent scope가 필요하다.
- gateway executable은 adapter 미구성으로 ingest `503`을 유지한다.
- 실제 Android/iPhone GPS, background 수집과 network reconnect는 검증하지 않았다.

## 관련 기록

- 결정: [ADR-0034](../decisions/ADR-0034-immutable-mobile-upload-body.md)
- 증거: [EVD-20260723-045](../evidence/2026-07.md#evd-20260723-045--모바일-immutable-telemetry-upload-protocol)
- 인시던트: 해당 없음 — local synthetic 검증이며 production·field 영향 없음
- 사람 대상 리포트: [HR-20260723-36](../reports/human/HR-20260723-36-mobile-upload-protocol.md)
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: Codex + delegated independent review, 사람 검토 대기
- 실제 주장과 근거 일치 여부: pure protocol unit·typecheck 범위에서 일치
- 검토 메모: 이를 GPS 업로드, offline reconnect 또는 서버 연동 완료로 표현하지 않는다.
