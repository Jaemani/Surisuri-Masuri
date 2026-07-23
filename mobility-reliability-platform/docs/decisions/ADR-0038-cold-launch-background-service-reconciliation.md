# ADR-0038: Cold launch에서 백그라운드 위치 서비스를 재조정한다

- 상태: accepted
- 결정일: 2026-07-23
- 관련 결정: [ADR-0037](./ADR-0037-fail-closed-background-gps-capture.md)

## 맥락

Android development build의 native smoke에서 활성 주행 중 앱 process를 강제 종료한 뒤
다시 열면 SQLite의 active session과 Expo task registration은 남았지만 실제
`LocationTaskService`와 foreground-service notification은 존재하지 않는 상태를
관찰했다. `Location.hasStartedLocationUpdatesAsync`가 등록 상태를 반환하는 것만으로는
현재 process의 native service가 실제로 다시 실행 중이라고 증명할 수 없었다.

화면은 등록 상태를 근거로 `주행 기록 중`을 표시했으므로, 그대로 두면 사용자는 계속
수집 중이라고 믿지만 화면 밖 callback은 받지 못하는 silent liveness failure가 된다.
또한 persisted Android job을 요청하는 Expo location service에는
`android.permission.RECEIVE_BOOT_COMPLETED`가 manifest에 있어야 한다. 이 권한 없이
설치된 첫 development APK에서는 background callback 시 JobScheduler가 process를
종료했다.

## 검토한 선택지

1. Process 재시작 뒤 항상 `ready_to_resume`으로 두고 사용자가 수동 재개하게 한다.
2. Expo가 반환하는 등록 상태를 실제 실행 상태로 간주한다.
3. Cold launch에서만 active session·background 권한·failure marker를 다시 확인하고,
   기존 task registration을 stop 후 동일 옵션으로 명시적으로 재등록한다.

선택지 1은 안전하지만 앱을 다시 여는 것만으로 매번 수집이 중단되고, 선택지 2는 이번
smoke에서 실제 service와 불일치했다. 사용자에게 거짓 실행 상태를 보여주지 않으면서
foreground에서 합법적으로 service를 복구할 수 있는 선택지 3을 채택한다.

## 결정

- 앱 초기화의 첫 runtime reconciliation에서만 cold-launch recovery를 수행한다.
- 다음 조건을 모두 만족할 때 persisted background task를 stop하고 같은 native 옵션으로
  직접 다시 등록한다.
  - 종료되지 않은 active trip session이 있다.
  - background 위치 권한이 현재도 `granted`다.
  - durable background-task failure marker가 없다.
  - Expo가 task registration을 시작된 상태로 보고한다.
- 일반적인 foreground↔background AppState 전환마다 service를 재시작하지 않는다.
- 재등록에 실패하면 `recording`으로 표시하지 않고 `capture_failed`와 함께
  `ready_to_resume` 경계로 닫는다.
- Android manifest에는 persisted job이 요구하는
  `android.permission.RECEIVE_BOOT_COMPLETED`를 명시한다.
- 이전 APK가 해당 권한 없이 설치된 local emulator는 clean uninstall/install로
  검증한다. 이 local 정정은 아직 실제 앱 upgrade 호환성을 증명하지 않는다.

## 결과와 한계

- Android 11 emulator에서 process force-stop 뒤 앱을 다시 열면 foreground location
  service와 notification이 재생성되고 합성 callback이 SQLite active session에 이어
  저장되는 것을 확인했다.
- 명시적 주행 종료 뒤 service는 `startRequested=false`·non-foreground가 되고 위치
  notification은 사라졌다.
- `RECEIVE_BOOT_COMPLETED`는 package가 강제 종료된 상태에서 앱을 몰래 재가동하거나
  기기 재부팅 뒤 사용자 동의 없이 새 세션을 만드는 권한으로 사용하지 않는다.
- Android emulator 한 환경의 결과이며 OEM별 kill policy, 기기 재부팅, swipe-away,
  장시간 잠금, 실제 배터리와 Android 실기기를 아직 증명하지 않는다.
- iOS는 이 Android lifecycle 결정을 그대로 증거로 사용할 수 없다. Mac/Xcode
  development build에서 별도 검증한다.

## 관련 기록

- 제품 업데이트: [UPD-20260723-17](../product-updates/UPD-20260723-17-android-background-native-smoke.md)
- 증거: [EVD-20260723-051](../evidence/2026-07.md#evd-20260723-051--android-background-gps-native-lifecycle-smoke와-cold-launch-복구)
- 사람 대상 리포트: [HR-20260723-42](../reports/human/HR-20260723-42-android-background-native-smoke.md)
- 개발 실패 기록: [DEVFAIL-20260723-01](../development/DEVELOPMENT_FAILURE_LOG.md#devfail-20260723-01--android-background-job-crash와-false-recording-state)
- 인시던트: 해당 없음 — local emulator·synthetic data에서 배포 전 발견했고 사용자·기관·현장 데이터 영향이 없음
