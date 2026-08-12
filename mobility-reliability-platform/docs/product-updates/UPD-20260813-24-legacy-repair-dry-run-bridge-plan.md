# UPD-20260813-24 — 레거시 완료 수리→device-state event dry-run bridge 계획

- 기준일: 2026-08-13
- 상태: implemented / local synthetic dry-run 검증
- 로드맵 위치: R09 Device Timeline & Reliability
- 대상: verified completed legacy repair의 신규 normalized event 변환 준비

## 제품·공학 변화

검증된 레거시 완료 수리만 explicit UUID crosswalk를 통과시켜 `device-state-event.v1`의 normalized repair event로 변환한다. event ID는 source identity와 mapping version에서 deterministic하게 만들고, 날짜·기기·category를 확인할 수 없는 record는 quarantine한다.

dry-run 결과에는 `dryRun=true`, `writeApplied=false`, `deploymentApplied=false`, disposition별 reconciliation count/hash와 event ID/hash만 남긴다. 원문 텍스트·PII·금액·UID·GPS는 제외하며, 명시적 component linkage 없는 레거시 수리에서 부품 설치·제거를 추정하지 않는다. Firestore와 current/shadow/checkpoint에는 write하지 않는다.

## 완료 범위

- verified completed legacy repair 입력 gate
- source→target explicit UUID crosswalk와 deterministic event ID
- unknown date/device/category 및 mapping conflict quarantine
- input/output count·canonical hash reconciliation
- dry-run artifact와 write/deployment false assertion
- Firestore write count 0 및 raw text/PII/money exclusion scan

## 검증 결과와 경계

- legacy importer 9 tests 통과: deterministic result, quarantine, verified evidence gate
- contracts 34 cases 통과: write/deployment와 source record extra field 거부
- mapper `accepted`만으로 verified event를 만들지 않음

실제 레거시 export, 변환 건수, Firestore import, device current 반영, production·복지관·field 결과가 완료됐다는 뜻이 아니다.

관련: [ADR-0054](../decisions/ADR-0054-legacy-repair-device-state-event-dry-run.md), [R09](../reports/fixed/2026-09-15.md), [EVD-20260813-020](../evidence/2026-08-product.md#evd-20260813-020--legacy-repair-device-state-event-dry-run-증거-계획)
