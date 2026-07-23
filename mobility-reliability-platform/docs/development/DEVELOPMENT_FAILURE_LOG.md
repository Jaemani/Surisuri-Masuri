# 개발 검증 실패 기록

이 문서는 배포 전 local·test 검증에서 발견한 **기술적으로 중대한 실패**를 숨기지 않고
추적한다. 실제 사용자·기관·staging·production 영향이 있는 사건은 이 문서가 아니라
[`incidents/`](../incidents/) 정책에 따라 별도 인시던트로 기록한다.

## DEVFAIL-20260723-01 — Android background job crash와 false-recording state

- 상태: `resolved-local`
- 발견일: 2026-07-23
- 환경: WSL2 source + Windows Android 11 x86 emulator + Expo development client
- 데이터: synthetic location only
- 실제 사용자·기관 영향: 없음
- 인시던트 분류: 비해당. 배포 전 local package에만 존재했고 사용자 데이터 수정이 없음

### 실패 1: Persisted job permission 누락

- 증상: 첫 native background callback에서 process가 종료됐다.
- 관측 오류: `requested job be persisted without holding RECEIVE_BOOT_COMPLETED permission`
- 원인: Expo location service가 persisted Android job을 요청했지만 generated manifest에
  `android.permission.RECEIVE_BOOT_COMPLETED`가 없었다.
- 정정: `app.json`의 Android permission에 값을 추가하고 native project·APK를 다시
  생성했다.
- 검증 주의: 권한 없는 APK 위에 replace install한 emulator에서는 기존 package state로
  crash가 반복됐다. 실제 데이터가 없는 development package를 uninstall한 뒤 clean
  install하고 package permission `granted=true`를 확인했다.
- 남은 위험: Store upgrade·기존 사용자 DB가 있는 in-place update 경로는 검증하지 않았다.

### 실패 2: Cold launch 뒤 거짓 `recording` 상태

- 증상: Active session에서 process force-stop 후 앱을 열면 UI는 `주행 기록 중`이지만
  `dumpsys activity services`에 실제 foreground `LocationTaskService`가 없었다.
- 원인: Persisted Expo task registration 상태를 현재 native service의 liveness로 간주했다.
- 정정: 첫 app initialization에서만 active session·permission·failure marker를 확인한 뒤
  task를 stop/re-register하도록 했다. 재등록 실패는 `ready_to_resume/capture_failed`로
  닫는다.
- 회귀 검증: Force-stop→launcher 진입 후 service·notification 재생성, 홈 화면 합성
  callback count 증가, 명시적 종료 후 notification 제거를 확인했다.

### 관련 기록

- 결정: [ADR-0038](../decisions/ADR-0038-cold-launch-background-service-reconciliation.md)
- 제품 업데이트: [UPD-20260723-17](../product-updates/UPD-20260723-17-android-background-native-smoke.md)
- 증거: [EVD-20260723-051](../evidence/2026-07.md#evd-20260723-051--android-background-gps-native-lifecycle-smoke와-cold-launch-복구)
- 사람 대상 리포트: [HR-20260723-42](../reports/human/HR-20260723-42-android-background-native-smoke.md)
