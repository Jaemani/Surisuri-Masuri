---
id: UPD-20260723-16
date: 2026-07-23
status: draft
version_or_deployment: background-gps-v1-source-static
roadmap_month: M2
owner: project owner
reviewed_at: TBD
---

# 제품 업데이트: 백그라운드 GPS 정적 구현 경계

## 요약

사용자가 시작한 주행을 development build에서 화면 밖에서도 이어 기록할 수 있도록
Expo TaskManager 기반 백그라운드 위치 수집 경로를 추가했다. 백그라운드 권한을 별도로
받지 않았거나 runtime을 사용할 수 없으면 foreground 수집을 유지하며, 두 경로 모두 같은
append-only SQLite session/event 저장 경계를 사용한다.

이 업데이트는 local source와 Android·iOS static export/config introspection까지만 검증한
draft다. Android/iPhone 실기기 수명주기, 배터리, native 동시성 또는 사용자 GPS 수집이
완료됐다는 주장이 아니다.

## 변경 전 문제

- 위치 수집은 앱 화면이 열린 동안의 foreground subscription에만 연결돼 있었다.
- OS가 앱 화면 밖에서 전달하는 위치 batch를 전역 task로 수신하고 기존 append-only
  SQLite event로 정규화하는 경로가 없었다.
- 백그라운드 권한 거부, native callback 오류와 앱 복귀 시 권한 철회·task 실패를
  fail-closed하게 반영하는 runtime 경계가 없었다.
- Expo Go와 development build의 기능 경계 및 Android/iOS application ID가 명시되지
  않았다.

## 변경 후 동작

- App root를 등록하기 전에 전역 모듈 scope에서
  `mobility-reliability.background-location.v1` TaskManager task를 한 번 등록한다.
- 사용자는 먼저 foreground 위치 권한을 허용하고, 별도 동작을 통해 background 권한을
  명시적으로 요청한다. 두 번째 권한을 허용하지 않아도 주행은 foreground capture로
  시작하거나 재개할 수 있다.
- 활성 foreground 주행 중 background 권한을 허용하면 foreground callback을 닫고 queued
  write가 끝난 뒤 background runtime으로 handoff한다. 이미 background 권한이 있으면 새
  주행·재개 시 background를 우선 사용하고, 사용할 수 없으면 foreground로 fallback한다.
- Foreground와 background sample은 동일한 정책 필터와 동일한 append-only SQLite
  session/event/outbox 경계를 사용한다.
- Native batch는 timestamp 오름차순으로 안정 정렬한 뒤 정책 필터를 통과시킨다. 활성
  session 시작 또는 마지막 저장 sample 이전·동일 시각의 fix는 pre-session/replay로
  무시하고, DB transaction이 persisted timestamp보다 반드시 큰 값만 받아 최종 단조성
  경계를 지킨다.
- Task callback batch는 최대 100개로 제한하며 한 JavaScript process 안에서는 delivery를
  직렬화한다. 이 직렬화는 여러 native runtime 또는 SQLite connection 사이의 mutual
  exclusion을 증명하지 않는다.
- Native task 오류, payload 오류, oversized batch와 batch 처리 실패는 좌표·session ID를
  담지 않는 bounded code와 시각만 durable metadata에 남긴다. Marker가 있으면 다음
  callback도 처리하지 않으며 사용자가 명시적으로 재시도할 때만 해제한다.
- 앱이 active 상태로 돌아와 runtime을 refresh할 때 background 권한이 철회됐거나 durable
  task failure marker가 있거나 활성 session이 없으면 background update를 중지하고 자동으로
  재시작하지 않는다.
- Expo Go가 아닌 development build를 기준으로 `expo-dev-client`를 추가했다. Android
  package와 iOS bundle identifier는 모두
  `com.jaemani.mobilityreliability.dev`로 고정했다.

## 범위

- 포함: global TaskManager 등록, foreground/background 2단계 권한, foreground fallback과
  background handoff, policy-filtered sorted batch, 최대 batch 100, in-process 직렬화,
  SQLite persisted timestamp 단조성 검사, coordinate-free durable failure marker,
  app-active refresh stop 경계, development-build config.
- 제외: 실제 Android/iPhone native 수명주기, swipe/kill·재부팅 후 동작, 배터리 측정,
  Android notification/iOS indicator 확인, native multi-runtime·SQLite contention,
  HTTP upload·재연결, Firebase Auth/App Check, server-managed scope, 실제 사용자 GPS와
  복지관 현장 실증.
- 배포 환경: `local source/static bundle`
- 데이터 유형: `synthetic GPS fixtures only`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 |
| --- | --- | --- | --- |
| Background processor·task·runtime·recorder 경계 | Mobile Vitest 10 files, 146 tests | `pass` | [EVD-20260723-050](../evidence/2026-07.md#evd-20260723-050--백그라운드-gps-정적-구현-경계) |
| TypeScript 정적 검사 | Mobile `tsc --noEmit` | `pass` | EVD-20260723-050 |
| Android·iOS bundle 생성 가능성 | 양 플랫폼 Expo static export | `pass` | EVD-20260723-050 |
| Background native config 반영 | Expo config introspection에서 Android background location·foreground location service 권한과 iOS `UIBackgroundModes: location` 확인 | `pass` | EVD-20260723-050 |
| Native lifecycle·표시·contention | Android/iPhone development build 필요 | `미검증` | 후속 gate |

## 배포와 롤백

- App Store, Play Store, staging, production 또는 field 배포는 수행하지 않았다.
- 현재 단계는 source/static bundle draft이며 실제 사용자 GPS를 수집하지 않았다.
- Source rollback은 field data가 없는 local 단계에서 commit `a65cd4d` 이전 코드로
  되돌리는 방식이다. 이미 SQLite에 저장된 local append-only event를 rollback 과정에서
  삭제하거나 재작성하지 않는다.
- Native 실기기 gate 전에는 백그라운드 GPS가 운영 가능하다고 표시하거나 배포하지 않는다.

## 알려진 제한과 후속 작업

- WSL2 환경에서 `adb`를 사용할 수 없어 Android development build를 설치·실행하지 못했다.
- Android/iPhone의 background/foreground 전환, 화면 잠금, swipe/kill, OS 재시작,
  permission revoke와 location service 변경을 실기기에서 검증해야 한다.
- Android foreground-service notification과 iOS background-location indicator가 실제로
  표시되는지 확인해야 한다.
- 배터리·정확도·batch delivery 지연을 실기기에서 측정하지 않았다.
- OS가 오류 callback 없이 location delivery를 중단하는 경우를 탐지할 liveness
  watchdog은 아직 없다.
- iOS SQLite backup 제외·file protection과 원본 좌표 보존·삭제 정책을 실제 GPS
  수집 전에 별도 privacy gate로 확정해야 한다.
- In-process queue는 native multi-runtime이나 GPS writer와 다른 SQLite writer 사이의
  contention을 막지 않는다. Android native writer-contention gate가 필요하다.
- HTTP transport, reconnect, retry/ACK transaction, Firebase Auth/App Check와
  server-issued session scope를 연결한 뒤에야 phone GPS에서 gateway까지 E2E를 주장할 수 있다.

## 관련 기록

- 결정: [ADR-0037](../decisions/ADR-0037-fail-closed-background-gps-capture.md)
- 증거: [EVD-20260723-050](../evidence/2026-07.md#evd-20260723-050--백그라운드-gps-정적-구현-경계)
- 인시던트: 해당 없음 — production·staging·field runtime과 사용자 데이터 영향 없음
- 사람 대상 리포트: [HR-20260723-41](../reports/human/HR-20260723-41-background-gps-static-boundary.md)
- 대체하는 업데이트: 없음 — [UPD-20260721-04](./UPD-20260721-04-foreground-telemetry.md)의 development-build background capture 증분

## 검토

- 검토자: Codex + delegated independent read-only review, 사람 검토 대기
- 실제 주장과 근거 일치 여부: commits `a65cd4d`, `ac3e408`의 local source, mobile test와 static bundle/config introspection 범위에서 일치
- 검토 메모: Native 실기기 수명주기, 배터리, notification/indicator, multi-runtime contention,
  HTTP/auth 또는 사용자 GPS 검증으로 표현하지 않는다.
