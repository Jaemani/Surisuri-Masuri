# Mobility Reliability Platform

> 내부 개발용 임시 명칭입니다. 상표 검토 전까지 외부 제품명으로 사용하지 않습니다.

전동보장구 사용자의 수리 요청부터 수리소 작업, 복지관 검증, 보조금 집행, 예방점검까지 연결하는 복지관 중심 운영 플랫폼입니다. 모바일 GPS와 AI는 사용량·수리 이력에 근거한 점검 시기를 개선하는 보조 레이어입니다.

이 저장소는 기존 `soo-ri`, `soo-ri-admin`, `power_assist_device_helper_backend`를 포크하거나 연장하지 않습니다. 기존 자산은 요구사항, DB 형식, 수리 도메인을 이해하기 위한 참고 자료와 승인된 데이터 이관 원천으로만 사용합니다.

과거 프로젝트 참조 경로: [`techforimpact-archive/TFI_CAMPUS_25Spring_Soori-soori`](https://github.com/techforimpact-archive/TFI_CAMPUS_25Spring_Soori-soori). 신규 구현은 이 archive에 런타임 의존하지 않으며, 상표·UI·코드는 재사용하지 않습니다. 저장소 접근 가능 여부와 권한은 별도로 확인합니다.

## 프로젝트 기간

- 2026년 5월 1일 ~ 2026년 12월 31일
- 기술 정기리포트: 매월 2회, 총 16회
- 현재 상태: local synthetic 제품에서 수리 접수→복지관 배정→수리사 QR 대조·native 일정→복수 구조화 작업 제출→센터 검증→지원금 집행의 부분 성공 흐름이 연결됐다. SQLite GPS outbox, telemetry 수집·복구 경계, R07 PyTorch 평가 후보, R10 합성 신뢰성 기준선, R11 calibration 판단 유보, R12 5-Fact deterministic fallback·검토/발행 lifecycle도 구현돼 있다. 정확한 기준 commit과 환경은 [CURRENT_STATUS](./docs/handoffs/CURRENT_STATUS.md)를 따른다.
- 미검증 상태: Firebase production·staging 연결, 실제 기관·수리사·지원금 처리, Android 실기기/iPhone QR·일정·background lifecycle, 야외 GPS와 field holdout, 실제 repair/inspection/trip 신뢰성 dataset·field calibration, LLM worker·report persistence·사람 발행. 합성 기준선만으로 ONNX 또는 production reliability 배포를 진행하지 않는다.

## 목표 제품

- React Native 기반 사용자·보호자·수리사 역할별 모바일 앱
- 수리 요청·배정·수리사 제출·복지관 검증 workflow
- 예약·집행·취소·조정을 추적하는 지원금 원장
- 스마트폰 자체 GPS 기반 주행 세션 수집
- 네트워크 단절을 견디는 로컬 이벤트 로그와 멱등 동기화
- Firebase Auth·App Check·FCM·Crashlytics 기반 관리 기능
- Go Cloud Run 기반 텔레메트리 수집 경계
- Firestore 제어 데이터와 Cloud Storage 원본 batch 분리
- 필요 시 BigQuery GIS로 확장하는 분석 플랫폼
- PyTorch 학습 및 ONNX 온디바이스 데이터 품질 판별
- 수리 이력과 사용량을 결합한 신뢰성·생존분석
- 계산 근거가 연결되는 AI 보고서
- 복지관용 신규 운영 콘솔과 기관별 문서 출력

## 저장소 구조

```text
apps/
  mobile/                 React Native 모바일 앱
  console/                복지관 운영 콘솔
services/
  domain-command/         수리·지원금 서버 명령 경계
  telemetry-gateway/      모바일 이벤트 수집 경계
  ml/                     학습·평가·모델 패키징
packages/
  contracts/              서비스 간 버전 고정 데이터 계약
  domain-workflows/       수리 상태기계와 지원금 원장 projection
  legacy-importer/        레거시 데이터 dry-run·검역·대조
infra/                    로컬·배포 인프라 정의
docs/
  decisions/              기술·제품 의사결정과 대안
  product-updates/         실제 제품 변경사항만 기록
  incidents/              중대 오류와 사후분석
  reports/                 사람에게 전달하는 정기·수시 보고서
  evidence/                테스트·실험·데모 증빙 규칙
  data/                    데이터 모델과 이관 기준
  handoffs/                다른 개발환경에서 이어받기 위한 실행 경계
```

## 문서 원칙

문서의 목적을 섞지 않습니다.

- 왜 선택했는가: `docs/decisions/`
- 제품에서 실제로 무엇이 바뀌었는가: `docs/product-updates/`
- 어떤 심각한 장애가 있었는가: `docs/incidents/`
- 사람에게 무엇을 보고하는가: `docs/reports/`
- 그 주장을 무엇으로 검증하는가: `docs/evidence/`

정기리포트 사전작성본은 8개월 계획을 고정하는 문서입니다. 실제 완료 여부는 증빙 칸에서 별도로 갱신하며, 계획을 완료 실적으로 바꾸어 쓰지 않습니다.

## 우선순위

1. 데이터의 진실성과 사용자 안전
2. 오프라인·권한 거부·앱 종료 상황에서도 복구 가능한 수집
3. 고령 사용자와 장애인을 위한 낮은 입력 부담
4. 모델 성능보다 재현 가능한 평가와 불확실성 공개
5. 발표 가능한 시각 결과와 실제 운영 증거의 동시 확보

전체 기준 문서와 읽는 순서는 [문서 인덱스](docs/INDEX.md)에서 확인합니다. 상세 범위는 [프로젝트 헌장](docs/PROJECT_CHARTER.md), 월별 게이트는 [8개월 로드맵](docs/ROADMAP.md), 시스템 경계는 [아키텍처](docs/architecture/SYSTEM_ARCHITECTURE.md)를 따릅니다.

Firebase 제품 연결을 이어서 구현할 때는 [command·projection handoff](docs/handoffs/FIREBASE_PRODUCT_CONNECTION.md)를 먼저 확인합니다.

QR camera, native 방문 일정과 복수 수리항목 흐름을 Android·iPhone에서 확인할 때는 [모바일 제품 실기기 smoke](docs/development/MOBILE_PRODUCT_DEVICE_SMOKE.md)를 사용합니다. 체크 결과는 실제 기기 실행 뒤에만 채웁니다.
