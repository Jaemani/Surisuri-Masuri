---
id: UPD-20260723-17
date: 2026-07-23
status: draft
version_or_deployment: android-development-client-local
roadmap_month: M2
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: Android 백그라운드 GPS native lifecycle

## 요약

React Native development client가 Android 11 emulator에서 화면 밖 위치 foreground
service를 실제로 시작하고, 합성 위치 callback을 기존 SQLite active session에 저장하며,
process 재시작 뒤 service를 복구하고 명시적 종료 때 해제하도록 보강했다.

이 업데이트는 WSL2 코드와 Windows Android emulator를 결합한 local native smoke다.
Android 실기기, iPhone, 현장 GPS, HTTP 동기화 또는 store 배포 결과가 아니다.

## 변경 전 문제

- Background task의 source·unit·static config만 확인했고 native service와 알림이 실제로
  실행되는지 증거가 없었다.
- Persisted Android job에 필요한 boot-completed permission이 없어 첫 callback에서 local
  process가 종료됐다.
- Process force-stop 뒤 SQLite session과 task registration은 남았지만 native service는
  사라졌고, 화면만 `주행 기록 중`으로 표시되는 liveness 불일치가 있었다.

## 변경 후 동작

- Android manifest에 `android.permission.RECEIVE_BOOT_COMPLETED`를 포함한다.
- Background 권한이 있는 active trip은 `LocationTaskService`를 foreground service로
  실행하고 사용자에게 `이동 기록 중` 알림을 표시한다.
- 앱이 launcher 뒤에 있는 동안 합성 위치 callback을 받아 공통 sample policy와 SQLite
  append-only event 경로로 저장한다.
- Active session이 있는 cold launch에서는 persisted registration을 명시적으로 재등록해
  실제 native service와 UI 상태를 맞춘다.
- 사용자가 주행 종료를 누르면 session을 종료하고 foreground service와 notification을
  해제한다.

## 범위

- 포함: Android 11 x86 emulator, Expo development client, background permission,
  foreground service notification, synthetic callback, SQLite active-session append,
  process force-stop·cold launch recovery, explicit stop.
- 제외: Android 실제 장치·OEM kill policy·재부팅·장시간 잠금, iPhone, 실제 야외 GPS,
  battery·accuracy benchmark, HTTP/Firebase sync, server-bound scope와 field pilot.
- 배포 환경: `local`
- 데이터 유형: `synthetic`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 |
| --- | --- | --- | --- |
| Background service와 알림 | `dumpsys activity services`, `dumpsys notification` | `pass` | [EVD-20260723-051](../evidence/2026-07.md#evd-20260723-051--android-background-gps-native-lifecycle-smoke와-cold-launch-복구) |
| 화면 밖 callback의 SQLite 반영 | 홈 화면에서 합성 좌표를 넣고 앱 복귀 후 count 비교 | `pass` — 최초 run 0→2, cold recovery 후 2→12 | EVD-20260723-051 |
| Process 재시작 뒤 service 복구 | Active session에서 force-stop 후 launcher 재진입 | `pass` | EVD-20260723-051 |
| 명시적 종료 | 종료 후 UI idle, service non-foreground·notification 없음 | `pass` | EVD-20260723-051 |
| Background runtime unit | Targeted 1 file/6 tests | `pass` | EVD-20260723-051 |
| 통합 mobile gate | 현재 결합 working tree 13 files/193 tests, typecheck, Android·iOS export | `pass` | EVD-20260723-051 |

저장 count는 emulator provider callback 수와 sample policy 결과이며 정확도·처리율 KPI가
아니다. 주입한 좌표 수와 1:1로 대응한다고 해석하지 않는다.

## 배포와 롤백

- Store·staging·production 배포는 없다. Local development APK와 Metro bundle에서만
  실행했다.
- Source 식별자는 `4049f10`이다.
- Rollback은 이 commit 이전 코드로 되돌릴 수 있으나, local SQLite event를 삭제하거나
  재작성하지 않는다.
- 이전 권한 없는 development APK 위에 replace install한 경로는 검증에서 제외하고
  emulator package clean install로 현재 manifest를 확인했다. 실제 upgrade compatibility는
  별도 gate다.

## 알려진 제한과 후속 작업

- Android 실기기에서 화면 잠금, recent-app swipe, OEM background restriction, 재부팅과
  30분 이상 수집을 검증한다.
- Actual two-connection Expo SQLite contention과 offline→reconnect upload를 연결한다.
- iPhone development build를 Mac/Xcode에서 같은 lifecycle checklist로 검증한다.
- 원본 위치 보존·삭제, iOS file protection과 backup 제외를 실제 GPS 전 확정한다.

## 관련 기록

- 결정: [ADR-0038](../decisions/ADR-0038-cold-launch-background-service-reconciliation.md)
- 증거: [EVD-20260723-051](../evidence/2026-07.md#evd-20260723-051--android-background-gps-native-lifecycle-smoke와-cold-launch-복구)
- 인시던트: 해당 없음 — 배포 전 local synthetic 검증에서 발견·수정
- 개발 실패 기록: [DEVFAIL-20260723-01](../development/DEVELOPMENT_FAILURE_LOG.md#devfail-20260723-01--android-background-job-crash와-false-recording-state)
- 사람 대상 리포트: [HR-20260723-42](../reports/human/HR-20260723-42-android-background-native-smoke.md)
- 대체하는 업데이트: [UPD-20260723-16](./UPD-20260723-16-background-gps-static-boundary.md)의 Android native 후속 증분

## 검토

- 검토자: Codex, 사람 검토 대기
- 실제 주장과 근거 일치 여부: local Android 11 emulator와 synthetic callback 범위에서 일치
- 검토 메모: Android 실기기·iPhone·현장·HTTP 완료로 확대하지 않는다.
