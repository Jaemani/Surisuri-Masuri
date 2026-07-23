# WSL2 개발·디버깅 Runbook

## 현재 확인된 환경

- WSL2 kernel: `6.6.114.1-microsoft-standard-WSL2`
- workspace: `/home/jaeman/Codes/Surisuri-Masuri` — Linux filesystem이므로 Metro 감시와 Node I/O에 적합
- Node.js: `v22.14.0`
- pnpm: `11.8.0`
- Java: OpenJDK 21 — Firebase Emulator 실행 가능
- Windows Android SDK: `C:\Users\Jaeman\AppData\Local\Android\Sdk`
- Windows platform-tools: 설치·ADB 연결 확인
- Windows emulator: `36.6.11.0`, WHPX 사용 가능
- Local AVD: `Surisuri_API_30` — Android 11 / Google Play / x86
- `adb.exe`: WSL PATH에는 없으므로 아래 절대 경로로 실행
- Go: 현재 WSL PATH에서 확인되지 않음

환경이 바뀔 때 다음을 실행한다.

```bash
rtk pnpm doctor
```

## 공통 원칙

1. 저장소를 `/mnt/c`로 옮기지 않는다. Windows filesystem은 Metro file watching, 권한, 대량 `node_modules` I/O에 불리하다.
2. 경로 대소문자를 엄격하게 다룬다. WSL에서는 통과하고 Windows/macOS checkout에서 충돌할 수 있는 파일명을 만들지 않는다.
3. Git 줄바꿈은 저장소 `.gitattributes`의 LF를 따른다.
4. WSL의 `localhost`, Windows host, Android/iPhone의 `localhost`는 서로 같은 주소가 아니다.
5. 원본 GPS와 실제 사용자 데이터는 디버그 로그, Metro console, Crashlytics에 출력하지 않는다.

## Android 에뮬레이터 빠른 데모

2026-07-23에 WSL 저장소, Windows emulator와 Expo Go 57 조합으로 현재
foreground vertical slice를 실제 실행했다. 저장소를 Windows로 복제하지 않는다.

### 1. AVD 실행

PowerShell에서 실행하거나 WSL에서 다음 명령을 호출한다.

```bash
rtk proxy powershell.exe -NoProfile -Command \
  "Start-Process -FilePath 'C:\Users\Jaeman\AppData\Local\Android\Sdk\emulator\emulator.exe' \
  -ArgumentList '-avd','Surisuri_API_30','-gpu','auto'"
```

부팅 확인:

```bash
rtk proxy /mnt/c/Users/Jaeman/AppData/Local/Android/Sdk/platform-tools/adb.exe \
  -s emulator-5554 shell getprop sys.boot_completed
```

결과가 `1`이어야 한다. ADB 목록에 emulator가 보이지 않지만 `5554/5555`가
listen 중이면 한 번 `adb.exe connect 127.0.0.1:5555`로 등록을 유도하고, 중복
transport가 생기면 `adb.exe disconnect 127.0.0.1:5555`로 정리한다.

### 2. Metro와 reverse 연결

WSL에서:

```bash
rtk pnpm --filter @mobility-reliability/mobile exec expo start --go --localhost

rtk proxy /mnt/c/Users/Jaeman/AppData/Local/Android/Sdk/platform-tools/adb.exe \
  -s emulator-5554 reverse tcp:8081 tcp:8081
```

Expo Go 57이 설치된 emulator에서 프로젝트 URL을 연다.

```bash
rtk proxy /mnt/c/Users/Jaeman/AppData/Local/Android/Sdk/platform-tools/adb.exe \
  -s emulator-5554 shell am start \
  -a android.intent.action.VIEW \
  -d 'exp://127.0.0.1:8081'
```

SDK 57용 Expo Go APK의 공식 release는
`https://github.com/expo/expo-go-releases/releases/download/Expo-Go-57.0.2/Expo-Go-57.0.2.apk`다.
다운로드한 파일은 설치 전 크기 `208277642` bytes인지 확인한다. 이 APK와 Expo
Go는 현재 foreground smoke에만 사용한다.

### 3. 현재 확인 가능한 데모

- 초기 SQLite open과 `기록 대기` 화면
- Android foreground 위치 권한 요청
- 합성 emulator 위치 sample의 SQLite append와 저장 수 증가
- `development_local_only`이므로 server upload 대기 수 0 유지
- Expo Go 강제종료 뒤 active session과 sample count 복구
- 복구된 session의 기록 재개 또는 명시적 종료

화면 근거는 [EVD-20260723-048](../evidence/2026-07.md#evd-20260723-048--android-foreground-gps와-sqlite-재시작-복구-smoke)에 있다.

### 이 데모가 증명하지 않는 것

- Expo Go는 background GPS, foreground service나 독립 native build를 증명하지 않는다.
- 현재 AVD는 Android 11 x86의 한 환경일 뿐 Android/iPhone 호환성 전체가 아니다.
- 합성 GPS는 실제 야외 정확도, 배터리, OEM process kill을 증명하지 않는다.
- Server-bound session, Firebase Auth/App Check, HTTP upload와 ACK는 아직 없다.
- Background 게이트부터는 Expo development build와 Android/iPhone 실기기를 사용한다.

## Android 실기기

### 권장 경로: Windows ADB + USB + reverse

1. Windows Android Studio 또는 Android platform-tools를 설치한다.
2. `adb.exe`가 WSL PATH에서 보이도록 Windows SDK platform-tools 경로를 추가하거나 절대 경로로 실행한다.
3. 실제 장치에서 USB debugging을 허용한다.
4. Metro와 로컬 emulator 포트를 장치로 reverse한다.

```bash
rtk adb.exe devices
rtk adb.exe reverse tcp:8081 tcp:8081
rtk adb.exe reverse tcp:9099 tcp:9099
rtk adb.exe reverse tcp:8080 tcp:8080
rtk adb.exe reverse tcp:9199 tcp:9199
rtk adb.exe reverse tcp:8085 tcp:8085
```

포트 의미:

- `8081`: Metro
- `9099`: Firebase Auth Emulator
- `8080`: Firestore Emulator
- `9199`: Storage Emulator
- `8085`: 로컬 telemetry gateway host port (`container/Cloud Run PORT=8080`)

Windows localhost가 WSL 서비스로 전달되지 않는 구성에서는 WSL mirrored networking 또는 Windows port proxy가 필요할 수 있다. 관리자 권한이 필요한 `netsh interface portproxy`는 자동 실행하지 않고 필요 시 별도 승인·기록한다.

### Wi-Fi 실기기

- 장치와 Windows가 같은 네트워크여도 WSL2 NAT 주소는 장치에서 직접 접근되지 않을 수 있다.
- 먼저 Expo tunnel 또는 WSL mirrored networking을 사용한다.
- 방화벽을 무작정 해제하지 않는다. 필요한 포트와 프로세스만 허용한다.

## iPhone 실기기

- WSL에서는 iOS native build와 Simulator를 실행할 수 없다.
- foreground 위치의 JS 개발은 Expo 흐름으로 진행할 수 있지만 background location 검증에는 Expo Go가 아니라 development build가 필요하다.
- EAS development build를 생성해 실제 iPhone에 설치하고, Metro 연결은 동일 LAN 또는 tunnel을 사용한다.
- `Allow Once`와 `While Using the App`은 앱에서 구분할 수 없으므로 권한 UX와 테스트 결과에 이 제한을 기록한다.
- background 권한은 foreground 권한과 분리해 후속 게이트에서 요청한다.

## Firebase Emulator

- Java 21이 있으므로 WSL 내부에서 Emulator Suite를 실행한다.
- Android USB 디버깅은 `adb reverse`로 WSL emulator에 접근한다.
- iPhone이 로컬 emulator에 접근해야 할 때는 WSL/Windows의 실제 접근 가능한 주소와 Windows 방화벽을 확인한다.
- emulator 연결이 불가능하다는 이유로 production Firebase 프로젝트를 개발 기본값으로 사용하지 않는다.
- `.firebaserc.example`의 demo project ID를 사용하고 실제 service account key를 저장소에 넣지 않는다.
- `pnpm check`와 `pnpm test`는 둘 다 Firebase Emulator Suite를 시작하므로 같은 workspace에서 병렬 실행하지 않는다. 병렬 실행은 hub `4400`, Firestore `8080`, websocket `9150` 충돌과 orphan Java process를 만들 수 있다.
- 테스트 실패 후 `8080`이 계속 점유되면 먼저 `rtk proxy ss -ltnp 'sport = :8080'`과 process command line으로 해당 project의 orphan emulator인지 확인한다. 확인되지 않은 Java process를 일괄 종료하지 않는다.

host Go가 없는 WSL에서는 Firestore transaction 통합 테스트를 Emulator가 살아 있는 동안 host-network Docker Go로 실행한다. 이 명령도 다른 Firebase test와 병렬 실행하지 않는다.

```bash
rtk pnpm --filter @mobility-reliability/firebase-rules exec firebase emulators:exec \
  --config ../../firebase.json \
  --project demo-mobility-reliability \
  --only firestore \
  "rtk docker run --rm --network host \
    -e FIRESTORE_EMULATOR_HOST=127.0.0.1:8080 \
    -v mobility-go-mod-cache:/go/pkg/mod \
    -v mobility-go-build-cache:/root/.cache/go-build \
    -v /home/jaeman/Codes/Surisuri-Masuri/mobility-reliability-platform:/workspace:ro \
    -w /workspace/services/telemetry-gateway \
    golang:1.26.5-bookworm \
    go test -mod=readonly -count=1 -race -timeout 60s ./internal/firebaseadapter \
      -run FirestoreAdmissionStoreEmulator -v"
```

이 검사는 demo project와 synthetic control document만 사용한다. `--network host`는 WSL localhost의 test emulator에 접근하기 위한 local 설정이며 production container network나 IAM 구성을 의미하지 않는다.

## Cloud Storage artifact integration

`DoesNotExist`, exact generation attrs와 `ReadCompressed(true)` 동작은 작은 in-memory fake만으로 증명하지 않는다. Cloud Storage Go client가 사용하는 official testbench image를 digest로 고정해 실행한다.

```bash
rtk docker run --rm -d \
  --name mobility-storage-testbench \
  --network host \
  gcr.io/cloud-devrel-public-resources/storage-testbench@sha256:600fa5c3cfc8be26435c38591cc094fb4ef648f760ffabf77f93237b1ebee027

rtk curl --retry 5 --retry-connrefused --silent --fail \
  http://127.0.0.1:9000

rtk docker run --rm --network host \
  -e STORAGE_EMULATOR_HOST=http://127.0.0.1:9000 \
  -v mobility-go-mod-cache:/go/pkg/mod \
  -v mobility-go-build-cache:/root/.cache/go-build \
  -v /home/jaeman/Codes/Surisuri-Masuri/mobility-reliability-platform:/workspace:ro \
  -w /workspace/services/telemetry-gateway \
  golang:1.26.5-bookworm \
  go test -mod=readonly -count=1 -race -timeout 60s \
    ./internal/gcsadapter -run ArtifactStoreEmulator -v

rtk docker rm -f mobility-storage-testbench
```

testbench는 synthetic bucket과 payload만 사용한다. 실행 뒤 마지막 명령으로 임시 container를 제거한다. 이 검사는 production bucket의 IAM, lifecycle, retention, KMS나 실제 사용자 위치 데이터 저장을 증명하지 않는다.

## GPS 디버깅 체크리스트

- [ ] 실기기 시각과 WSL 시각의 차이를 기록했다.
- [ ] 권한 상태와 `canAskAgain`을 좌표와 분리해 기록했다.
- [ ] 앱에 좌표 원문을 표시하거나 console에 출력하지 않는다.
- [ ] Android OEM의 background kill 동작을 장치명·OS 버전과 함께 기록한다.
- [ ] iPhone의 `Allow Once` 제한을 테스트 결과에 명시한다.
- [ ] 앱 background·강제종료·재시작을 서로 다른 시나리오로 측정한다.
- [ ] 합성 위치와 실제 야외 주행을 별도 evidence ID로 기록한다.

## 문제별 빠른 진단

| 증상 | 우선 확인 |
| --- | --- |
| Metro가 장치에서 열리지 않음 | `adb reverse`, tunnel, Windows 방화벽, Metro listen address |
| Emulator 접속 거부 | 장치에서 `localhost`를 썼는지, reverse 포트, emulator 실행 host |
| 변경 감지가 느림 | workspace가 `/mnt/c`인지, watcher 수, WSL 메모리 |
| iOS background task 미실행 | Expo Go 사용 여부, development build, iOS background mode, Always 권한 |
| Android에서 recent apps 제거 후 중단 | OEM kill policy, foreground service, `dontkillmyapp` 유형 기록 |
| GPS 값이 없음 | 시스템 위치 서비스, foreground 권한, 실내 accuracy, device/emulator 여부 |
