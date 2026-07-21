# ADR-0004: 런타임과 언어 경계

- 상태: superseded
- 결정일: 2026-07-21
- 대체 결정: [ADR-0007](./ADR-0007-firebase-first-hybrid.md)

## 맥락

프로젝트는 모바일, 데이터 수집, 공간 처리, ML 학습, 기관 운영 UI를 포함한다. 하나의 프레임워크로 통합하면 시작은 쉽지만 각 계층의 실패와 평가 경계가 흐려진다.

## 결정

- 모바일: React Native + TypeScript, 필요 시 Kotlin/Swift native module
- 기관 콘솔: Next.js + TypeScript
- 텔레메트리 수집: Go
- 운영 DB: PostgreSQL + PostGIS
- ML 학습·평가: Python + PyTorch
- 온디바이스 모델: ONNX
- 서비스 간 계약: 버전이 고정된 JSON Schema에서 시작

## 경계 원칙

- Go 서비스는 텔레메트리 계약·인증·멱등성에 집중한다.
- 일반 기관 CRUD를 마이크로서비스로 분해하지 않는다.
- Python은 학습과 versioned inference artifact를 담당하고 요청 경로의 모든 비즈니스 로직을 소유하지 않는다.
- 공유 계약은 특정 ORM 모델을 노출하지 않는다.

## 대체 이유

초기 결정은 PostgreSQL/PostGIS를 운영 DB로 선택했지만, 프로젝트 소유자가 배포·관리 비용을 줄이기 위해 Firebase를 우선 활용한다는 운영 제약을 추가로 확정했다. 모바일·Go·Python 경계는 유지하되 저장·운영 구조는 ADR-0007이 대체한다.

## 결과

- 새 기술 경험과 명확한 평가 경계를 얻는다.
- 여러 toolchain의 재현성과 CI 구성이 필요하다.
- 규모 근거 없이 추가 런타임을 늘리지 않는다.
