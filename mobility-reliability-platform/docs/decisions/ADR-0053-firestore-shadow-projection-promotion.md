# ADR-0053 — Firestore shadow projection을 authoritative current state로 원자 승격한다

- 상태: accepted / local Firestore Emulator·Rules 검증
- 결정일: 2026-08-13
- 영향 범위: R09 device current-state projection, server-only shadow·checkpoint promotion
- 선행 결정: [ADR-0052](./ADR-0052-device-current-state-deterministic-replay.md)

## 맥락

R09 선행 증분은 normalized event를 pure projector로 재생해 state checksum을 만드는 범위까지다. 다음 단계에서 결과를 Firestore에 쓰려면 replay 도중 current state를 부분적으로 노출하거나 checkpoint만 먼저 전진시키지 않아야 한다. shadow 결과, authoritative device state, per-device checkpoint가 서로 다른 replay를 가리키면 이후 재처리와 감사가 불가능해진다.

## 계획하는 결정

projector는 먼저 server-only shadow projection을 생성한다. shadow에는 최소한 다음 binding을 포함한다.

- `tenantId`, `deviceId`, `projectorName`, `projectorVersion`
- `replayRunId`, `asOf` 또는 replay cutoff
- normalized input event count/hash와 output canonical checksum
- 마지막 event cursor 또는 `(occurredAt, sourceEventId)` 경계
- 계산된 device state와 invariant 결과

shadow 검증이 끝나면 하나의 Firestore transaction에서 authoritative device `state/current`와 해당 device checkpoint를 함께 승격한다. transaction은 expected current binding/revision을 다시 읽고, shadow의 tenant·device·projectorVersion·replayRunId·inputHash·outputChecksum이 모두 일치할 때만 current와 checkpoint를 갱신한다. 승격 실패 시 current와 checkpoint 모두 변경하지 않는다.

## 승격·멱등성 규칙

- 동일 replay binding과 동일 output checksum의 재시도는 이미 승격된 결과를 반환하는 idempotent no-op이다.
- 동일 `replayRunId` 또는 promotion key에 다른 input hash, output checksum, projector version, device/tenant binding이 들어오면 `binding_conflict`로 fail-closed하고 write하지 않는다.
- shadow 문서 누락, schema/version 불일치, checksum 불일치, checkpoint drift, tenant/device 불일치, corrupt state는 promotion을 거부하고 current pointer를 유지한다.
- stale projector version 또는 expected revision을 가진 동시 승격은 transaction conflict로 닫는다. 승자만 current와 checkpoint를 전진시킨다.
- checkpoint는 current state가 가리키는 동일 projector version·replay run·input boundary를 기록해야 하며 checkpoint만 단독으로 전진하지 않는다.
- exact replay는 external side effect를 만들지 않는다. FCM, SMS, 외부 API, 수리 command, 지원금 ledger, 사용자 알림을 실행하지 않는다.

## Firestore 보안 경계

- shadow projection, authoritative device current state, per-device checkpoint와 promotion metadata는 server-only write path다.
- beneficiary, guardian, repairer와 일반 console client는 이 원장에 직접 write할 수 없다.
- client read가 허용되는 경우에도 role-purpose DTO를 통해 필요한 상태만 제공하며 shadow, input event, checkpoint binding, raw GPS와 internal checksum payload를 직접 읽지 않는다.
- Firestore Rules는 client direct write를 deny하고, service/server transaction만 승격을 수행한다. Rules 변경과 production 배포는 별도 검증 gate다.

## 대안과 기각 이유

1. **current를 먼저 쓰고 checkpoint를 나중에 쓰기** — 중간 실패 시 상태와 cursor가 서로 다른 replay를 가리킨다.
2. **shadow 없이 current를 in-place 부분 갱신** — corrupt/late event가 사용자에게 부분 상태로 노출되고 rollback 기준이 없다.
3. **checkpoint만 보고 이미 처리했다고 판단** — checkpoint binding과 current checksum의 불일치를 숨긴다.
4. **동일 replay를 매번 새 결과로 기록** — retry가 중복 promotion과 불필요한 side effect를 만든다.
5. **client가 current state를 직접 갱신** — 권한·tenant·event lineage를 우회하고 기기 상태를 위조할 수 있다.

## 계획된 검증

- shadow write 후 current와 checkpoint가 한 transaction에서 함께 승격되는지 확인한다.
- 동일 replay를 반복해도 authoritative state·checkpoint·promotion 결과가 하나로 수렴한다.
- binding conflict, corrupt shadow, checksum drift, stale revision, concurrent promotion에서 current/checkpoint write가 0인지 확인한다.
- current 승격 전 shadow는 client read/write에 노출되지 않고, client direct write가 Rules Emulator에서 거부되는지 확인한다.
- promotion retry와 replay mode에서 FCM/SMS/외부 API/도메인 명령/지원금 변경이 0인지 확인한다.
- local synthetic/Firestore Emulator에서 먼저 검증하고 production·field 결과로 확대하지 않는다.

## 구현 결과와 한계

server-only adapter가 bounded `deviceStateEvents`를 strict decode해 pure projector를 실행하고, deterministic shadow를 create-once로 준비한 뒤 Firestore transaction에서 `devices/{deviceId}/state/current`와 per-device checkpoint를 함께 승격한다. exact replay는 revision을 늘리지 않고 `replayed`로 수렴하며, 입력 변경·shadow checksum 변조는 두 authoritative 문서를 쓰지 않고 거부한다. Rules Emulator에서는 모든 client의 source/shadow/checkpoint read/write를 막고 case worker·tenant admin만 current state를 read-only로 볼 수 있음을 확인했다.

이는 local Emulator 구현이며 production Firebase 배포·기관 사용을 증명하지 않는다. shadow retention, promotion ledger 보존, rollback/replay runbook, Cloud Tasks/Pub/Sub 운영 worker와 비용은 후속 설계·검증 대상이다.
