# ADR-0051 — 복지관 콘솔도 완료 수리 archive를 read-time replay한다

- 상태: accepted / local·Emulator·synthetic 구현 검증
- 결정일: 2026-08-13
- 영향 범위: R09 기기 타임라인, 복지관 콘솔 기기 상세·수리 운영 projection
- 선행 결정: [ADR-0050](./ADR-0050-completed-repair-timeline-replay.md)

## 맥락

모바일 사용자 화면은 완료된 수리 사실을 mutable `repairWorkOrders`가 아니라 검증 완료된 `repairs/{repairId}`와 구조화된 `items`에서 재생하도록 설계됐다. 복지관 콘솔도 같은 기기 이력을 보여줘야 하지만, 운영자 화면에만 별도의 임의 상태·자유문자·수리사 식별자를 조합하면 사용자 화면과 사실 기준이 달라지고, 아직 완료되지 않은 작업을 완료 이력처럼 보일 위험이 있다.

## 결정 후보

콘솔의 기기 관리 상세에서 목적 제한된 `timeline`을 제공한다.

- 입력은 요청 tenant의 해당 `device_id`에 속한 completed repair header와 nested `items`다.
- `status=completed`, `source_quality=verified`인 문서만 허용한다.
- tenant·device·repair/item identity, `occurred_at`, category/action/quantity를 read-time에 다시 검증한다.
- 입력 순서와 무관하게 `(occurred_at, repair_id, repair_item_id)` 순서로 replay하고 canonical checksum을 계산한다.
- 콘솔 DTO에는 공개 기기 코드, 완료일, 구조화된 수리 항목과 항목 수만 포함한다. canonical checksum은 replay 결정성 검증 내부에 유지하고 화면 DTO에는 노출하지 않는다.
- 원 증상 자유문, repairer Firebase UID, subsidy account, raw GPS, Storage object path, 부품 설치·제거를 추정하는 필드는 포함하지 않는다.
- 진행 중·반려·취소 work order는 완료 수리 timeline에 넣지 않고, 기존 수리 운영 queue에서 별도로 보여준다.
- client는 이 projection을 쓰지 못하며, server projection/read boundary가 tenant와 역할을 확인한다.

`repairs/{repairId}/items`에 명시적인 part/component linkage가 없는 경우 category/action만 표시한다. `replace`라는 action만으로 부품 설치 상태를 생성하거나 현재 부품을 추정하지 않는다.

## 대안과 기각 이유

1. **현재 mutable work order를 그대로 표시** — 현재 운영 상태와 완료 이력을 혼합하고 수정·재개방 시 과거 화면이 변한다.
2. **콘솔 전용 임의 timeline 문구 저장** — 모바일과 사실 기준이 분리되고 원본 자유문·민감정보 복제 위험이 있다.
3. **component installation을 category/action에서 추정** — 명시적 부품 식별자·설치 시각·교체 관계가 없어 잘못된 maintenance state를 만들 수 있다.
4. **async current projection을 이번 증분에 함께 도입** — read-time 사실 연결과 worker/checkpoint/replay 운영을 한 번에 섞어 실패 경계를 확인하기 어렵다.

## 검증 계획

- 같은 header/items를 순서를 바꿔 읽어도 DTO 순서와 checksum이 같다.
- 다른 tenant/device, duplicate identity, invalid code/quantity, unverified source는 write 없이 fail-closed한다.
- console DTO serialized output에 raw issue, UID, subsidy account, GPS, Storage path가 없다.
- completed repair가 없을 때는 빈 timeline과 명시적인 상태를 반환하며 mutable work order를 완료 이력으로 승격하지 않는다.
- Emulator와 console web visual 검증은 local/synthetic 범위로 분리한다.

## 구현 결과와 한계

서버의 `devices` projection이 기기별 완료 repair header와 nested items를 bounded read하고 기존 replay 경계를 재사용한다. 콘솔 repository는 timeline 계약을 strict decode하며, 기기 관리 화면은 목록에서 기기를 고른 뒤 현재 상태·완료 수리 이력·예방점검/수리 운영 CTA를 한 화면에 표시한다. Firestore Emulator와 local web Playwright에서 목적 제한 DTO와 화면 상태를 확인했다.

이는 production 배포나 실제 기관 사용을 의미하지 않는다. 데모 repository의 점검·접수·등록 항목은 합성 화면 상태이고, 서버 authoritative timeline은 verified completed repair만 반환한다. async current projection, legacy import, component lifecycle, GPS·inspection 통합, production Firebase와 field evidence는 후속 gate다.
