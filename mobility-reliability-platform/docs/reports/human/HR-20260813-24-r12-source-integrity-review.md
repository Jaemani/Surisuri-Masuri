# HR-20260813-24 — R12 source 무결성 경계 검토

- 대상 기간: 2026-08-13 local increment
- 작성자: Codex 구현 초안
- 검토자: 프로젝트 책임자 확인 대기
- 로드맵: 10월 R12 Fact Store·report agent
- 상태: generated / 사람 검토 대기

## 실제 변경

보고서 생성기가 source assessment에 적힌 hash 문자열을 그대로 계보 증거로 복사하던 경계를 닫았다. 이제 전체 source 내용을 다시 hash하고, 합성 전용·test tuning 금지·배포 보류·개별 조치 금지·세 부품 판단 유보 계약을 Fact 생성 전에 확인한다.

## 사람 검토 요청

- `hash 검증`이 작성자의 진실성 인증이 아니라 운반 중 변조 탐지라는 설명이 분명한지
- Python schema/dataset binding과 Node report boundary의 책임 구분이 유지되는지
- production 서명·Firebase Fact Store가 아직 없다는 제한이 외부 발표에서 누락되지 않는지

근거는 [ADR-0062](../../decisions/ADR-0062-r12-source-assessment-integrity.md), [UPD-20260813-32](../../product-updates/UPD-20260813-32-r12-source-integrity.md), [EVD-20260813-028](../../evidence/2026-08-product.md#evd-20260813-028--r12-source-assessment-self-hash-gate)이다. 실제 회의·기관 승인·production 배포를 의미하지 않는다.
