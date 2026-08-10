# 개발 검증 실패 기록

이 문서는 배포 전 local·test 검증에서 발견한 **기술적으로 중대한 실패**를 숨기지 않고
추적한다. 실제 사용자·기관·staging·production 영향이 있는 사건은 이 문서가 아니라
[`incidents/`](../incidents/) 정책에 따라 별도 인시던트로 기록한다.

아래 세 항목은 2026-08-11 배포 전 local review에서 발견해 같은 코드 증분에서
해결한 경계 결함이다. 모두 WSL2의 local Node SQLite/component test 범위에서만
관찰되었고 실제 사용자·기관·staging·production 데이터에는 영향이 없었다. 따라서
`INC-*` 인시던트가 아니며, 이 문서에서는 기존 Android native smoke와 분리해 기록한다.

## DEVFAIL-20260811-01 — bounded LIMIT 100 scan의 actionable batch starvation

- 상태: `resolved-local-review`
- 발견일·해결일: 2026-08-11
- 환경·데이터: WSL2 local Node SQLite / synthetic test fixture
- 실제 사용자·기관 영향: 없음
- 인시던트 분류: 비해당. 배포 전 component review에서만 확인됐고 데이터 mutation이나
  외부 요청이 없었다.

### 문제

lease 후보를 생성시각 순으로 최대 100건만 읽을 때, 앞의 100건이 유효한 future
retry 또는 아직 살아 있는 lease라면 101번째 이후의 due batch가 계속 가려질 수 있었다.
기존 100건 안의 poisoned row를 먼저 hold하는 bounded integrity scan 의도는 유지해야
했다.

### 정정·검증

- 첫 100건은 기존대로 canonical timestamp·metadata·integrity·CAS 검사를 수행한다.
- 첫 window에 actionable 후보가 없을 때만 `due_upload_batch` prefilter에서 가장 오래된
  due 후보 하나를 조회하고, 동일 canonical 검사와 lease CAS를 다시 통과시킨다.
- 100건의 future retry 뒤 101번째 due batch를 lease하는 회귀 테스트를 추가했다.
- SQL due 조건은 후보 탐색 최적화일 뿐 권위가 아니며 malformed metadata는 기존 fail-closed
  hold 경로를 따른다.

## DEVFAIL-20260811-02 — BEGIN/COMMIT response uncertainty 처리 누락

- 상태: `resolved-local-review`
- 발견일·해결일: 2026-08-11
- 환경·데이터: WSL2 local fake native connection / synthetic transaction response loss
- 실제 사용자·기관 영향: 없음
- 인시던트 분류: 비해당. 실제 native DB나 서버에 배포되지 않은 persistence 경계
  review였고 외부 데이터 변경은 없었다.

### 문제

native SQLite promise가 `BEGIN IMMEDIATE` 이후 또는 `COMMIT` 직후 reject되면 promise
결과만으로 transaction이 열렸는지·commit됐는지 판단할 수 없다. 이를 일반 오류로만
처리하면 rollback 또는 mutation 재시도로 상태를 더 손상시킬 수 있었다.

### 정정·검증

- BEGIN await 전에 `transactionMayBeOpen`을 세우고, BEGIN reject에도 compensating
  rollback을 시도한다. rollback과 close까지 실패하면 disposition은
  `UPLOAD_DISPOSITION_ROLLBACK_FAILED`, lease는 `UPLOAD_LEASE_ROLLBACK_FAILED`로
  명시적으로 닫는다.
- COMMIT reject 뒤에는 rollback이나 mutation 재호출을 하지 않는다. writer를 닫고 새
  query-only connection에서 `committed`·`not_committed`·`unverifiable`을 상관한다.
- `committed`만 성공으로 반환하며 writer·reader close 경고를 operational warning으로
  분리한다. 나머지는 disposition의 `UPLOAD_DISPOSITION_NOT_COMMITTED` 또는
  `UPLOAD_DISPOSITION_COMMIT_UNVERIFIABLE`, lease의
  `UPLOAD_LEASE_COMMIT_UNVERIFIABLE`로 fail-closed 처리한다.
- begin-open/rollback response loss, commit response loss와 fresh-read 결과를 포함한
  coordinator 회귀 테스트를 추가했다.

## DEVFAIL-20260811-03 — fractional position·malformed nested JSON audit 누락

- 상태: `resolved-local-review`
- 발견일·해결일: 2026-08-11
- 환경·데이터: WSL2 local SQLite v3→v4 migration / synthetic corrupt fixtures
- 실제 사용자·기관 영향: 없음
- 인시던트 분류: 비해당. migration 전 audit fixture에서만 발견됐고 실제 DB를 자동
  수정·삭제하지 않았다.

### 문제

기존 terminal batch의 item 수만 확인하면 position이 fractional이거나 0부터
`sample_count - 1`까지 연속되지 않는 binding을 통과시킬 수 있었다. 또한 malformed
nested sample JSON을 audit query가 직접 확장하면 audit 자체가 예외를 내어 원본 값이
오류 메시지로 노출될 수 있었다.

### 정정·검증

- v4 migration audit와 terminal cardinality trigger가 `COUNT`, `MIN=0`,
  `MAX=sample_count-1`, 모든 position의 `typeof=integer`를 함께 확인한다.
- malformed top-level JSON은 안전한 `{}` 대체값으로 검사해 `DATABASE_UPLOAD_BATCH_BODY_INVALID`
  로만 거부하고 payload를 오류에 포함하지 않는다. nested sample은 object 여부와
  canonical field count를 별도 확인한다.
- position gap/out-of-range/fractional, leased fractional position, malformed nested
  sample fixture의 migration 거부 회귀 테스트를 추가했다.

### 관련 기록

- 결정: [ADR-0039](../decisions/ADR-0039-atomic-mobile-upload-disposition.md),
  [ADR-0036](../decisions/ADR-0036-fail-closed-mobile-upload-lease.md)
- 제품 업데이트: [UPD-20260811-01](../product-updates/UPD-20260811-01-mobile-upload-disposition.md)
- 증거: [EVD-20260811-001](../evidence/2026-08.md#evd-20260811-001--모바일-upload-disposition과-v4-state-integrity)
- 사람 대상 리포트: [HR-20260811-01](../reports/human/HR-20260811-01-mobile-upload-disposition.md)

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
