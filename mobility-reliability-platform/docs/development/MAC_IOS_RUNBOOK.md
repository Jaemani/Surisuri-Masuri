# Mac·iOS 실행 및 인수인계 Runbook

## 문서 상태

- 기준일/commit: 2026-08-14 / `dc2548c`
- 검증됨: WSL 회귀, Android/iOS Expo static export
- 미검증: clean Mac의 Xcode build, Simulator 설치, 실제 iPhone 설치·GPS lifecycle·배터리
- 현재 지원 경로: local Xcode. EAS는 `eas.json`, project ID, credentials 절차가 없어 지원 경로가 아니다.

## 지금 확인할 수 있는 것

Mac에서 성공적으로 빌드되면 deterministic demo 제품 UI와 local SQLite/GPS 후보를 확인할 수 있다. Firebase environment 값을 넣는 것만으로 production Auth/App Check와 제품 repository가 연결되지는 않는다. 기본 repository source는 `demo`다.

## 1. 저장소와 신원 확인

```bash
rtk git clone https://github.com/Jaemani/Surisuri-Masuri.git
cd Surisuri-Masuri/mobility-reliability-platform
rtk git remote -v
rtk git config user.name
rtk git config user.email
rtk git status --short
```

원격 소유자가 `Jaemani`인지 확인한다. Git author도 프로젝트 소유자의 승인된 로컬 identity여야 하며 임시 계정이면 commit/push하지 않는다. identity 값 자체를 문서에 복제하지 않는다.

`rtk`는 저장소의 agent 명령 정책상 필수지만 설치 bootstrap은 저장소에 포함되어 있지 않다. `rtk --version`이 실패하면 조직/소유자가 제공하는 RTK 설치 절차를 먼저 받아야 한다. 이는 현재 외부 prerequisite다.

## 2. Toolchain 준비

- Node.js 22
- Corepack과 pnpm 11.8.0
- 최신 프로젝트 호환 Xcode와 Command Line Tools
- Xcode license/first launch 완료
- CocoaPods 사용 가능
- 실제 iPhone은 Trust, Developer Mode, 잠금 해제 상태

```bash
rtk proxy node --version
rtk proxy corepack enable
rtk proxy pnpm --version
rtk proxy xcode-select -p
rtk proxy xcodebuild -version
rtk proxy pod --version
```

검증된 macOS/Xcode/CocoaPods 버전 조합은 아직 없다. 최초 성공 시 결과 manifest에 버전을 기록한다.

## 3. 정적 게이트

```bash
rtk pnpm install --frozen-lockfile
rtk pnpm --filter @mobility-reliability/mobile check
rtk pnpm --filter @mobility-reliability/mobile test
rtk pnpm --filter @mobility-reliability/mobile exec expo export --platform ios
rtk pnpm check:docs
```

## 4. Simulator와 실제 iPhone 분리

Simulator는 UI·상태 전이·일정 picker 확인용이다. 카메라 QR, 실제 GPS, background lifecycle, 배터리 측정은 실제 iPhone에서만 판정한다.

```bash
rtk pnpm --filter @mobility-reliability/mobile exec expo prebuild --clean --platform ios
rtk pnpm --filter @mobility-reliability/mobile exec expo run:ios
rtk pnpm --filter @mobility-reliability/mobile exec expo run:ios --device
```

`apps/mobile/ios`는 생성물이며 직접 수정하지 않는다. 변경이 필요하면 `app.json` 또는 config plugin에 반영하고 `prebuild --clean`으로 재생성한다.

실제 iPhone에서는 Xcode automatic signing과 승인된 Apple Team이 필요하다. 기본 bundle ID `com.jaemani.mobilityreliability.dev`를 사용할 권한이 없으면 임의로 production ID를 만들지 말고 소유자와 dev bundle-ID 정책을 확정한다.

## 5. 생성된 iOS 설정 확인

prebuild 후 Xcode 프로젝트에서 다음을 확인한다.

- `UIBackgroundModes`에 `location`
- When In Use/Always 위치 권한 설명
- camera 권한 설명
- 사용하지 않는 microphone 권한과 `NSAllowsArbitraryLoads`가 최소화됐는지
- SQLite 파일 보호·backup exclusion은 아직 별도 실기기 검증 필요

## 6. 제품 smoke와 telemetry smoke

제품 MP-01~MP-18은 [공통 device smoke](./MOBILE_PRODUCT_DEVICE_SMOKE.md)를 따른다. iPhone telemetry는 각 항목을 개별 `PASS | FAIL | BLOCKED`로 남긴다.

| ID | 실제 iPhone 전용 확인 |
| --- | --- |
| TG-01 | 위치 권한 거부→When In Use→Always 전이와 Precise off |
| TG-02 | foreground 10분 수집, timestamp 단조 증가와 앱 재시작 복구 |
| TG-03 | 잠금/background 10분 callback과 foreground service 동작 |
| TG-04 | recent-app 제거·강제종료·재실행. iOS 강제종료 후 자동재개를 기대하지 않음 |
| TG-05 | 비행기 모드 수집→재연결 시 SQLite 보존·중복 없는 처리 |
| TG-06 | v1→v4 SQLite migration 및 malformed row fail-closed |
| TG-07 | 30분 샘플링 cadence·배터리 변화. 기간·기기·OS·표본을 함께 기록 |
| TG-08 | 종료 뒤 원본 좌표·token이 UI/console log/evidence에 노출되지 않음 |

production HTTP upload/ACK가 연결되지 않았으므로 TG-05는 현재 local queue 경계까지만 판정한다.

## 7. 증거와 개인정보

- source commit, dirty state, 시작/종료 KST, Mac/Xcode/iOS/device model, 권한 상태, 각 명령과 결과를 기록한다.
- 화면·영상·device log는 원본 좌표, 이름, token을 redaction한다.
- 외부 artifact는 SHA-256과 접근권한·보존기간을 manifest에 남긴다.
- `artifacts/device-smoke/`는 Git에 넣지 않는다.
- 최초 성공은 새 EVD로 기록하며 static export를 native/iPhone 성공 근거로 재사용하지 않는다.

## 8. 실패 처리

- signing/Team/App ID 실패: `BLOCKED`, 사용자 권한 필요. 임의 인증서나 production ID를 만들지 않는다.
- Pods/DerivedData 오류: 오류 로그와 toolchain을 먼저 보존한 뒤 clean prebuild를 재시도한다.
- Metro 연결 실패: Mac과 기기의 LAN·방화벽을 확인한다. tunnel을 쓰면 사용 여부를 기록한다.
- background callback 실패: 앱 강제종료 여부, 권한, Low Power Mode, OS 버전을 분리한다.
- 개인정보 노출 또는 반복적 데이터 손실이면 개발 실패 로그가 아니라 incident 기준을 먼저 검토한다.

완료 후 [현재 상태](../handoffs/CURRENT_STATUS.md), 새 EVD, 관련 UPD/HR을 실제 결과만으로 갱신한다.
