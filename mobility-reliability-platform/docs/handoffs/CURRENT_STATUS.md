# 현재 구현 상태 — 2026-08-14

이 문서는 새 개발 환경에서 **현재 코드가 어디까지 구현됐고 무엇이 아직 연결되지 않았는지** 판단하는 기준점이다. 계획은 [8개월 로드맵](../ROADMAP.md), 역사적 증분은 [제품 업데이트 인벤토리](../product-updates/README.md), 개별 검증은 [증거 인덱스](../evidence/README.md)를 사용한다.

## 기준점

- 기준일: 2026-08-14 KST
- 기준 commit: `dc2548c`
- 작업 환경: WSL2 local, Firebase Emulator, deterministic synthetic/test data
- 사람 검토 상태: generated — 저장소 자동검사 결과이며 production·field 승인 아님

## 로드맵 위치

| 게이트 | 현재 코드 상태 | 검증 범위 | 아직 증명하지 않은 것 |
| --- | --- | --- | --- |
| M1~M3 모바일 telemetry | foreground/background capture, SQLite v4, immutable upload·recovery 구성요소 구현 | unit, Android emulator development client 일부, static Android/iOS export | Android 실기기, iPhone native lifecycle, production upload/ACK, 장시간·배터리 |
| R07 주행 품질 | 합성 dataset·feature·rules·PyTorch 후보와 field admission/evaluation-only 계약 구현 | local synthetic | field label/data, ONNX 효용, on-device 성능 |
| 수리·지원금 제품 흐름 | 이용자·수리사·복지관 UI, 5개 Firebase HTTP function, verified repair archive, 구조화 items, 센터 검증→지원금 집행 부분 성공 제어 구현 | local, Firestore Emulator, web visual | production Firebase/Auth/App Check, 실제 기관 데이터 이관, 현장 업무 |
| R09 Digital Twin 기반 | 완료 수리 replay, current-state pure projector, shadow promotion·legacy dry-run 계약 구현 | local synthetic/Emulator | production backfill, GPS·점검·Storage 통합, 전체 twin 운영 |
| R10 신뢰성 baseline | censoring/time-split/abstention 계약과 합성 기준선·콘솔 설명 UI 구현 | local synthetic | 생존모델 field calibration, 실제 고장 예측 성능 |
| R11 calibration 판단 | estimability를 평가하고 표본 부족 시 판단 유보를 표시 | local synthetic | field calibration 완료 또는 위험도 배포 |
| R12 근거형 보고서 | 5-Fact deterministic fallback, source self-hash, tenant/parent binding, generation→review→publication 상태, unsupported claim omission 구현 | local synthetic, Rules/console/unit | LLM worker, Firestore writer/runtime persistence, 사람 승인·외부 발행 |
| R13~R16 | 계획만 존재 | 해당 없음 | pilot, field, production, 정책·최종 발표 성과 전부 |

8월의 수리·지원금 구현은 제품 workstream의 선행 증분이고, 8월 fixed R07/R08의 주 게이트는 ML 품질·온디바이스 판단이다. 계획 월과 실제 조기 구현일을 같은 것으로 보지 않는다.

## 재현된 검사 수치

아래는 2026-08-14 현재 저장소 회귀 기준이다. 과거 EVD의 당시 수치는 덮어쓰지 않는다.

- 모바일: 258 tests
- 복지관 콘솔: 25 tests
- domain-command pure: 32 tests; 일반 unit 실행에서 Emulator 항목 19개 제외
- Firebase Rules: 41 tests
- report-evidence: 17 tests
- Playwright console: 6 tests
- Android/iOS Expo static export: 성공

정확한 재실행 명령은 각 package README와 [Mac/iOS runbook](../development/MAC_IOS_RUNBOOK.md)을 따른다. 실제 Mac/Xcode/iPhone native build 성공은 아직 증거가 없다.

## 제품 표면

- 이용자 모바일: 기기 확인, 수리 요청, 진행 상태, 완료 수리 이력, 지원금 상태, 주행 시작·종료 요약.
- 수리사 모바일: 배정 작업, QR/공개코드 기기 확인, 방문 일정, 작업 시작, 1~20개 구조화 항목과 비용 제출.
- 복지관 콘솔: 오늘의 운영, 이용자·기기·수리·지원금, 센터 검증, 예방점검 근거, 합성 reliability 비교, 보고서 검토 상태.
- 서버: command 3개와 product projection 2개, 총 5개 Firebase Functions export. 기본 모바일 product repository는 deterministic demo이며 production wiring은 자동 활성화되지 않는다.

## 안전 경계와 다음 작업

1. 실기기 검증 전 iOS/Android background 신뢰성을 완료로 표현하지 않는다.
2. R10/R11 합성 결과를 field 위험도나 예산 절감으로 확대하지 않는다.
3. R12 보고서는 현재 deterministic fallback이며 생성 완료와 사람 검토·발행을 분리한다.
4. `reportRuns/claims` Rules는 구현됐지만 production writer와 persistence는 미연결이다.
5. 다음 인계 우선순위는 Mac/iPhone L3 smoke, Firebase staging wiring, 승인된 repair export dry-run, field admission 순이다.

