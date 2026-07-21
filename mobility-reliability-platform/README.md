# Mobility Reliability Platform

> 내부 개발용 임시 명칭입니다. 상표 검토 전까지 외부 제품명으로 사용하지 않습니다.

전동보장구 사용자의 모바일 기기에서 수집한 주행 데이터와 수리·점검 이력을 결합해, 기기 상태와 예방점검 근거를 사용자·수리사·복지관에 제공하는 신규 플랫폼입니다.

이 저장소는 기존 `soo-ri`, `soo-ri-admin`, `power_assist_device_helper_backend`를 포크하거나 연장하지 않습니다. 기존 자산은 요구사항, DB 형식, 수리 도메인을 이해하기 위한 참고 자료와 승인된 데이터 이관 원천으로만 사용합니다.

## 프로젝트 기간

- 2026년 5월 1일 ~ 2026년 12월 31일
- 기술 정기리포트: 매월 2회, 총 16회
- 현재 상태: 신규 코드베이스 및 운영 문서 기반 구축

## 목표 제품

- React Native 기반 사용자·수리사 모바일 앱
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
  telemetry-gateway/      모바일 이벤트 수집 경계
  ml/                     학습·평가·모델 패키징
packages/
  contracts/              서비스 간 버전 고정 데이터 계약
infra/                    로컬·배포 인프라 정의
docs/
  decisions/              기술·제품 의사결정과 대안
  product-updates/         실제 제품 변경사항만 기록
  incidents/              중대 오류와 사후분석
  reports/                 사람에게 전달하는 정기·수시 보고서
  evidence/                테스트·실험·데모 증빙 규칙
  data/                    데이터 모델과 이관 기준
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

상세 범위는 [프로젝트 헌장](docs/PROJECT_CHARTER.md), 월별 게이트는 [8개월 로드맵](docs/ROADMAP.md), 시스템 경계는 [아키텍처](docs/architecture/SYSTEM_ARCHITECTURE.md)를 확인합니다.
