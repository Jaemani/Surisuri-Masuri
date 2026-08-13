# ADR-0058 — 합성 reliability 비교 화면은 검증된 aggregate artifact만 읽는다

- 상태: accepted / local synthetic web 구현 검증
- 결정일: 2026-08-13
- 영향 범위: R10 presentation contract, ML artifact, 복지관 콘솔 보고서
- 선행 결정: [ADR-0055](./ADR-0055-reliability-baseline-result-contract.md), [ADR-0056](./ADR-0056-r10-synthetic-reliability-baseline.md)

## 맥락

R10 evaluator 결과를 발표 화면에 옮길 때 값을 다시 입력하면 데이터셋이나 evaluator가 바뀐 뒤 화면만 과거 수치를 유지할 수 있다. 또한 train에서 정한 규칙·생존곡선과 untouched test에서 계산한 지표를 한 출처처럼 보이면 leakage 또는 validation tuning으로 오해될 수 있다.

## 결정

- `reliability-comparison-artifact.v1`을 identity-free, aggregate-only, read-only 파생 계약으로 둔다.
- 학습 규칙과 Kaplan–Meier curve는 train aggregate에서, sensitivity·specificity·Brier는 untouched synthetic test aggregate에서 가져온다. validation은 tuning이나 metric에 사용하지 않는다.
- artifact는 dataset hash와 baseline result hash, 자체 canonical hash를 갖는다. dataset 또는 result로 provenance를 검증할 때는 둘을 함께 요구하고 metric·count·curve를 원 결과와 대조한다.
- 콘솔은 evaluator가 생성한 고정 artifact JSON을 읽어 cohort·metric·abstention을 표시하며 숫자를 별도로 복사하지 않는다.
- 화면은 synthetic-only, production/field/per-device/safety decision false와 deployment defer를 지속적으로 노출하며 운영 CTA를 제공하지 않는다.
- JSON Schema는 wire shape를, Python semantic validator는 합계·중복 component·curve 단조성·lineage와 metric binding을 책임진다.

## 검증과 한계

계약 valid/invalid fixture, deterministic builder와 tamper tests, 콘솔 typecheck/test/build, Playwright console 5 flows 및 1440×1024 snapshot을 local WSL2에서 검증했다.

이는 실제 수리·주행 데이터, field calibration, 개별 기기 판단, 기관 사용자 테스트, Firebase 또는 production 배포 증거가 아니다. committed JSON은 재현 가능한 local synthetic snapshot이며 데이터셋이나 evaluator가 바뀌면 생성·hash 검증 후 함께 갱신해야 한다.
