# ADR-0050 — 사용자 기기 타임라인은 완료 수리 archive를 결정론적으로 replay한다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: R09 기기 타임라인, 모바일 beneficiary projection, 완료 수리 archive

## 맥락

완료된 수리와 구조화 작업항목은 이미 `repairs/{repairId}/items`에 불변 기록되지만, 모바일 기기 타임라인은 mutable `repairWorkOrders`를 화면용 문구로 바꾸고 있었다. 이 방식은 수리 결과의 검증 상태나 구조화 항목을 재생하지 못하고 현재 업무 상태와 과거 이력을 섞는다.

## 결정

- 진행 중 수리는 기존 수리 요청 카드가 담당한다.
- 기기 타임라인의 완료 수리는 `source_quality=verified`, `status=completed`인 repair header와 items만 읽는다.
- replay는 tenant·device·repair/item identity, 시간, category/action/quantity를 fail-closed 검증한다.
- 입력 순서와 무관하게 `(occurred_at, repair_id, repair_item_id)` 순으로 재생하고 canonical checksum을 만든다.
- 화면 projection은 구조화된 부위·처리·수량만 표시하며 원 증상, UID, 지원금, 위치, Storage path를 복사하지 않는다.
- 현재 단계는 bounded read-time replay다. async checkpoint/current-state worker, 부품 설치·제거 상태, legacy import, GPS·점검 이벤트 통합은 후속 범위다.

## 결과

사용자는 검증 완료된 수리 archive에서 재생된 기기 이력을 볼 수 있고 같은 입력은 같은 checksum과 순서를 만든다. 그러나 이를 전체 Digital Twin, 현장 수리 실적, 부품 lifecycle projection 또는 predictive reliability 완료라고 부르지 않는다.
