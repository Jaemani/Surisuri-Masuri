# Mac 작업 재개용 일회성 인수인계 — 2026-08-15

> 상태: `disposable handoff`  
> 독자: Mac에서 처음 저장소를 받는 개발자 또는 코딩 에이전트  
> 폐기 조건: Mac/iOS 최초 실행 결과를 EVD·UPD·CURRENT_STATUS에 반영하고 push한 뒤 이 파일을 삭제할 수 있음  
> 기준 코드 commit: `dc2548c`  
> 문서 무결성 정리 commit: `fc7db40`  

이 문서 하나를 먼저 읽고 작업을 시작한다. 영구적인 사실은 [CURRENT_STATUS](./CURRENT_STATUS.md), 반복 가능한 절차는 [Mac·iOS Runbook](../development/MAC_IOS_RUNBOOK.md)에 있다. 이 문서는 첫 Mac 세션의 순서와 종료 조건만 고정한다.

## 1. 이번 인수인계의 목적

첫 Mac 세션에서 다음 세 단계를 순서대로 확인한다.

1. clean checkout이 정적 검사와 iOS prebuild를 재현하는지 확인한다.
2. Simulator에서 이용자·수리사 모바일 제품 흐름이 정상 표시되는지 확인한다.
3. 실제 iPhone에서 설치, foreground GPS, SQLite 복구를 확인하고 background GPS는 별도 결과로 기록한다.

Firebase production 연결, EAS, TestFlight, field 성능, LLM 보고서 worker는 이번 인수인계 범위가 아니다.

## 2. 절대 지켜야 할 경계

- Git remote가 `github.com/Jaemani/Surisuri-Masuri.git`인지 확인한다.
- commit 전에 승인된 `Jaemani` Git identity인지 확인한다. 임시 계정이면 commit/push하지 않는다.
- 기존 `soo-ri`, `soo-ri-admin`, `power_assist_device_helper_backend` 코드를 가져오지 않는다. archive는 요구사항·DB 형식 참고용이다.
- `apps/mobile/ios`는 Expo prebuild 생성물이다. 직접 수정하지 않고 `app.json` 또는 config plugin으로 환원한다.
- static export 성공을 iPhone native 성공으로 기록하지 않는다.
- Simulator 결과를 카메라 QR·실제 GPS·background·배터리 결과로 사용하지 않는다.
- 원본 좌표, 이름, 전화번호, Firebase token, Apple signing 자료를 Git에 넣지 않는다.
- 기본 product repository는 deterministic `demo`다. Firebase 환경변수만으로 production 연결이 완성되지 않는다.
- EAS는 현재 설정되지 않았다. `eas.json`이나 project ID를 임의 생성하지 않는다.

## 3. 저장소 받기

### 새 clone

```bash
rtk git clone https://github.com/Jaemani/Surisuri-Masuri.git
cd Surisuri-Masuri/mobility-reliability-platform
```

### 기존 clone

먼저 로컬 변경을 확인한다. 사용자 변경이 있으면 덮어쓰거나 버리지 않는다.

```bash
cd Surisuri-Masuri/mobility-reliability-platform
rtk git status --short
rtk git fetch origin
rtk git pull --ff-only origin main
```

공통 확인:

```bash
rtk git remote -v
rtk git branch --show-current
rtk git log -3 --oneline
rtk git config user.name
rtk git config user.email
```

`main`, `origin`, `fc7db40` 이상이 보여야 한다. `../.serena/`는 로컬 도구 데이터이며 추가하지 않는다.

## 4. Mac toolchain 준비

필수 기준:

- Node.js 22
- Corepack
- pnpm 11.8.0
- Xcode와 Command Line Tools
- CocoaPods
- 실제 iPhone 사용 시 Xcode Apple account, Trust, Developer Mode

```bash
rtk proxy node --version
rtk proxy corepack enable
rtk proxy corepack prepare pnpm@11.8.0 --activate
rtk proxy pnpm --version
rtk proxy xcode-select -p
rtk proxy xcodebuild -version
rtk proxy pod --version
```

저장소 agent 명령 정책은 `rtk`를 요구한다. 새 Mac에 RTK가 없으면 프로젝트 소유자의 RTK bootstrap을 먼저 적용한다. 설치 방법은 현재 저장소 밖의 prerequisite이며 임의 바이너리를 설치하지 않는다.

## 5. 최초 정적 게이트

프로젝트 root가 아니라 반드시 `mobility-reliability-platform`에서 실행한다.

```bash
rtk pnpm install --frozen-lockfile
rtk pnpm check:docs
rtk pnpm --filter @mobility-reliability/mobile check
rtk pnpm --filter @mobility-reliability/mobile test
rtk pnpm --filter @mobility-reliability/mobile exec expo export --platform ios
```

기대 기준:

- 문서 링크·semantic integrity 통과
- mobile 258 tests 기준에서 회귀 없음
- iOS static export 성공

숫자가 바뀌면 무조건 실패는 아니지만 원인을 기록한다. 과거 EVD 수치를 최신 숫자로 덮어쓰지 않는다.

## 6. Simulator 확인

```bash
rtk pnpm --filter @mobility-reliability/mobile exec expo prebuild --clean --platform ios
rtk pnpm --filter @mobility-reliability/mobile exec expo run:ios
```

확인 순서:

- 이용자: 오늘 → 수리 요청 → 내 기기 → 완료 수리 이력 → 복지지원
- 수리사: 배정 작업 → 기기 확인 → 일정 선택 → 작업 시작 → 복수 수리항목 → 제출 전 검토
- 주행: 대기 → 시작 → 종료 → session summary
- 접근성: 큰 글씨, 최소 터치 영역, 화면 잘림

각 항목을 `PASS | FAIL | BLOCKED | NOT_APPLICABLE`로 남긴다. Simulator에서 QR camera와 실제 GPS는 `NOT_APPLICABLE`이다.

## 7. 실제 iPhone 확인

Xcode에서 승인된 Team과 automatic signing을 사용한다. 기본 bundle ID는 `com.jaemani.mobilityreliability.dev`다. 사용할 권한이 없으면 소유자에게 dev bundle ID 결정을 요청하고 임의 production ID를 만들지 않는다.

```bash
rtk pnpm --filter @mobility-reliability/mobile exec expo run:ios --device
```

최소 첫날 범위:

| ID | 시나리오 | 합격 기준 |
| --- | --- | --- |
| IOS-BOOT-01 | build·install·launch | 서명된 development build가 실제 iPhone에서 실행됨 |
| IOS-UI-01 | 이용자·수리사 핵심 flow | crash 없이 주요 화면과 상태 전이가 표시됨 |
| IOS-QR-01 | 공개코드 QR | 허용 payload만 처리하고 invalid/http payload를 거부함 |
| IOS-GPS-01 | foreground 10분 | 명시적 시작 뒤 sample이 저장되고 종료 가능함 |
| IOS-DB-01 | 앱 종료·재실행 | active/finished session과 sample count가 계약대로 복구됨 |
| IOS-BG-01 | 잠금/background 10분 | 권한·OS 상태와 함께 callback 결과를 관찰함 |
| IOS-PRIV-01 | 로그·화면 점검 | 원본 좌표·token·PII가 evidence에 노출되지 않음 |

iOS에서 사용자가 앱을 강제종료한 뒤 background 작업이 자동 재개될 것으로 기대하지 않는다. 단순 background, recent-app 제거, 강제종료를 별개 시나리오로 기록한다.

## 8. 실패 시 판단

| 실패 | 처리 |
| --- | --- |
| Xcode Team/App ID/signing | `BLOCKED`; 권한 요청. 임시 인증서·production ID 생성 금지 |
| CocoaPods/prebuild | toolchain과 오류를 보존하고 generated native 폴더를 직접 고치지 않음 |
| Metro 연결 | LAN·방화벽·기기 연결을 기록하고 사용한 transport를 명시 |
| Simulator UI 오류 | web snapshot과 비교하되 native 문제를 web 성공으로 덮지 않음 |
| iPhone GPS/background | 권한, Precise, Low Power Mode, 잠금, 강제종료 여부를 분리 |
| 개인정보 노출·반복 데이터 손실 | 즉시 중단하고 incident 기준 검토 |

## 9. 결과를 남기는 위치

- 원본 화면·영상·device log: `artifacts/device-smoke/` 또는 승인된 외부 저장소. Git 제외 상태 유지.
- 실제 결과 요약: 새 `EVD-YYYYMMDD-NNN`
- 제품 동작 변화가 있으면: 새 `UPD-YYYYMMDD-NN`
- 사람이 읽을 점검 보고가 필요하면: 새 `HR-YYYYMMDD-NN`
- 심각한 장애만: `INC-*`
- 최신 경계: [CURRENT_STATUS](./CURRENT_STATUS.md)

결과에는 다음을 반드시 넣는다.

```text
source commit / dirty state
macOS / Xcode / CocoaPods / Node / pnpm
iPhone model / iOS / build identifier
시작·종료 KST
각 명령과 exit result
시나리오별 PASS|FAIL|BLOCKED|NOT_APPLICABLE
권한·Low Power Mode·네트워크 조건
증명한 것 / 증명하지 못한 것
redacted artifact 위치와 SHA-256
reviewer / reviewed_at 또는 사람 검토 대기
```

## 10. 첫 커밋과 종료 조건

커밋 전:

```bash
rtk git status --short
rtk pnpm check:docs
rtk git diff --check
rtk git config user.name
rtk git config user.email
```

첫 권장 커밋은 결과에 따라 하나만 선택한다.

```text
test(ios): record initial simulator smoke
test(ios): record initial iPhone lifecycle smoke
fix(ios): correct native configuration from first device run
docs: record blocked Mac signing handoff
```

의미 있는 결과와 문서를 함께 push한다. 실제 성공을 확인하지 못했다면 `blocked` 보고만 남기고 성공 EVD를 만들지 않는다.

이 일회성 문서는 다음 조건을 모두 만족한 뒤 별도 commit으로 삭제할 수 있다.

- Mac toolchain과 첫 실행 결과가 EVD에 기록됨
- CURRENT_STATUS의 iOS 경계가 갱신됨
- 관련 UPD/HR 또는 blocked 사유가 연결됨
- 문서 검사 통과
- `origin/main` push 완료

