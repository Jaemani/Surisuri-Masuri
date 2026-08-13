# UPD-20260813-36 — unsupported claim 자동 제외

- 기준일: 2026-08-13
- 상태: implemented-local / synthetic-only
- version_or_deployment: R12 candidate validator local / production 배포 없음
- 대상 사용자: 보고서 파이프라인 개발자·검토자
- 로드맵 위치: 10월 R12 claim validator

근거 Fact ID는 존재하지만 문장이 “고장 확률 95%”로 바뀐 후보를 최종본에서 제외한다. 민감 key·foreign Fact가 있는 후보도 원문을 결과에 되돌려주지 않고 제외한다. report-evidence test는 17개다.

관련: [ADR-0066](../decisions/ADR-0066-unsupported-claim-disposition.md), [EVD-20260813-032](../evidence/2026-08-product.md#evd-20260813-032--unsupported-candidate-claim-disposition)
