---
id: UPD-20260721-02
date: 2026-07-21
status: draft
version_or_deployment: executable-foundation-v0
roadmap_month: M1
owner: project owner
reviewed_at: 2026-07-21
---

# 제품 업데이트: 실행 가능한 모바일·계약 기반 추가

## 요약

신규 모노레포에 Expo SDK 57 기반 Android/iOS 공통 앱 셸과 실행 가능한 JSON Schema fixture 검사를 추가했다. 앱은 실제 기능 상태를 정직하게 표시하며 GPS와 오프라인 동기화는 아직 구현하지 않았다.

## 변경 전 문제

- 문서와 schema 파일은 있었지만 실행 가능한 모바일 package와 계약 성공·실패 검사가 없었다.
- JSON 구문이 맞다는 사실만으로 실제 validator가 계약을 compile하고 payload를 구분하는지 알 수 없었다.

## 변경 후 동작

- `apps/mobile`이 Expo/React Native TypeScript workspace package로 존재한다.
- 앱 공개 config는 내부 개발명과 iOS·Android 플랫폼을 선언한다.
- 초기 화면은 신규 기반 준비, GPS 미착수, 오프라인 동기화 미착수를 명시한다.
- `expo-location`, `expo-sqlite`는 다음 vertical slice용 호환 의존성으로 고정됐다.
- 계약 검사는 유효/무효 텔레메트리 batch와 domain event fixture를 각각 판별한다.

## 범위

- 포함: 모바일 앱 셸, 접근성 label이 있는 상태 화면, typecheck, Expo config, schema validator와 synthetic fixture.
- 제외: 위치 권한, 좌표 수집, SQLite runtime, background 실행, 서버 전송, 실기기 빌드, 현장 데이터.
- 배포 환경: `local`
- 데이터 유형: `synthetic` contract fixture / 모바일 실행 데이터 없음

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| 계약이 성공·실패 fixture를 구분 | `pnpm test` | `pass` | [EVD-20260721-003](../evidence/2026-07.md#evd-20260721-003--계약-성공실패-fixture-검증) |
| 모바일 TypeScript 정적 검사 | `tsc --noEmit` | `pass` | [EVD-20260721-004](../evidence/2026-07.md#evd-20260721-004--expo-모바일-스캐폴드-정적-검증) |
| Expo 공개 config의 iOS/Android 선언 | `expo config --type public` | `pass` | [EVD-20260721-004](../evidence/2026-07.md#evd-20260721-004--expo-모바일-스캐폴드-정적-검증) |

## 배포와 롤백

- 런타임 배포 없음. 로컬 개발 기반만 생성했다.
- 실기기 기능을 제공하지 않으므로 기능 플래그와 사용자 롤백은 해당 없다.
- 의존성이나 schema를 바꾸면 lockfile과 fixture 검사를 함께 갱신한다.

## 알려진 제한과 후속 작업

- 계약 검사기의 첫 실행은 schema 중복 compile 오류로 실패했고 validator cache 추가 후 전체 재실행했다.
- Expo 정적 config와 typecheck는 실기기 실행을 증명하지 않는다.
- 다음 제품 변경은 위치 권한 상태머신과 foreground GPS vertical slice다.

## 관련 기록

- 결정: [ADR-0002](../decisions/ADR-0002-mobile-gps-sessions.md), [ADR-0003](../decisions/ADR-0003-offline-event-sync.md), [ADR-0004](../decisions/ADR-0004-runtime-boundaries.md)
- 증거: [EVD-20260721-003](../evidence/2026-07.md#evd-20260721-003--계약-성공실패-fixture-검증), [EVD-20260721-004](../evidence/2026-07.md#evd-20260721-004--expo-모바일-스캐폴드-정적-검증)
- 인시던트: 해당 없음 — 로컬 최초 검사 실패는 SEV 기준을 충족하지 않음
- 사람 대상 리포트: [HR-20260721-01](../reports/human/HR-20260721-01-project-initiation.md)
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: Codex 자체 검토 — 사람 검토 필요
- 실제 주장과 근거 일치 여부: contract fixture, TypeScript, Expo public config 범위에서 일치
- 검토 메모: Android/iPhone 실기기 기능으로 확대 해석하지 않는다.
