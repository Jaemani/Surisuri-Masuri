# ADR-0045 — 수리 제출 근거를 구조화된 작업 항목으로 저장한다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: 수리사 제출, 복지관 검증, 완료 수리 이력, 향후 신뢰성 모델 feature

## 맥락

기존 수리사 제출에는 청구액만 있어 복지관이 “수리 결과를 확인했다”고 판단할 근거가 없었다. 자유 메모만 추가하면 부위·작업 분류를 집계하기 어렵고 개인정보가 완료 이력과 모델 데이터로 복제될 위험이 있다.

## 결정

수리사 제출에는 1~20개의 구조화된 `workItems`가 필요하다.

- `categoryCode`: wheel_tire, battery, brakes, controls, seat_frame, other
- `actionCode`: inspect, adjust, repair, replace
- `quantity`: 1~20 정수
- `lineAmountKrw`: 0~100,000,000원 정수

항목에는 자유문자, 고객정보, 부품 serial, 지원금 정보를 받지 않는다. 항목 금액 합계는 청구액과 정확히 같아야 한다. 제출 work order에 저장된 항목은 복지관 검증과 완료 처리 후 `/repairs/{repairId}/items/{itemId}`에 검증된 이력으로 함께 materialize한다.

## 결과

- 복지관은 제출된 작업 종류와 금액 구성을 확인할 수 있다.
- 완료 이력은 예방정비·신뢰성 분석의 구조화 feature가 된다.
- 자유문자 redaction 파이프라인 전까지 PII가 수리 항목에 유입되지 않는다.
- 구체 부품 카탈로그·설치/제거 component linkage는 후속 계약으로 남는다.

## 검증

- domain unit/typecheck
- Firestore Emulator 전체 lifecycle
- 완료 repair item의 category/action/quantity/amount와 raw detail 부재 확인

