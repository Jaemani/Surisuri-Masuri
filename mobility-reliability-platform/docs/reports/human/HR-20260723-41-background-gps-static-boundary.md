---
id: HR-20260723-41
report_type: requested
status: draft
period_start: 2026-07-23
period_end: 2026-07-23
issued_at: TBD
roadmap_month: M2
technical_gate: background GPS static implementation boundary
author: Codex
reviewer: human-review-required
audience: project owner and technical reviewers
---

# 요청 기술 리포트: 백그라운드 GPS 정적 구현 경계

## 한눈에 보기

- 계획상 위치: M2인 6월의 reliable mobile capture 게이트에서 화면 전환·잠금
  중에도 사용자가 시작한 주행을 로컬에 보존하는 단계다.
- 실제 시점: 2026-07-23에 M3의 upload·sync 기반 작업과 병행해 source commits
  `a65cd4d`, `ac3e408`로 구현·보강했다. 일정상 M2 항목을 7월에 보완한 것이며 M2 전체나 M3
  전체를 완료했다는 뜻은 아니다.
- 실제 결과: Expo background location 설정, 전역 task 등록, foreground와 같은
  SQLite append 경로, bounded batch·replay·failure 처리를 코드·설정·단위 테스트
  수준에서 확인했다.
- 가장 중요한 제한: 이 결과는 **정적 source/config/unit evidence**다. Android와
  iPhone의 실제 background lifecycle, 화면 잠금·OS 종료·재시작, HTTP 전송은
  검증하지 않았다.

## 8개월 로드맵 대비 현재 위치

| 월 | 계획한 기술 게이트 | 보고 시점 실제 상태 |
| --- | --- | --- |
| 5월 (M1) | 신규 앱 shell·foreground GPS | Android emulator foreground smoke까지 별도 확인 |
| 6월 (M2) | Background GPS·offline local persistence | 7월 23일 source/config/unit 구현. Native lifecycle 미검증 |
| 7월 (M3) | Auth·HTTP upload·ACK·정제 경로·지도 | Local protocol·ledger·materializer·lease 일부 구현. HTTP 없음 |
| 8~12월 (M4~M8) | ML→Twin→근거형 AI report→실증→통합 | 미착수 |

M2의 목표를 달력상 6월에 완료한 것으로 소급하지 않는다. 이번 구현은 7월 23일에
M3 작업과 함께 수행한 M2 보완분이며, 실기기 검증이 남아 있어 M2 reliable capture
게이트도 아직 닫히지 않았다.

## 계획과 실제

| 항목 | M2 계획 | 2026-07-23 실제 | 남은 검증 |
| --- | --- | --- | --- |
| Background 권한·설정 | Android/iOS background 위치 허용 | Expo plugin, iOS background mode, Android foreground service 설정을 introspection으로 확인 | 두 플랫폼 development build 권한 흐름 |
| Task lifecycle | 화면 전환·잠금 중 수집 지속 | 전역 TaskManager task 정의와 start/stop runtime을 source·unit에서 확인 | 잠금·background·OS reclaim·재부팅 |
| Local persistence | Callback을 기존 append-only SQLite 경로에 저장 | Active session 조회 후 foreground와 동일한 sample policy·SQLite append 사용 | 실제 GPS writer contention·강제종료 복구 |
| Batch 경계 | 잘못된 callback을 좌표 노출 없이 거부 | 최대 100건, malformed/native error, bounded coordinate-free failure code 처리 | 실제 OS callback shape·빈도 |
| Replay 경계 | callback 재전달의 중복 반영 방지 | Last accepted timestamp 이하 sample 무시, batch 입력 timestamp 순 정렬 | Native duplicate delivery와 clock edge |
| Network sync | Offline 저장 후 reconnect upload | 구현·검증하지 않음 | Firebase Auth/App Check, HTTP, retry·ACK |

## 실제 구현 결과

1. `expo-location`과 `expo-task-manager`를 연결하고 background task를 React
   component 밖의 진입점에서 등록했다.
2. 사용자가 시작한 active trip이 있을 때만 callback을 처리하며, 입력을 시간순으로
   평가해 foreground 수집과 동일한 sample policy 및 SQLite 이벤트 경로에 넣는다.
3. Active trip이 없으면 좌표를 새 session으로 추정해 만들지 않고 count-only 결과로
   종료한다.
4. Native task error, 잘못된 payload, 과대 batch, 처리 실패는 좌표나 원문을
   보관하지 않는 bounded 상태 코드로 남기도록 했다.
5. Background start 실패 시 사용자의 session 상태와 수집 상태가 어긋나지 않도록
   fail-closed 흐름을 두고, 앱에는 raw coordinate 대신 제한된 상태만 노출한다.

이는 background capture를 **구현한 코드 경계**에 대한 설명이다. 실제 Android나
iPhone에서 앱이 background로 내려가거나 화면이 잠긴 뒤 위치가 계속 저장됐다는
현장·native 실행 주장은 아니다.

## 근거

- 증거: [EVD-20260723-050](../../evidence/2026-07.md#evd-20260723-050--백그라운드-gps-정적-구현-경계)
- 구현 commits: `a65cd4d` (`feat: add fail-closed background GPS capture`),
  `ac3e408` (`fix: latch background capture failures`)
- 결정: [ADR-0037](../../decisions/ADR-0037-fail-closed-background-gps-capture.md)
- 제품 변화: [UPD-20260723-16](../../product-updates/UPD-20260723-16-background-gps-static-boundary.md)
- 인시던트: 해당 없음 — production·staging·field 및 사용자 영향 없음

위 링크 중 ADR·UPD·EVD는 각각 의사결정, 제품 업데이트, 검증 근거의 원문이다.
이 사람 대상 리포트는 해당 원문을 대체하지 않으며 사람이 최종 검토하기 전 상태는
`draft`다.

## 검증 범위

- 최종 모바일 Vitest 10개 파일, 146건 통과
- 모바일 TypeScript 검사 통과
- Android·iOS Expo static export 통과
- Expo config introspection에서 background location·foreground service 설정 확인
- 입력 정렬, replay 무시, no-active-session, malformed payload, 최대 batch,
  coordinate-free durable failure와 start/stop 실패 경계에 대한 단위 검증
- 검증 환경: WSL2. WSL 안에서는 `adb`를 사용할 수 없어 Android native 실행을
  이번 결과에 포함하지 않음

전체 검증 중 Firebase emulator suite의 테스트 자체는 통과했으나 종료 단계가 한 번
실패했다. 이후 영향을 받는 targeted 명령을 깨끗한 상태에서 다시 실행해 통과를
확인했다. 제품 runtime 장애, 데이터 손실, 외부 사용자 영향이 없었으므로 별도
인시던트로 분류하지 않는다. 다만 이 재실행은 Android/iPhone background lifecycle의
대체 근거가 아니다.

## 검증하지 않은 것

- Android 또는 iPhone 실기기·native development build의 background callback
- 화면 잠금, 앱 전환, OS process 종료, 장시간 정지, 재부팅 후의 수집 지속·복구
- 실제 기기 GPS 좌표, 배터리 사용량, 정확도, callback 지연
- 오류 callback 없이 OS delivery가 멈춘 상태의 탐지와 liveness watchdog
- iOS SQLite backup 제외·file protection, 원본 좌표 보존·삭제 정책
- Offline→reconnect HTTP upload, retry, exact ACK와 server receipt
- Firebase Auth/App Check 및 server-issued session scope
- 실제 사용자 GPS, 복지관 현장 실증, production·staging 배포

따라서 이번 결과를 “실기기 백그라운드 GPS 완료”, “종단간 telemetry 완료” 또는
“현장 수집 검증”으로 보고하지 않는다.

## 다음 회차

1. Android development build에서 foreground→background→잠금→복귀와 SQLite
   저장을 검증한다.
2. 별도 writer connection으로 GPS append와 upload lease가 경쟁할 때 timeout과
   fail-closed 동작을 측정한다.
3. iPhone development build에서 권한 단계, background mode, 중단·복구를 같은
   체크리스트로 검증한다.
4. Leased→pending retry/backoff와 exact ACK transaction을 구현한 뒤 HTTP transport를
   연결한다.
5. 실제 좌표를 저장하기 전 보존·삭제·동의 범위와 승인된 테스트 계정을 수동 확인한다.

## 회의·증빙 확인

- 실제 회의 여부: 아니오
- 참석자·사진·지출: 해당 없음
- 실제 사용자 GPS: 수집하지 않음
- 이 문서는 실제 코드·설정·단위 검증 결과 요청 리포트이며 회의록이 아니다.

## 발행 전 검토

- [x] M2 계획과 7월 23일 실제 구현 시점을 분리했다.
- [x] Static source/config/unit evidence와 native lifecycle 검증을 분리했다.
- [x] Android·iPhone, HTTP, 사용자 GPS, 현장 실증을 완료로 표현하지 않았다.
- [x] Firebase emulator 종료 관찰과 제품 인시던트를 구분했다.
- [x] 실제 회의·참석자·사진·지출을 생성하지 않았다.
- [ ] 사람이 실제 주장과 링크된 근거를 검토했다.
