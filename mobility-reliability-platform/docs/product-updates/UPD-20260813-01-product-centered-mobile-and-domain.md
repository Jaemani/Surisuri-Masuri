# UPD-20260813-01 — 제품 중심 모바일과 수리·지원금 도메인 기반

- 상태: generated / 사람 검토 대기
- 환경: local WSL2, deterministic demo data
- 배포 상태: 미배포
- 결정: [ADR-0043](../decisions/ADR-0043-welfare-center-repair-operations-product-core.md)

## 실제 변경

- 모바일 첫 화면을 GPS 개발 패널에서 사용자·보호자의 수리·기기·지원 경험으로 교체했다.
- 동일 코드베이스에 개발 검토용 역할 전환을 두고 수리사 작업 목록과 기기 확인 화면을 추가했다.
- GPS 기록은 유지하지만 홈의 주 목적이 아니라 이동 사용량을 제공하는 보조 카드와 설정으로 이동했다.
- 수리 work order, 지원금 ledger transaction, 레거시 import 결과의 JSON Schema 계약을 추가했다.
- 역할별 수리 상태 전이와 원장 projection을 독립 workflow package로 구현했다.
- Firestore Rules가 사용자 본인 수리 요청, 배정된 수리사의 작업, 복지관 운영 대기열과 원장 상세를 서로 다른 범위로 읽도록 확장됐다. 클라이언트 직접 쓰기는 계속 차단된다.

## 검증된 범위

- 모바일 TypeScript typecheck
- 모바일 telemetry 포함 unit test 229개
- 도메인 workflow Node test 6개
- 계약 valid/invalid fixture
- Firebase Firestore/Storage Emulator Rules test

검증 명령과 정확한 실행 결과는 [EVD-20260813-001](../evidence/2026-08.md#evd-20260813-001--제품-중심-모바일과-수리지원금-도메인-기반)에 기록한다.

## 제한

- 모바일의 사용자·기기·수리·지원금 값은 deterministic demo이며 실제 이용자 데이터가 아니다.
- 수리 요청 버튼 일부는 로컬 UI 상태만 바꾸며 Domain Command API에 연결되지 않았다.
- 수리사 QR 화면은 카메라 스캔과 서버 조회가 연결된 운영 기능이 아니다.
- Android/iPhone native build와 현장 접근성 검증은 이 업데이트의 증거가 아니다.
