# 모바일 제품 Android·iPhone 실기기 smoke

## 목적과 완료 경계

이 문서는 수리사 제품 흐름의 QR camera, native 방문 일정, 복수 작업항목, 키보드·뒤로가기와 접근성을 실제 Android·iPhone에서 같은 순서로 검증한다. 체크박스와 결과는 실행자가 실제 기기를 조작한 뒤에만 채운다.

- source 기준: 실행 시 `git rev-parse HEAD`를 기록한다.
- 데이터: 합성 demo만 사용한다. 실제 사용자·기기 공개코드·수리정보를 촬영하거나 입력하지 않는다.
- 성공 의미: 기록한 OS·기기·build에서 해당 과업이 재현됐다는 뜻이다.
- 성공하지 않는 의미: production Firebase, 실제 수리사 업무, 모든 OS/OEM, WCAG 준수를 증명하지 않는다.

## 1. 공통 사전 기록

| 항목 | 실제 값 |
| --- | --- |
| 실행 일시 | 미입력 |
| 실행자 | 미입력 |
| source commit | 미입력 |
| 앱 build 식별자·SHA-256 | 미입력 |
| 플랫폼·OS version | 미입력 |
| 기기 model | 미입력 |
| 화면 크기·글자 크기 설정 | 미입력 |
| 접근성 서비스 | 미사용 / TalkBack / VoiceOver |

준비 확인:

- [ ] `git status --short`가 깨끗하거나, 미커밋 차이를 별도 기록했다.
- [ ] 합성 demo 역할 전환만 사용한다.
- [ ] QR에는 raw `MR-2208` 또는 `surisuri://device/MR-2208`만 사용한다.
- [ ] screenshot·영상에 실제 위치, 알림, 연락처나 다른 앱 개인정보가 보이지 않는다.

## 2. Android development build

WSL 저장소에서 Windows ADB로 보는 기본 경로는 [WSL Runbook](./WSL_RUNBOOK.md#android-development-client-빠른-데모)을 따른다. 날짜 선택기와 QR camera는 native dependency이므로 Expo Go가 아니라 dependency를 포함해 다시 만든 development build를 사용한다.

```bash
rtk pnpm install --frozen-lockfile
rtk pnpm --filter @mobility-reliability/mobile exec expo prebuild --clean --platform android
rtk proxy env ANDROID_HOME=/home/jaeman/Codes/android-sdk-linux \
  ANDROID_SDK_ROOT=/home/jaeman/Codes/android-sdk-linux \
  bash -lc 'cd apps/mobile/android && ./gradlew assembleDebug'
rtk proxy sha256sum apps/mobile/android/app/build/outputs/apk/debug/app-debug.apk
rtk proxy /mnt/c/Users/Jaeman/AppData/Local/Android/Sdk/platform-tools/adb.exe devices
rtk proxy /mnt/c/Users/Jaeman/AppData/Local/Android/Sdk/platform-tools/adb.exe \
  -s DEVICE_SERIAL install -r apps/mobile/android/app/build/outputs/apk/debug/app-debug.apk
rtk proxy /mnt/c/Users/Jaeman/AppData/Local/Android/Sdk/platform-tools/adb.exe \
  -s DEVICE_SERIAL reverse tcp:8081 tcp:8081
rtk pnpm --filter @mobility-reliability/mobile exec expo start --dev-client --localhost
```

`apps/mobile/android`는 생성물이며 Git에 커밋하지 않는다. 기존 실제 데이터를 지우는 uninstall은 자동으로 하지 않는다.

## 3. iPhone development build

WSL에서는 iOS native build를 검증할 수 없다. Mac의 clean checkout에서 같은 commit을 받은 뒤 Xcode 또는 EAS development build를 사용한다.

```bash
rtk git pull --ff-only origin main
rtk pnpm install --frozen-lockfile
rtk pnpm --filter @mobility-reliability/mobile exec expo prebuild --clean --platform ios
rtk pnpm --filter @mobility-reliability/mobile exec expo run:ios --device
```

Mac에 `rtk`가 없다면 먼저 RTK를 설치하고 이 저장소 지침을 유지한다. 다른 Mac에서 source를 고쳤다면 clean commit/push로 WSL과 맞추며 두 checkout을 동시에 수정하지 않는다.

## 4. 과업별 smoke

각 줄은 `PASS`, `FAIL`, `BLOCKED` 중 하나와 screenshot/video ID를 기록한다. 실패 시 증상, 정확한 재현 단계, 로그의 개인정보 제거 여부를 적고 완료 주장에 포함하지 않는다.

| ID | 과업 | 예상 결과 | Android 결과·증거 | iPhone 결과·증거 |
| --- | --- | --- | --- | --- |
| MP-01 | 합성 demo에서 설정·알림 → 개발용 역할 전환 | 수리사 `오늘의 작업` 표시 | 미실행 | 미실행 |
| MP-02 | 일정 필요 작업 열기 | 공개코드 확인 전 CTA 비활성 | 미실행 | 미실행 |
| MP-03 | QR 권한 거부 | 거부 설명과 수동 입력 fallback 유지 | 미실행 | 미실행 |
| MP-04 | QR 권한 허용 | QR 전용 camera 화면 표시 | 미실행 | 미실행 |
| MP-05 | 허용 raw code QR scan | arbitrary URL을 열지 않고 코드만 채움 | 미실행 | 미실행 |
| MP-06 | 잘못된·HTTP QR scan | 코드 일치 상태가 되지 않음 | 미실행 | 미실행 |
| MP-07 | 수동 `mr-2208` 입력 | 대소문자 정규화 후 기기 일치 | 미실행 | 미실행 |
| MP-08 | 방문 날짜 선택·취소 | 취소는 기존 값 유지, 선택은 요약 갱신 | 미실행 | 미실행 |
| MP-09 | 방문 시간 선택·취소 | 취소는 기존 값 유지, 선택은 요약 갱신 | 미실행 | 미실행 |
| MP-10 | 과거 또는 180일 초과 경계 | 제출 불가 또는 경계 안으로 제한 | 미실행 | 미실행 |
| MP-11 | 일정 확정 → 작업 시작 | projection 상태가 단계별 갱신 | 미실행 | 미실행 |
| MP-12 | 수리항목 2개 입력 | 독립 값과 실시간 합계 표시 | 미실행 | 미실행 |
| MP-13 | 항목 삭제·다시 추가 | 최소 한 항목 유지, 합계 정확 | 미실행 | 미실행 |
| MP-14 | 검토 → 수정 → 재검토 | 입력값 유지, 제출은 최종 CTA에서만 발생 | 미실행 | 미실행 |
| MP-15 | keyboard open 상태 scroll | 가려진 입력·CTA로 이동 가능 | 미실행 | 미실행 |
| MP-16 | system back·back gesture | native dialog/keyboard/화면이 예측 가능하게 닫힘 | 미실행 | 미실행 |
| MP-17 | TalkBack·VoiceOver 순서 | 항목 번호·label·checked·합계가 구분돼 읽힘 | 미실행 | 미실행 |
| MP-18 | 제출 직후 문구 | 완료가 아니라 `복지관 검증 대기` 표시 | 미실행 | 미실행 |

## 5. 증거 저장

실제 실행 뒤 아래처럼 별도 폴더를 만든다. 원본 영상·화면은 개인정보를 검토한 뒤 보존한다.

```text
artifacts/device-smoke/YYYY-MM-DD/<platform-device>/
  manifest.md
  01-role-switch.png
  02-qr-denied.png
  03-qr-success.png
  04-native-schedule.png
  05-multi-item-review.png
  06-verification-wait.png
```

`artifacts/`는 기본적으로 Git에 넣지 않는다. 사람이 공개 가능성을 확인한 작은 파생 screenshot만 `docs/evidence/`에서 명시적으로 링크한다. 결과를 기록할 때 [실기기 증거 템플릿](../evidence/templates/MOBILE_PRODUCT_DEVICE_SMOKE_RESULT.md)을 복사해 사용한다.

## 6. 실패 처리

- 앱 crash, 데이터 손실, 권한 우회 또는 잘못된 공적 비용 확정은 즉시 실행을 중단한다.
- Sev-1/Sev-2 조건이면 `docs/incidents/`에 기록한다.
- 그 외 개발 실패는 `DEVELOPMENT_FAILURE_LOG.md`에 증상·원인·수정·재검증을 남긴다.
- 실기기 하나가 통과해도 다른 플랫폼 결과를 추정하지 않는다.
