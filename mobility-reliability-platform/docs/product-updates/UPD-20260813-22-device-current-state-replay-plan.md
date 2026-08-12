# UPD-20260813-22 — device current-state deterministic replay 계획

- 기준일: 2026-08-13
- 상태: implemented / contract·pure replay local synthetic 검증
- 로드맵 위치: R09 Device Timeline & Reliability
- 대상: server-only device current-state projection과 replay 운영 경계

## 제품·공학 변화

완료 수리, 명시적 부품 설치·제거, 완료 점검, raw GPS가 아닌 주행 요약을 정규화 event로 묶고, projector version과 checkpoint를 가진 deterministic replay로 기기 current state를 구성한다.

늦게 도착한 event와 `asOf` 조회를 지원하되, 기존 current pointer는 shadow replay 검증 전 변경하지 않는다. 명시적 part/component linkage가 없는 수리 항목은 부품 설치·제거로 추정하지 않으며 raw GPS는 state와 checkpoint에 포함하지 않는다.

## 완료 범위

- normalized `RepairLogged`, `PartInstalled`, `PartRemoved`, `InspectionCompleted`, `TripSummarized` event 경계
- canonical ordering, out-of-order replay, `asOf`, projector version, checkpoint, replay run ID
- output checksum와 input count/hash invariant
- Firestore shadow state·atomic pointer 전환은 후속 범위로 고정
- raw GPS·PII·UID·지원금 계정 미포함 및 component 추정 금지
- local synthetic/Emulator 중심의 replay·quarantine·privacy scan 증거

## 검증 결과와 경계

- contracts valid/invalid fixture: `device-state-event.v1`과 raw coordinate 거부 통과
- domain-command: 신규 projector test를 포함해 local 32 passed, 11 skipped
- 순서 반전 replay 결과·checksum 동일, `asOf`, 동일 timestamp tie-break 확인
- `repair.recorded`만으로 component를 만들지 않고 explicit `part.replaced`만 component state 갱신
- source quality, schema version, tenant/device/event identity, 시간·payload extra field fail-closed

이 결과는 local synthetic pure replay다. async worker, Firestore shadow/checkpoint/pointer, production 배포와 실제 복지관·현장 결과가 완료됐다는 뜻이 아니다.

관련: [ADR-0052](../decisions/ADR-0052-device-current-state-deterministic-replay.md), [R09](../reports/fixed/2026-09-15.md), [EVD-20260813-018](../evidence/2026-08-product.md#evd-20260813-018--device-current-state-deterministic-pure-replay)
