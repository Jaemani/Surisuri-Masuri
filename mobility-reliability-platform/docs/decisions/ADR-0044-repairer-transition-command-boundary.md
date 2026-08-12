# ADR-0044 — 수리사 상태 변경을 단계별 최소 명령으로 제한한다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: 수리 command, Firestore work order, 수리사 모바일 workflow

## 맥락

기존 `transitionRepairRequest`는 모든 상태에서 배정 수리소·수리사 UID·지원금 참조·청구액·제출시각을 선택적으로 받았다. 상태기계가 전이를 제한하더라도, 배정된 수리사가 정상적인 `assigned → scheduled` 호출에 다른 수리사나 지원금 필드를 섞어 권위 데이터를 덮어쓸 수 있었다. 또한 `scheduled` 상태는 저장할 일정 필드가 없었고 `submittedAt`은 클라이언트가 정할 수 있었다.

## 결정

- 상태별 허용 필드를 정확한 allowlist로 검증하며 나머지는 `UNEXPECTED_COMMAND_FIELD`로 거부한다.
- 수리사가 수행하는 단계는 다음 최소 계약으로 제한한다.
  - 일정 확정: work order ID, revision, `scheduledAt`
  - 작업 시작: work order ID, revision
  - 비용 제출: work order ID, revision, `billedAmountKrw`
- `scheduledAt`은 ISO 시각으로 정규화하고 서버 현재시각 기준 15분 전부터 180일 후까지만 허용한다.
- `submittedAt`은 요청에서 받지 않고 서버의 command 처리시각으로 기록한다.
- `scheduled`, `in_progress`, `repairer_submitted` 전이는 복합 역할 여부와 무관하게 정확히 배정된 repairer UID만 수행한다.
- 구조화된 작업 항목 계약 전에는 이를 “수리 결과 보고서”가 아닌 “비용 제출”로 표시한다.

## 결과

- 모바일 adapter가 실수해도 수리사가 배정·지원금 권위 필드를 변경할 수 없다.
- 일정 UI가 실제 Firestore `scheduled_at`과 연결된다.
- 제출 증빙 시각은 클라이언트 시계나 조작에 의존하지 않는다.
- 복지관 검증·지원금 판단·완료 처리는 계속 기관 전용 권한으로 남는다.

## 검증

- domain command unit/typecheck
- Firestore Emulator command/projection 시나리오
- HTTP 경계의 수리사 재배정 필드와 client `submittedAt` 주입 거부

