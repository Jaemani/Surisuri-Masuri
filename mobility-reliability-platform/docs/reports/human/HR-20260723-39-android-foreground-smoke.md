---
id: HR-20260723-39
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M3
technical_gate: Android foreground native smoke
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Android 데모와 화면 테스트 게이트

## 한눈에 보기

- 계획상 위치: 5~6월 foreground GPS·local persistence의 Android native 보완 검증.
- 실제 결과: React Native 앱을 Windows Android 11 emulator에서 실행해 권한,
  합성 GPS 4건 저장과 process 재시작 복구를 화면으로 확인했다.
- 가장 중요한 차이: 화면과 foreground SQLite는 확인됐지만 background GPS,
  실제 업로드·ACK와 지도는 아직 구현·검증되지 않았다.
- 필요한 사람 확인: 현재 화면의 문구·크기·색상과 iPhone development build를
  시작할 Mac 이관 시점을 프로젝트 소유자가 추후 검토한다.

## 8개월 로드맵 대비 현재 위치

| 월 | 화면·기술 게이트 | 보고 시점 실제 상태 |
| --- | --- | --- |
| 5월 | 앱 shell·foreground GPS vertical slice | Android native 화면·권한·GPS 저장 검증됨 |
| 6월 | Background GPS·offline lifecycle | Foreground process restart 복구만 검증. Background 미구현 |
| 7월 | Auth·upload·ACK·정제 경로·지도 | Wire/ledger/materializer local 구현. HTTP·ACK·지도 미완료 |
| 8월 | 라벨링·PyTorch·ONNX 화면 | 미착수 |
| 9월 | Digital Twin·위험곡선·abstain UI | 미착수 |
| 10월 | 근거형 보고서·수리사 feedback | 미착수 |
| 11월 | 기관 콘솔·QR·접근성·운영 화면 | 미착수 |
| 12월 | 종단간 데모·발표 freeze | 미착수 |

## 실제 확인 결과

| 항목 | 상태 | 확인된 결과 | 검증 환경 |
| --- | --- | --- | --- |
| React Native 화면 | `검증됨` | 초기·기록 중·복구 상태 렌더링 | Android 11 emulator |
| 위치 권한 | `검증됨` | Foreground permission dialog와 granted 상태 | Android 11 emulator |
| GPS→SQLite | `검증됨` | Synthetic sample 4건 저장, 제외 0건 | Expo native SQLite/location |
| Process restart | `검증됨` | Force-stop 뒤 active session·4건 복구 | Expo Go process |
| 원본 좌표 비노출 | `검증됨` | 보관 screenshot·Metro output에 좌표 없음 | Local evidence review |
| Background·offline HTTP | `미착수` | Expo development build·transport 필요 | 후속 gate |

## 근거

- [EVD-20260723-048](../../evidence/2026-07.md#evd-20260723-048--android-foreground-gps와-sqlite-재시작-복구-smoke)
- [UPD-20260723-14](../../product-updates/UPD-20260723-14-android-foreground-smoke.md)
- 재현 절차: [WSL Runbook](../../development/WSL_RUNBOOK.md#android-에뮬레이터-빠른-데모)
- Source baseline: `3a1a6ba`; 이 검증을 위해 앱 source를 변경하지 않았다.

## React를 사용하지 않은 것이 아니다

현재 모바일은 React 19.2.3, React Native 0.86.0, Expo SDK 57과 TypeScript를
사용한다. Flutter는 사용하지 않았고 Android·iOS를 따로 두 번 구현하지 않았다.
현재는 Expo managed 공통 코드베이스이며 직접 작성한 Kotlin/Swift module은 없다.

이 선택은 기존 React/TypeScript 경험을 재사용해 새 학습 가치를 UI framework
반복보다 mobile lifecycle, native telemetry, offline sync, geospatial pipeline과
on-device ML에 집중하기 위한 것이다. Mac은 아래 시점에 필요하다.

1. iPhone development build와 background location mode 검증
2. Swift native module이 필요한 적응형 sampling 또는 lifecycle bridge 구현
3. ONNX iPhone latency·memory·battery 비교
4. App Store/TestFlight signing과 현장 배포 준비

## 화면 테스트 시점

1. **지금 완료:** Android foreground UI·권한·SQLite·재시작 smoke.
2. **6월 보완 게이트:** Android/iPhone development build에서 잠금, background,
   앱 전환, process 종료와 offline local persistence.
3. **7월 sync 게이트:** 로그인·동의·기기 배정, 실제 upload/ACK 상태,
   offline→reconnect→202→replay 200과 정제·마스킹 지도.
4. **8월 ML 게이트:** 라벨링·주행 판별·data insufficient/fallback UI와
   Android/iPhone ONNX latency.
5. **9월 Twin 게이트:** 기기/부품 timeline, 위험곡선, confidence·abstain UI.
6. **10월 AI 게이트:** 위험 설명, 수리사 feedback, Fact ID 근거 열기와 LLM fallback.
7. **11월 운영 게이트:** 기관 콘솔, QR, tenant 격리, TalkBack·VoiceOver,
   monitoring·incident 화면.
8. **12월 발표 게이트:** 5분 종단간 시나리오, 새 장치 회귀, 발표 화면 freeze.

8~12월 화면 prototype은 synthetic data로 먼저 병행할 수 있다. 다만 6~7월
lifecycle·Auth·HTTP·ACK가 연결되기 전에는 종단간 완료로 보고하지 않는다.

## 다음 회차

1. Stored body의 SHA-256을 lease 직전에 다시 계산하고 corruption을 fail-closed한다.
2. Retry와 exact ACK의 batch/outbox transaction을 구현한다.
3. Firebase Auth/App Check와 server-managed session scope를 연결한다.
4. Android offline→reconnect server E2E 뒤 iPhone development build로 이관한다.
5. 그 다음 정제 경로·지도·H3 화면을 연결해 7월 visual gate를 닫는다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 참석자·사진·지출: 해당 없음
- 이 문서는 실제 개발환경 검증 요청 리포트이며 회의록이 아니다.

## 발행 전 검토

- [x] 계획과 실제 구현을 분리했다.
- [x] Android emulator와 Android/iPhone 실제 장치를 구분했다.
- [x] Synthetic GPS를 field·ML 학습 데이터로 표현하지 않았다.
- [x] Background·HTTP·지도·8~12월 기능을 완료로 표현하지 않았다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 화면과 주장 범위를 검토했다.
