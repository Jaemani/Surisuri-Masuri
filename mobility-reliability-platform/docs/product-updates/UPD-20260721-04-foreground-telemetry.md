---
id: UPD-20260721-04
date: 2026-07-21
status: draft
version_or_deployment: mobile-foreground-telemetry-v0
roadmap_month: M2
owner: project owner
reviewed_at: 2026-07-21
---

# 제품 업데이트: Foreground GPS와 SQLite outbox 첫 구현

## 요약

모바일 앱에 위치 권한 확인, 명시적 주행 시작·종료, foreground GPS 수집, SQLite WAL event log와 재시작 복구를 구현했다. 화면은 좌표 대신 저장·거부·전송 대기 개수만 보여주며 server sync와 background 수집은 아직 제공하지 않는다.

## 변경 전 문제

- 앱은 GPS와 오프라인 저장이 미착수라고 표시하는 정적 셸이었다.
- Expo·SQLite 의존성은 있었지만 위치 권한, 세션 lifecycle, 이벤트 순서와 재시작 복구가 실행 코드로 연결되지 않았다.

## 변경 후 동작

- foreground 위치 권한을 `undetermined`, 재요청 가능 거부, 설정 필요 거부, 허용으로 구분한다.
- 사용자가 주행 시작을 누르면 수집 세션을 만들고 5초 또는 5m 기준의 High accuracy 위치 update를 요청한다.
- session start, accepted sample, rejected sample reason, session stop을 순서 번호가 있는 append-only log로 저장한다.
- event sequence와 accepted sample sequence를 분리해 거부·종료 event가 wire sample 순서에 영향을 주지 않는다.
- 전송 상태는 event payload와 다른 table에 저장해 acknowledgment가 payload를 수정하지 않게 한다.
- SQLite는 WAL과 exclusive transaction으로 event·projection·delivery insert 순서를 묶는다.
- 앱 재시작에서 미종료 세션이 있으면 자동 GPS 재개 대신 재개·종료 선택을 표시한다.
- 현재 Auth·기기·동의가 없는 세션은 installation ID와 `development_local_only`를 저장해 서버 업로드 대상에서 제외한다.
- watcher runtime error, 빠른 이중 동작과 종료 직전 callback을 error handler·operation lock·callback gate로 차단한다.
- 원본 좌표는 UI와 오류 로그에 출력하지 않는다.

## 범위

- 포함: foreground permission, explicit trip session, GPS sample policy, SQLite WAL, local outbox, restart recovery UI, Android/iOS JS bundle.
- 제외: background GPS, 화면 잠금 지속성, server upload·acknowledgment, Firebase Auth/App Check 연결, 실제 실기기 GPS·배터리 측정, 원격 삭제.
- 배포 환경: `local bundle`
- 데이터 유형: `synthetic unit fixture` / 실제 위치 수집 데이터 없음

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| 권한·sample·capture guard 정책 | Vitest 3파일 65건 | `pass` | [EVD-20260721-007](../evidence/2026-07.md#evd-20260721-007--foreground-telemetry-정적정책-검증) |
| 모바일 TypeScript | `tsc --noEmit` | `pass` | [EVD-20260721-007](../evidence/2026-07.md#evd-20260721-007--foreground-telemetry-정적정책-검증) |
| Expo project health | Expo Doctor 20개 검사 | `pass` | [EVD-20260721-007](../evidence/2026-07.md#evd-20260721-007--foreground-telemetry-정적정책-검증) |
| Android·iOS JS bundle | Expo export 2개 platform | `pass` | [EVD-20260721-007](../evidence/2026-07.md#evd-20260721-007--foreground-telemetry-정적정책-검증) |
| clean runner 재현 | GitHub Actions install·check·build·test | `pass` | [EVD-20260721-008](../evidence/2026-07.md#evd-20260721-008--github-clean-runner-ci) |
| 실제 장비 GPS·SQLite 동작 | Android·iPhone 실기기 | `미검증` | WSL ADB·EAS 후속 gate |

## 배포와 롤백

- 앱스토어·Firebase 배포는 수행하지 않았다. local export만 생성했고 `dist/`는 Git에서 제외한다.
- 기능 flag는 아직 없다. 실제 실증 전 Remote Config gate를 추가한다.
- 데이터베이스 파일명에 v1을 포함해 schema가 변경될 때 명시적 migration 또는 개발 DB 초기화를 선택할 수 있게 했다.
- 첫 schema는 `PRAGMA user_version=1`로 고정한다. 커밋 전 unversioned prototype table이 발견되면 조용히 잘못된 schema로 실행하지 않고 개발 앱 데이터 초기화를 요구한다.

## 알려진 제한과 후속 작업

- WSL PATH에 ADB가 없어 Android 실기기에서 native SQLite와 위치 callback을 실행하지 못했다.
- iPhone은 EAS development build 또는 Expo 호환 실행환경에서 별도 검증해야 한다.
- iOS에서 `timeInterval`은 적용되지 않으므로 Android와 동일한 5초 sampling으로 해석하지 않는다.
- Android backup은 비활성화했지만 iOS SQLite file protection·backup 제외는 후속 native 보안 gate다.
- 현재 수집은 foreground 전용이며 화면 잠금·background·앱 강제 종료 중 지속성을 보장하지 않는다.
- pending outbox를 Cloud Run으로 전송하거나 삭제하는 코드는 아직 없다.
- 다음 작업은 repository 동작의 native integration test, Android/iPhone foreground 실기기 검증, batch builder와 멱등 sync다.

## 관련 기록

- 결정: [ADR-0008](../decisions/ADR-0008-foreground-telemetry-slice.md)
- 증거: [EVD-20260721-007](../evidence/2026-07.md#evd-20260721-007--foreground-telemetry-정적정책-검증), [EVD-20260721-008](../evidence/2026-07.md#evd-20260721-008--github-clean-runner-ci)
- 인시던트: 해당 없음 — production·field 사용자 영향 없음
- 사람 대상 리포트: [HR-20260721-02](../reports/human/HR-20260721-02-foreground-telemetry.md)
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: Codex 자체 검토 — 실기기 사람 검토 필요
- 실제 주장과 근거 일치 여부: pure policy, typecheck, Android/iOS bundle 범위에서 일치
- 검토 메모: bundle 성공을 실기기 GPS·SQLite 검증으로 확대 해석하지 않는다.
