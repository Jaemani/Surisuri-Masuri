---
id: HR-20260721-02
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M2
technical_gate: Foreground Telemetry Vertical Slice
author: Codex draft
reviewer: human review pending
audience: project owner
---

# 기술 리포트: 모바일 주행 기록 첫 vertical slice

## 한눈에 보기

- 이번 회차의 사전 목적: 명시적 주행 세션, 휴대폰 자체 GPS와 offline-first event 저장의 가장 작은 실행 경로를 만든다.
- 보고 기준일의 실제 상태: foreground 권한·주행 시작/종료·sample 품질 정책·SQLite outbox·재시작 복구 UI가 코드로 구현되고 정적 검사·정책 테스트 65건·Android/iOS bundle이 통과했다.
- 가장 중요한 차이 또는 위험: WSL2에 ADB가 없고 iOS native build가 불가능해 실제 휴대폰 GPS·SQLite runtime은 아직 검증되지 않았다.
- 사람에게 필요한 결정·확인: Android·iPhone에서 권한, 10~20분 주행, 앱 재시작 시나리오를 실행하고 결과를 실제 evidence로 확정해야 한다.

## 1. 계획

> 이 섹션은 8개월 로드맵에 따른 계획이며 실제 성과가 아니다.

- 로드맵상 위치: 6월 Background GPS·offline sync 중 foreground 선행 gate.
- 계획한 기술 주제: 위치 권한 상태, 명시적 세션, WAL event log, 순서 번호, outbox delivery 분리, 재시작 복구.
- 예상 산출물: 시작/종료 UI, sample policy, SQLite repository, 테스트 결과, 양 플랫폼 bundle.
- 검토할 질문: 좌표가 UI·로그에 노출되지 않는가, 세션과 sample 순서가 transaction으로 보존되는가, 실기기 전 검증 범위를 과장하지 않는가.
- 계획 완료 조건: pure policy test, typecheck, Android/iOS bundle 통과와 실기기 미검증 표시.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| 위치 권한 상태 | `검증됨` | 순수 상태 변환 9건 통과 | native prompt는 미검증 | `WSL2 unit` |
| GPS sample policy | `검증됨` | 경계값·정규화 53건 통과 | 실제 GPS 분포는 미측정 | `WSL2 unit` |
| 중복 동작·늦은 callback guard | `검증됨` | 순수 guard 3건 통과 | hook/native 통합은 실기기 미검증 | `WSL2 unit` |
| SQLite event outbox | `진행 중` | WAL·event/projection/delivery transaction 코드 구현 | native runtime 미검증 | `static` |
| Android·iOS app | `부분 검증` | Expo Doctor 20/20과 양 플랫폼 JS bundle export 통과 | 설치·실행 미검증 | `WSL2 bundle` |
| background·server sync | `미착수` | 동작 증거 없음 | 후속 gate로 분리 | 해당 없음 |

### 실제 결과 상세

- 결과: 기존 정적 셸이 사용자가 시작한 세션의 위치를 로컬 event outbox에 기록하도록 확장됐다.
- 관측 수치: 2026-07-21 기준 pure test 65건 통과, Android bundle 1개와 iOS bundle 1개 생성 성공.
- 데이터 유형: synthetic unit fixture / 실제 GPS 없음.
- 알려진 제한: native SQLite, OS permission, 야외 accuracy, 배터리, 앱 종료 복구와 네트워크 sync는 증명되지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| 정책 테스트 65건·typecheck·Android/iOS bundle 통과 | [EVD-20260721-007](../../evidence/2026-07.md#evd-20260721-007--foreground-telemetry-정적정책-검증) | `generated` — 실기기 검토 전 | Codex / 2026-07-21 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0008](../../decisions/ADR-0008-foreground-telemetry-slice.md)
- 실제 제품 업데이트: [UPD-20260721-04](../../product-updates/UPD-20260721-04-foreground-telemetry.md)
- 인시던트: 해당 없음 — production·field 사용자 영향 없음
- 열린 위험: WSL과 실제 장치 네트워크, iOS EAS build·SQLite file protection, Android OEM 동작, native DB transaction을 아직 검증하지 않았다. iOS time interval은 Android와 동일하지 않다.

## 다음 회차

- 8개월 계획상 다음 주제: background GPS와 offline batch sync.
- 실제 상태를 반영한 다음 검증: 먼저 Android·iPhone foreground 실기기 검증과 DB recovery 증거를 만든 뒤 background 권한으로 확장한다.
- 필요한 사람의 결정·지원: 장비별 OS 버전과 테스트 결과를 사람이 확인해 evidence 상태를 갱신한다.

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: `아니오`
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 사람 확인 필요

> 참석자, 사진, 지출 및 시각은 자동 생성하거나 추정하지 않았다.

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] 실제 주장마다 근거가 있거나 미검증으로 표시했다.
- [x] 수치에 측정일·모수·단위가 있다.
- [x] synthetic test와 실제 GPS 데이터를 구분했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·UPD·EVD를 원문으로 링크했다.
- [ ] 사람 검토 후 발행 상태와 발행일을 확정한다.
