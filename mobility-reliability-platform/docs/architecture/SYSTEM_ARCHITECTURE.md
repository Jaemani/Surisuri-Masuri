# 시스템 아키텍처

## 1. 목표

이 아키텍처는 모바일 위치 수집, 오프라인 동기화, 공간 데이터 처리, 기기 상태 복원, ML 평가, 근거형 보고서를 하나의 감사 가능한 흐름으로 연결한다.

## 2. 시스템 경계

```text
[Mobile App]
  React Native UI
  Native location lifecycle
  SQLite event outbox
  ONNX quality inference
          │ versioned batch + idempotency key
          ▼
[Firebase / GCP Boundary]
  Firebase Auth + App Check
  Go Cloud Run schema / tenant / idempotency validation
          │
          ├──> Cloud Storage: compressed raw GPS batches + lifecycle
          └──> Firestore: receipt / metadata / current projection
                         │
                         ├── projector ──> device/part current state
                         ├── feature job ─> Storage or optional BigQuery
                         └── fact job ───> evidence-backed facts
                    │
          ┌─────────┴─────────┐
          ▼                   ▼
[ML Service]            [Institution Console]
  training/evaluation     operational views
  model registry          reports/adapters
  calibrated inference    review/feedback
          │                   │
          └──────> [Report Agent] <──────┘
                     schema + fact validation
```

## 3. 런타임 책임

### Mobile

- 사용자가 명시적으로 시작한 주행 세션을 기본 단위로 수집한다.
- OS 권한과 lifecycle 변화를 이벤트로 남긴다.
- 서버 수신 확인 전까지 로컬 이벤트를 보관한다.
- 네트워크 재연결 시 제한된 batch로 재전송한다.
- ML 결과가 낮은 신뢰도이면 사용자 또는 후처리 검토 대상으로 보낸다.

### Telemetry Gateway

- 비즈니스 CRUD를 모두 담당하는 범용 백엔드가 아니다.
- 모바일 텔레메트리의 인증, 계약 검증, 멱등성, receipt만 책임진다.
- Firebase ID token과 App Check를 확인한 뒤 tenant membership을 서버에서 결정한다.
- 동일한 `tenant_id + idempotency_key`는 동일한 결과를 반환한다.
- 수집 시각과 기기 발생 시각을 분리한다.
- raw sample은 Firestore 개별 문서가 아니라 압축 Storage object로 저장한다.

### Data Platform

- Firestore는 기관·기기·수리·점검·동의·receipt·현재 projection을 제공한다.
- Cloud Storage는 pseudonymous raw batch를 보관하고 lifecycle rule을 적용한다.
- BigQuery는 분석 필요가 증명된 뒤 날짜 partition과 tenant/device clustering으로 활성화한다.
- 개인 식별정보와 기기·수리·위치 이벤트를 논리적으로 분리한다.
- 원본 위치에는 TTL을 적용하고 파생 집계의 계보를 보존한다.
- immutable event와 삭제 대상 개인정보를 같은 저장 정책으로 취급하지 않는다.
- 현재 상태는 projection이며 원 이벤트를 수정하지 않는다.

### ML

- 첫 모델은 안전 판단이 아니라 데이터 품질·이동 유형 보조 판별을 수행한다.
- 신뢰성 모델은 향후 일정 기간의 점검 필요 위험을 추정한다.
- 학습 데이터, feature version, model version, threshold를 inference 결과에 저장한다.
- 데이터 부족 또는 distribution shift 시 abstain한다.

### Report Agent

- 모델 값을 다시 계산하지 않는다.
- 허용된 fact ID만으로 사용자·수리사·복지관별 설명을 구성한다.
- validator를 통과하지 못한 문장은 삭제하거나 `확인 필요`로 표시한다.
- 생성 결과와 최종 사람 검토 결과를 모두 보존한다.

## 4. 핵심 신뢰 경계

1. 모바일 기기 입력은 신뢰하지 않고 Auth·App Check·스키마·시간·속도·정확도를 검증한다.
2. tenant ID는 클라이언트 주장만 믿지 않고 Firebase membership의 권한으로 재결정한다.
3. 관리자 콘솔은 원본 좌표를 기본 표시하지 않는다.
4. 모델 결과와 사람의 수리 판단은 서로 다른 provenance를 가진다.
5. LLM 출력은 데이터베이스 변경 명령으로 직접 사용하지 않는다.

## 5. 핵심 관측 지표

- 모바일 수집 성공률, 권한 상태, 배터리 소모
- 미동기화 이벤트 수와 최고 대기시간
- batch 거부율, 중복률, p95 수신시간
- GPS accuracy 분포와 필터 탈락률
- projector lag와 재처리 실패
- 모델 입력 누락률, abstention, calibration drift
- 보고서 schema 유효율, 근거 지원 precision
- tenant 접근 거부와 민감 데이터 조회 감사로그

## 6. 배포 단계

초기에는 Firebase control plane, Cloud Storage, scale-to-zero Go Cloud Run만 운영한다. BigQuery는 분석 요구가 확인된 뒤 활성화한다. 데이터 규모와 운영 증거가 나오기 전에는 PostgreSQL/PostGIS, Kafka, Kubernetes를 도입하지 않는다. 확장보다 실패 복구, 비용 가시성, 재현성을 먼저 증명한다.
