# ADR-0052 — 기기 current state는 정규화 event를 결정론적으로 replay한다

- 상태: accepted / pure projector·contract local 검증
- 결정일: 2026-08-13
- 영향 범위: R09 Device Timeline & Reliability, device current-state projection
- 선행 결정: [ADR-0050](./ADR-0050-completed-repair-timeline-replay.md), [ADR-0051](./ADR-0051-console-completed-repair-timeline-read-time-replay.md)

## 맥락

현재 모바일과 복지관 콘솔의 완료 수리 timeline은 bounded read-time replay 범위다. 다음 R09 증분에서는 특정 시점의 기기 상태를 복원할 수 있는 server-only current projection을 설계한다. 단순히 최신 문서를 덮어쓰면 늦게 도착한 수리·점검·주행 요약이 과거 상태를 잘못 덮고, projector를 변경했을 때 결과의 재현 여부도 확인하기 어렵다.

## 계획하는 결정

정규화된 event 집합을 canonical order로 정렬해 current-state value를 재구성한다. 이번 첫 slice의 projector는 다음 입력만 사용한다.

- `repair.recorded`: 검증 완료 수리 archive에서 정규화한 수리 reference
- `part.replaced`: repair와 component의 명시적 linkage가 확인된 경우에만 생성되는 부품 교체 event
- `InspectionCompleted`: 점검 header와 observation에서 정규화한 완료 점검 사실
- `TripSummarized`: raw GPS가 아닌 승인된 trip summary의 사용량·거리·품질 사실

category/action, `replace` action, 수리 메모, 주행량만으로 `PartInstalled`나 `PartRemoved`를 추정하지 않는다. 명시적 part/component ID와 설치·제거 시각이 없으면 부품 상태는 `unknown` 또는 `not_linked`로 남긴다.

## 결정론적 replay 규칙

- `device-state-event.v1` JSON Schema와 projector가 동일한 exact field/payload 계약을 사용하며 tenant·device·event UUID, 시간 순서와 `sourceQuality=verified`를 검증한다.
- 입력 순서와 도착 순서는 신뢰하지 않고 `(occurred_at, event_type_order, source_event_id)` canonical order로 정렬한다. 동일 시각의 우선순위와 tie-break는 projector version에 고정한다.
- `asOf`가 있으면 `occurred_at <= asOf`인 event만 포함한다. 이후 event는 상태와 checksum에 영향을 주지 않는다.
- out-of-order event는 current state를 부분 수정하지 않고 해당 device의 shadow replay를 다시 수행한 뒤 결과를 교체한다.
- pure 결과에 projector version, event count, last occurred/event ID, output canonical checksum을 포함한다.
- 기존 pointer는 shadow state의 count·hash·invariant 검증이 끝난 뒤에만 원자적으로 전환한다. replay 실패·unknown event·검증 불일치 시 기존 current state를 유지하고 새 pointer를 쓰지 않는다.
- replay mode에서는 FCM, SMS, 외부 API, 수리 명령, 지원금 원장 변경을 실행하지 않는다.

## 상태와 개인정보 경계

current state에는 기기 상태, 마지막 verified repair/inspection reference, 명시적으로 연결된 component 상태, 집계된 trip usage/quality와 data sufficiency만 둔다. raw GPS sample, 좌표, 출발·도착 위치, Storage object path, PII, repairer UID, subsidy account는 state·checkpoint·checksum payload·일반 로그에 넣지 않는다.

`TripSummarized`는 거리·시간·품질 등 요약 필드만 전달하며, 위치 원본의 존재를 current state가 의미하지 않는다. 정밀 동선은 이 projection의 입력이나 출력이 아니다.

## 대안과 기각 이유

1. **최신 event 하나로 상태를 덮어쓰기** — out-of-order 도착과 정정 event에 취약하고 as-of 재현이 불가능하다.
2. **mutable work order/device 문서를 직접 현재 상태로 사용** — verified history와 운영 중인 작업을 혼합하며 변경 이력이 사라진다.
3. **category/action에서 부품 lifecycle 추정** — 명시적 component linkage가 없어 잘못된 설치·제거 상태를 만든다.
4. **raw GPS를 current projection에 복사** — 목적 범위를 넘는 위치 노출과 저장 비용을 만든다.
5. **checkpoint 없이 매번 최신 상태를 계산** — 재처리 경계와 projector version 차이를 추적하기 어렵다.

## 계획된 검증

- 동일 event 집합을 순서·batch로 섞어도 output state와 checksum이 동일하다.
- `asOf` 경계 전·후 event, 동일 timestamp tie-break, out-of-order late event를 재현한다.
- projector version 변경 시 shadow output과 checkpoint가 분리되고, 검증 전 pointer가 바뀌지 않는다.
- tenant/device/event identity, source quality, schema version 오류는 quarantine/fail-closed한다.
- raw GPS 및 PII scan이 state·checkpoint·일반 로그에서 0건이다.
- explicit component linkage가 없는 repair item은 component state를 만들지 않는다.
- local synthetic/Emulator에서만 먼저 검증하며 production·field 결과로 확대하지 않는다.

## 구현 결과와 한계

`device-state-event.v1` 계약과 strict pure projector를 구현했다. 입력 순서 반전, 동일 시각 event ID tie-break, `asOf`, explicit component linkage, checksum, schema/source/identity/payload 오류와 raw-coordinate extra field 거부를 local synthetic test로 확인했다.

이번 slice는 Firestore current/shadow document write, replay run ID, input hash, atomic pointer/checkpoint worker를 아직 구현하지 않는다. 따라서 async current projection 완료를 주장하지 않는다. legacy import, 실제 trip ingest, inspection adapter, component command, production 배포·기관 사용과 reliability model도 별도 gate다.
