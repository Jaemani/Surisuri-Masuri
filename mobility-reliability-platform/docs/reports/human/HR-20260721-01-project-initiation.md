---
id: HR-20260721-01
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M1-foundation-baseline
technical_gate: Greenfield Definition
author: Codex draft
reviewer: human review pending
audience: project owner
---

# 기술 리포트: 신규 프로젝트 착수 기반선

## 한눈에 보기

- 이번 회차의 사전 목적: 기존 세 코드베이스와 분리된 신규 프로젝트의 범위, 8개월 계획, 기록 체계와 초기 데이터 계약 골격을 고정한다.
- 보고 기준일의 실제 상태: 신규 폴더·문서·계약 골격, Expo 모바일 앱 셸, Firebase 로컬 보안 규칙과 WSL2 진단 기반까지 생성·확인되었다. GPS·SQLite runtime·Cloud Run·production Firebase·ML·현장 기능은 미착수다.
- 가장 중요한 차이 또는 위험: 5월~7월 계획 주제가 문서에는 정의되어 있지만, 문서 존재가 해당 기능의 실제 구현이나 과거 수행을 의미하지 않는다.
- 사람에게 필요한 결정·확인: 이 초안의 범위 표현과 이후 실제 진행 입력 방식에 대한 사람 검토가 필요하다.

## 1. 계획

> 이 섹션은 8개월 로드맵에 따른 계획이며 실제 성과가 아니다.

- 로드맵상 위치: 5월 Greenfield Definition을 기반으로 7월 Trusted Telemetry Platform까지 이어지는 착수 기준선.
- 계획한 기술 주제:
  - 상표·코드 재사용 경계를 분리한 신규 코드베이스
  - 모바일 자체 GPS 세션, 오프라인 이벤트 동기화와 Firebase/Cloud Run 책임 경계
  - 위치·신원 분리와 모델·LLM 책임 원칙
  - 텔레메트리 배치·도메인 이벤트 계약 초안
  - 5월~12월 월 2회 기술 리포트 운영
- 예상 산출물: 프로젝트 헌장, 로드맵, ADR 7개, 문서 스트림, JSON Schema 2개, Firebase 규칙 기반, WSL2 runbook, 고정 리포트 사전작성본 16개.
- 검토할 질문: 계획과 실제를 구분하는가, 신규 구조가 기존 런타임에 의존하지 않는가, 다음 구현이 검증 가능한 증거를 만들 수 있는가.
- 계획 완료 조건: 지정 문서와 계약 파일의 존재 및 개수 확인, JSON 구문 파싱 통과.

## 2. 실제

> 2026-07-21 기준으로 로컬 저장소에서 확인된 사실만 기록한다.

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| 신규 코드베이스 폴더 | `검증됨` | 모바일·콘솔·게이트웨이·ML·계약·인프라용 디렉터리 골격이 존재함 | 실행 코드는 없음 | `local` |
| 프로젝트 기준 문서 | `검증됨` | 프로젝트 헌장과 5월~12월 로드맵이 존재함 | 외부 제품명과 실제 구현 상태는 미확정·미착수 | `local` |
| ADR | `검증됨` | ADR 7개가 존재하며 ADR-0004는 superseded, ADR-0007이 현재 Firebase 우선 구조를 고정함 | 실제 runtime 반영은 아직 검증할 수 없음 | `local` |
| 문서 스트림 | `검증됨` | 결정·업데이트·인시던트·사람 리포트·증거 규칙과 템플릿이 분리됨 | 운영 누적 기록은 아직 없음 | `local` |
| JSON Schema | `검증됨` | 텔레메트리 배치와 도메인 이벤트의 유효·무효 fixture 4개가 기대대로 판별됨 | 실제 모바일·서버 payload 호환성은 미착수 | `local` |
| 모바일 앱 셸 | `검증됨` | Expo SDK 57 TypeScript 앱이 typecheck를 통과하고 iOS·Android config를 선언함 | 실기기 빌드와 GPS·SQLite 기능은 미착수 | `local` |
| Firebase 보안 기반 | `검증됨` | Firestore 6건·Storage 2건의 Emulator Rules test가 통과함 | production Auth·App Check·IAM·배포는 미착수 | `local demo project` |
| WSL2 개발환경 | `부분 준비` | `/home` workspace, Node, Java는 확인됨 | ADB와 Go는 PATH에 없고 iOS native build는 EAS·실기기가 필요함 | `WSL2 local` |
| 월 2회 리포트 | `검증됨` | 5월~12월 계획 기준 사전작성본 16개가 존재함 | 실제 회의·진척·증빙 입력은 비어 있음 | `local` |
| GPS·동기화·서버·현장 기능 | `미착수` | 실행 또는 현장 동작 증거 없음 | 계획상의 기능만 문서화됨 | 해당 없음 |

### 실제 결과 상세

- 결과: 신규 프로젝트의 문서·계약 기반선과 정적 검증 가능한 모바일 앱 셸이 만들어져 이후 기능 구현을 별도 증거로 추적할 수 있다.
- 관측 수치: ADR 7개, JSON Schema 2개, contract fixture 4개, Firebase Rules test 8개, 고정 리포트 사전작성본 16개. 측정일은 2026-07-21이다.
- 데이터 유형: `synthetic` contract fixture / 모바일 실행 데이터 없음.
- 알려진 제한: 앱 실기기 빌드, GPS 수집, SQLite 저장, 서버 수신, 데이터 저장, 모델 평가, 복지관 실증은 검증하지 않았다.

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| 신규 골격, 프로젝트 문서, ADR 7개, 문서 스트림, 사전작성본 16개가 존재함 | [EVD-20260721-001](../../evidence/2026-07.md#evd-20260721-001--신규-프로젝트-기반선-파일-인벤토리) | `generated` — 사람 검토 전 | Codex / 2026-07-21 |
| JSON Schema 2개가 JSON 구문 파싱을 통과함 | [EVD-20260721-002](../../evidence/2026-07.md#evd-20260721-002--json-schema-구문-파싱) | `generated` — 사람 검토 전 | Codex / 2026-07-21 |
| 계약 fixture 성공·실패 판별 | [EVD-20260721-003](../../evidence/2026-07.md#evd-20260721-003--계약-성공실패-fixture-검증) | `generated` — 사람 검토 전 | Codex / 2026-07-21 |
| Expo 모바일 앱 셸 정적 검증 | [EVD-20260721-004](../../evidence/2026-07.md#evd-20260721-004--expo-모바일-스캐폴드-정적-검증) | `generated` — 사람 검토 전 | Codex / 2026-07-21 |
| Firebase tenant·역할·Storage client 차단 회귀검사 | [EVD-20260721-005](../../evidence/2026-07.md#evd-20260721-005--firebase-security-rules-회귀검사) | `generated` — 사람 검토 전 | Codex / 2026-07-21 |
| WSL2 Node·Java 준비와 ADB·Go 경고 | [EVD-20260721-006](../../evidence/2026-07.md#evd-20260721-006--wsl2-개발환경-진단) | `generated` — 사람 검토 전 | Codex / 2026-07-21 |
| GPS·SQLite·서버·현장 기능이 완료되지 않음 | 앱 상태 화면과 실행 증거 부재 | `reviewed` | Codex / 2026-07-21 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0001](../../decisions/ADR-0001-greenfield-boundary.md), [ADR-0002](../../decisions/ADR-0002-mobile-gps-sessions.md), [ADR-0003](../../decisions/ADR-0003-offline-event-sync.md), [ADR-0004](../../decisions/ADR-0004-runtime-boundaries.md), [ADR-0005](../../decisions/ADR-0005-location-and-identity-separation.md), [ADR-0006](../../decisions/ADR-0006-model-and-llm-responsibility.md), [ADR-0007](../../decisions/ADR-0007-firebase-first-hybrid.md)
- 실제 제품 업데이트: [UPD-20260721-01](../../product-updates/UPD-20260721-01-foundation.md), [UPD-20260721-02](../../product-updates/UPD-20260721-02-executable-foundation.md), [UPD-20260721-03](../../product-updates/UPD-20260721-03-firebase-security-foundation.md)
- 인시던트: 해당 없음 — 런타임이 아직 없어 운영 장애를 주장하지 않음
- 열린 위험:
  - 계약 fixture는 합성이며 실제 모바일·서버 payload 호환성을 검증하지 않았다.
  - 모바일 앱 셸은 있지만 실기기 GPS·SQLite·서버 runtime이 없어 ADR의 실제 적용 여부를 검증할 수 없다.
  - Emulator Rules 통과는 production IAM·App Check·Admin SDK 보안을 증명하지 않는다.
  - WSL PATH에 Android ADB와 Go가 없어 각각 실기기 연결과 gateway 구현 전에 설치·버전 고정이 필요하다.
  - 사전작성본이 실제 회의나 완료 성과로 잘못 해석되지 않도록 매 회차 실제·근거를 별도 갱신해야 한다.

## 다음 회차

- 8개월 계획상 다음 주제: 실제 착수 시점의 상태를 기준으로 첫 모바일 GPS vertical slice와 계약 검증기를 구현한다.
- 실제 상태를 반영한 다음 검증:
  - Android/iOS 실기기 권한 흐름과 foreground GPS의 최소 실행 증거
  - 구현 결과가 생긴 경우에만 별도 제품 업데이트와 증거 ID 발급
- 필요한 사람의 결정·지원: 본 보고서 초안의 완료 범위와 미착수 표시를 검토하고 외부 발행 여부를 확정한다.

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
- [x] 실제 주장마다 근거가 있거나 미착수로 표시했다.
- [x] 수치에 측정일·모수·단위가 있다.
- [x] 실행 데이터가 없음을 명시했다.
- [x] 참석자·사진·지출을 생성하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] 관련 ADR·UPD·EVD를 원문으로 링크했다.
- [ ] 사람 검토 후 발행 상태와 발행일을 확정한다.
