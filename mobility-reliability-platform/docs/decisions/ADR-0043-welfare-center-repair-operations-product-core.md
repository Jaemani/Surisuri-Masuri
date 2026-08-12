# ADR-0043 — 복지관 중심 수리 운영을 제품의 핵심으로 둔다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: 모바일 정보구조, 복지관 콘솔, 도메인 계약, 레거시 이관, 5–12월 로드맵

## 맥락

초기 신규 구현은 모바일 GPS 수집, 오프라인 동기화, telemetry gateway와 품질 모델에 집중했다. 이 기반은 기술적으로 필요하지만 사용자가 서비스를 찾는 이유와 복지관의 실제 업무를 대표하지 않는다. 과거 프로젝트의 사용자·기기·QR·수리·지원금·수리소 데이터도 GPS 모델만을 위해 이관하는 것이 아니다.

제품이 이어야 하는 실제 흐름은 사용자의 증상 접수부터 수리소 작업, 복지관의 공적 지원 검증, 완료 이력과 다음 예방점검까지다. 수리 요청과 완료 기록, 청구금액과 지원금 집행을 분리하지 않으면 진행 상태와 예산 책임을 추적할 수 없다.

## 결정

> 전동보장구 사용자의 수리 요청부터 수리소 작업, 복지관 검증, 보조금 집행, 예방점검까지 연결하는 복지관 중심 운영 플랫폼

- 하나의 모바일 코드베이스가 사용자·보호자·수리사에게 역할별 화면을 제공한다.
- 사용자·보호자·복지관은 수리를 요청할 수 있고, 수리사가 작업을 제출하며, 공적 지원 건은 복지관이 검증한다.
- 보조금은 잔액 하나가 아니라 allocation·reservation·execution·release·reversal·adjustment 거래 원장으로 관리한다.
- 수리소 배정은 기관 정책별로 `center_assigned` 또는 `user_selectable`을 지원하며 기본값은 `center_assigned`다.
- GPS와 AI는 사용량·이력에 근거한 예방점검을 보조한다. 위치 동의를 거부해도 수리·지원금 기능을 사용할 수 있다.
- 레거시 데이터는 사용자·기기·QR·수리 타임라인·수리소 운영 연속성을 위한 일회성 변환 입력이다.

## 대안과 기각 이유

- GPS 수집 앱 중심: 기술 데모는 명확하지만 사용자의 수리 문제와 기관 업무를 해결하지 못한다.
- 복지관 콘솔만 추가: 수리 요청·진행 확인·지원금 설명이 사용자에게서 단절된다.
- 수리비 기반 잔액 계산만: 예약·취소·조정과 본인부담을 감사할 수 없다.

## 결과

- telemetry 구현은 유지하되 모바일 주 화면에서 설정·자동 근거 레이어로 이동한다.
- `repairWorkOrders`, 완료 `repairs`, `subsidyAccounts/transactions`, `deviceAssignments`를 분리한다.
- 5–8월은 제품 운영 vertical slice와 GPS 기반을 병렬 구축한다.
- 기존 GPS 중심 UI 방향은 이 ADR과 충돌하는 범위에서 superseded다.

## 검증

- 계약: `repair-work-order.v1`, `subsidy-ledger-transaction.v1`, `legacy-import-record.v1`
- 상태기계: `@mobility-reliability/domain-workflows`
- 권한: beneficiary own work order, assigned repairer job, case-worker queue와 ledger의 Firebase Emulator test
- 제품 화면은 별도 Product Update와 Evidence 문서에 연결한다.
