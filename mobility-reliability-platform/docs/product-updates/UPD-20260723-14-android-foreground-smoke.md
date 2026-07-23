---
id: UPD-20260723-14
date: 2026-07-23
status: draft
version_or_deployment: android-emulator-foreground-smoke-1
roadmap_month: M3
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: Android foreground GPS native smoke

## 요약

React Native 공통 앱의 현재 foreground vertical slice를 WSL 정적 검사 밖의
Android 11 native runtime에서 처음 실행했다. 위치 권한 요청, 합성 GPS 4건의
SQLite 저장과 Expo Go process 강제종료 뒤 active session·sample count 복구를
화면으로 확인했다.

## 변경 전 문제

- Android·iOS static export와 Node SQLite test만 있어 native module callback과
  실제 앱 화면을 증명하지 못했다.
- WSL 저장소와 Windows Android runtime을 연결하는 재현 가능한 절차가 없었다.
- 앱 재시작 복구는 코드와 pure/local database test에만 의존했다.

## 변경 후 동작

- Windows WHPX AVD가 WSL Metro에 ADB reverse로 연결된다.
- Expo Go 57에서 현재 React Native UI와 Android foreground 권한 dialog를 볼 수 있다.
- Synthetic emulator GPS sample이 native SQLite를 거쳐 화면의 저장 수를 4로 갱신했다.
- App process를 강제종료한 뒤 `중단된 기록 발견`과 같은 sample count가 복구됐다.
- WSL Runbook에 사용한 AVD, 실행 명령과 제한을 고정했다.

## 범위

- 포함: Android 11 emulator, Expo Go 57, foreground permission/GPS, fresh SQLite
  v3 database, process restart recovery, screenshot evidence.
- 제외: Android/iPhone 실제 장치, background GPS, 기존 DB migration, server-bound
  scope, Firebase Auth/App Check, HTTP upload·ACK, 지도와 production 배포.
- 배포 환경: `local Android emulator`
- 데이터 유형: `synthetic`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 |
| --- | --- | --- | --- |
| Native 화면 실행 | Android 11 AVD + Expo Go + Metro bundle | `pass` | [EVD-20260723-048](../evidence/2026-07.md#evd-20260723-048--android-foreground-gps와-sqlite-재시작-복구-smoke) |
| Android foreground 권한 | OS permission dialog·앱 상태 확인 | `pass` | EVD-20260723-048 |
| Native SQLite sample append | Synthetic location 주입, 저장 수 `0→4` | `pass` | EVD-20260723-048 |
| Process restart recovery | Force-stop·reopen, active session과 count 확인 | `pass` | EVD-20260723-048 |
| Background·offline HTTP | Development build·transport 미구현 | `미검증` | 후속 gate |

## 배포와 롤백

- App Store, Play internal track, staging 또는 production에 배포하지 않았다.
- Source code를 바꾸지 않은 native smoke이므로 사용자 대상 rollback은 없다.
- AVD와 Expo Go는 local 개발 도구이며 production 앱 식별자나 데이터를 사용하지 않는다.

## 알려진 제한과 후속 작업

- Expo Go는 background location 검증 환경이 아니다. Background task 구현 뒤
  development build를 Android·iPhone 실제 장치에서 검증한다.
- Android 11 x86 emulator 한 환경만 통과했다. 실제 Android OEM lifecycle과
  iPhone 권한·background mode는 별도 gate다.
- 다음 sync gate는 lease 전 body digest 재검증, HTTP transport, offline→reconnect와
  exact ACK transaction 순서로 진행한다.

## 관련 기록

- 결정: [ADR-0008](../decisions/ADR-0008-foreground-telemetry-slice.md)
- 증거: [EVD-20260723-048](../evidence/2026-07.md#evd-20260723-048--android-foreground-gps와-sqlite-재시작-복구-smoke)
- 인시던트: 해당 없음 — production·staging·field 영향 없음
- 사람 대상 리포트: [HR-20260723-39](../reports/human/HR-20260723-39-android-foreground-smoke.md)
- 대체하는 업데이트: 없음 — foreground code 기록 [UPD-20260721-04](./UPD-20260721-04-foreground-telemetry.md)의 native verification 후속

## 검토

- 검토자: Codex, 사람 검토 대기
- 실제 주장과 근거 일치 여부: Android 11 local emulator·synthetic data 범위에서 일치
- 검토 메모: 실기기, background, server sync 또는 7월 전체 완료로 표현하지 않는다.
