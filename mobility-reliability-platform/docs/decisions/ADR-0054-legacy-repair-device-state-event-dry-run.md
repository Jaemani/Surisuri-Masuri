# ADR-0054 — 검증된 레거시 완료 수리를 device-state event로 dry-run 변환한다

- 상태: accepted / local synthetic dry-run 구현 검증
- 결정일: 2026-08-13
- 영향 범위: R09 legacy repair bridge, `device-state-event.v1` 입력 준비
- 선행 결정: [ADR-0052](./ADR-0052-device-current-state-deterministic-replay.md), [ADR-0053](./ADR-0053-firestore-shadow-projection-promotion.md)

## 맥락

기존 수리 데이터는 신규 device current-state projector의 입력 후보가 될 수 있지만, 레거시 ID와 신규 UUID를 추정으로 연결하거나 누락된 날짜·기기·분류를 기본값으로 채우면 잘못된 기기 상태가 만들어진다. 따라서 첫 bridge는 verified completed legacy repair만 대상으로 하고, 실제 Firestore import가 아닌 변환·검역·대조 dry-run으로 제한한다.

## 계획하는 변환 경계

- 기존 mapper의 `accepted`를 verified로 간주하지 않는다. 별도 `verifiedSourceIds` 승인 집합에 포함된 레거시 수리 record만 허용한다.
- source tenant/device/repair를 신규 entity UUID에 연결하는 explicit crosswalk를 필수로 한다. fuzzy name, 공개 코드 추정, 날짜 근접성 또는 임의 fallback으로 UUID를 만들지 않는다.
- target event ID는 manifest와 source identity의 canonical tuple에서 deterministic UUID로 생성한다. repair UUID는 별도 explicit crosswalk를 요구하며 임의 생성하지 않는다.
- target은 기존 `device-state-event.v1`의 normalized repair event(`repair.recorded`) 형태로만 만든다. event에는 verified device linkage와 유효한 occurrence time이 있어야 한다.
- 명시적 part/component linkage가 없는 레거시 수리 항목에서 `PartInstalled`·`PartRemoved`를 만들지 않는다.

## Quarantine와 제외

다음 record는 출력 event를 만들지 않고 reason code와 source identity만 가진 quarantine 결과로 분류한다.

- explicit UUID crosswalk가 없음·중복·충돌
- occurrence date가 없거나 파싱 불가·허용 범위 밖
- device UUID가 없거나 tenant/device crosswalk와 불일치
- verified completed가 아님
- category/action mapping이 없거나 여러 target으로 모호함
- source record identity·tenant·mapping version이 불완전함

날짜를 현재시각으로, 기기를 `unknown device`로, category를 `other`로 자동 대체하지 않는다. quarantine 출력에도 원문 증상·자유 메모·PII·금액·수리사 UID·계좌 정보·GPS를 복사하지 않는다.

## Dry-run과 reconciliation

- 실행 결과에 `dryRun: true`, `writeApplied: false`, `deploymentApplied: false`를 고정한다.
- 실행 결과는 source count, eligible count, emitted event count, quarantined count, duplicate count, reason별 count와 input/output canonical hash를 포함한다.
- source record ID, crosswalk UUID, deterministic event ID, mapping version 간 lineage만 redacted report에 남긴다.
- 동일 source manifest와 동일 mapping version을 다시 실행했을 때 event ID·결과 count·canonical hash가 같아야 한다.
- source eligible + quarantined + duplicate/disposition count가 입력 total과 설명 가능하게 일치해야 한다. 불일치는 dry-run 실패다.
- dry-run은 Firestore, Cloud Storage production path, device current, shadow, checkpoint에 write하지 않는다. 결과 파일은 로컬 검토 artifact로만 저장한다.

## 대안과 기각 이유

1. **레거시 ID를 신규 UUID처럼 사용** — tenant 경계를 넘거나 신규 entity와 충돌할 수 있다.
2. **누락 날짜를 현재시각으로 보정** — 시간순 replay와 `asOf` 결과를 오염시킨다.
3. **매칭 실패를 unknown device/category로 편입** — 잘못된 기기 상태를 숨기고 reconciliation을 깨뜨린다.
4. **원문 수리 메모·금액을 event에 복사** — current-state 입력 범위를 넘어 PII·민감 운영정보를 확산한다.
5. **dry-run 중 Firestore에 provisional event 쓰기** — 실패·재실행 시 partial import와 실제 상태 오인을 만든다.

## 계획된 검증

- valid verified completed fixture가 explicit UUID crosswalk를 통해 deterministic event로 변환된다.
- unknown date/device/category, crosswalk conflict, duplicate source ID는 quarantine되고 event output에 포함되지 않는다.
- component linkage 없는 category/action은 component event를 만들지 않는다.
- raw text, PII, money, UID, GPS가 event와 report에 포함되지 않는다.
- 동일 실행 재현 시 event ID·count·hash가 동일하고, input mutation은 drift로 탐지된다.
- `dryRun=true`, `writeApplied=false`, `deploymentApplied=false`를 실행 결과에서 확인한다.

## 구현 결과와 한계

기존 mapper 결과를 입력으로 받되 device·repair UUID crosswalk, verified source 승인, 의미가 확인된 recordedAt을 다시 검증하는 strict bridge를 구현했다. 동일 입력은 같은 event ID·hash·result를 만들고, missing crosswalk·invalid date·unknown category·unverified amount·conflicting duplicate·unverified source·missing recordedAt은 event 없이 reason count로 격리된다. 결과 계약은 원본 record·PII·자유문을 허용하지 않는다.

이는 합성 record를 사용한 local dry-run이며 실제 레거시 export나 이관 건수를 뜻하지 않는다. Firestore write, current-state 반영, 복지관 사용과 field 성과도 수행·증명하지 않는다.
