---
id: HR-20260723-42
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M2
technical_gate: Android background GPS native lifecycle
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: Android 백그라운드 GPS native smoke

## 한눈에 보기

- 계획상 위치: M2의 background GPS·offline local persistence gate다.
- 실제 상태: 달력상 7월 23일에 Android 11 emulator의 native development build로 M2
  보완 검증을 수행했다.
- 확인한 결과: 화면 밖 foreground service, 합성 callback SQLite 저장, process 재시작
  뒤 service 복구, 명시적 종료를 확인했다.
- 가장 중요한 제한: Android 실기기와 iPhone은 아직 검증하지 않았다. M3 HTTP upload도
  아직 executable 흐름에 연결되지 않았다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 기준 계획이며 실제 성과를 소급하지 않는다.

- M2 목표: React Native 앱에서 background 위치 수집과 offline SQLite 보존을 양 플랫폼
  development build로 검증한다.
- 예상 산출물: 권한 화면, foreground service/indicator, background count 변화, 앱
  재시작 복구, 종료 증거, 배터리·정확도 기준선.
- 계획 완료 조건: Android와 iPhone 각각에서 화면 전환·잠금·process lifecycle을 통과하고
  실제 장치의 저장·복구 증거를 남긴다.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 환경 |
| --- | --- | --- | --- | --- |
| Android background service | `검증됨` | Service·notification foreground 실행 | Emulator까지, 실기기 미검증 | local Android 11 x86 |
| 화면 밖 저장 | `검증됨` | 합성 callback 후 active-session count 증가 | 야외 GPS·배터리 미측정 | synthetic |
| Cold launch 복구 | `검증됨` | Process force-stop 뒤 service 재등록과 후속 저장 | 재부팅·OEM kill 미검증 | local |
| 명시적 종료 | `검증됨` | UI idle, service non-foreground, notification 제거 | 장시간 반복 미검증 | local |
| iPhone lifecycle | `미착수` | Mac/Xcode 필요 | M2 gate 미종료 | 해당 없음 |
| Offline HTTP reconnect | `진행 중` | Local ledger·lease 기반만 존재 | Transport·Auth 미연결 | local unit |

### 실제 결과 상세

- 앱은 Flutter가 아니라 React 19·React Native 0.86·Expo 57·TypeScript의 공통
  코드베이스다. Android와 iOS UI·도메인 로직은 같은 React Native 구현을 사용한다.
- Expo는 React Native를 대체하는 UI 프레임워크가 아니라 development build, 위치 권한,
  TaskManager, SQLite와 native project 생성을 제공하는 통합 계층이다.
- Android native smoke에서 첫 background callback crash와 cold-launch false-recording
  상태를 발견했고, manifest permission과 cold-launch service reconciliation으로 닫았다.
- 화면에는 원본 좌표를 표시하지 않았고 저장소 증거에도 좌표를 남기지 않았다.

## 3. 근거

| 실제 주장 | 증거 | 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| Android native lifecycle와 count 변화 | [EVD-20260723-051](../../evidence/2026-07.md#evd-20260723-051--android-background-gps-native-lifecycle-smoke와-cold-launch-복구) | `generated` | Codex / 2026-07-23 |
| Cold-launch 복구 결정 | [ADR-0038](../../decisions/ADR-0038-cold-launch-background-service-reconciliation.md) | `accepted` | Codex / 2026-07-23 |
| 실제 코드 변화 | [UPD-20260723-17](../../product-updates/UPD-20260723-17-android-background-native-smoke.md) | `draft` | 사람 검토 대기 |
| 검증 중 오류와 정정 | [DEVFAIL-20260723-01](../../development/DEVELOPMENT_FAILURE_LOG.md#devfail-20260723-01--android-background-job-crash와-false-recording-state) | `resolved-local` | Codex / 2026-07-23 |

## 화면 테스트 시점

| 시점 | 반드시 볼 화면·동작 | 실행 환경 | gate 결과 |
| --- | --- | --- | --- |
| 지금 | Android 권한, background 알림, 홈 전환, SQLite count, 종료·재실행 | Windows emulator/Android 실기기 | Emulator 부분 통과 |
| iPhone 이전 직후 | While Using→Always 권한, 잠금·background indicator, 강제종료·재실행 | Mac/Xcode+iPhone | 미검증 |
| 8월 | 주행 품질 label UI, ONNX 판별, 규칙 대비 모델 결과·지연시간 | Android+iPhone | 계획 |
| 9월 | Digital Twin 부품 상태, 위험곡선, confidence·abstention | 모바일+웹 console | 계획 |
| 10월 | Fact ID가 연결된 사용자·수리사·기관 보고서와 근거 열기 | 모바일+console | 계획 |
| 11월 | 복지관 운영 흐름, 접근성, 권한·삭제, 장애·관측 대시보드 | 실기기+staging | 계획 |
| 12월 | GPS→Twin→위험→근거 보고서의 5분 통합 데모 | 발표 장비 | 계획 |

## 결정·제품 변화·인시던트

- 관련 결정: ADR-0038
- 실제 제품 업데이트: UPD-20260723-17
- 인시던트: 없음. Local emulator·synthetic data에서 배포 전에 발견했고 실제 사용자,
  기관, staging·production 영향이 없어 incidents severity 기준에 해당하지 않는다.
- 열린 위험: Android upgrade 경로, OEM kill, iOS lifecycle, native SQLite contention,
  offline HTTP/auth, 위치 보존·삭제 정책.

## 다음 회차

1. Android 실기기에서 잠금·recent-app swipe·30분 background·권한 철회를 실행한다.
2. Mac으로 저장소를 옮겨 iPhone development build와 같은 checklist를 실행한다.
3. M3의 retry/backoff·ACK transaction을 닫고 Firebase Auth/App Check가 있는 HTTP
   reconnect E2E를 만든다.
4. 8월 모델 gate 전에 실제·합성 데이터 구분과 label UI를 먼저 고정한다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 참석자·사진·지출: 해당 없음
- 이 문서는 기술 점검 요청에 대한 draft이며 회의록이 아니다.

## 발행 전 검토

- [x] M2 계획과 7월 실제 수행 시점을 분리했다.
- [x] Emulator·synthetic 결과를 실기기·field 결과로 표현하지 않았다.
- [x] React Native와 Expo의 역할을 명시했다.
- [x] 화면 테스트 시점을 8~12월 gate와 연결했다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 실제 주장과 증거를 검토했다.
