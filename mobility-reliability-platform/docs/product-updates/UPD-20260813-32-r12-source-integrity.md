# UPD-20260813-32 — R12 source assessment 무결성 gate

- 기준일: 2026-08-13
- 상태: implemented-local / synthetic-only
- version_or_deployment: `report-evidence` local package / production 배포 없음
- 대상 사용자: 보고서 파이프라인 개발자·검토자
- 로드맵 위치: 10월 R12 Fact Store·report agent 선행 increment

R12 builder가 R11 assessment의 self-hash를 직접 재계산하고 목적·분할·판단 유보 계약 전체를 다시 검사하도록 강화했다. 입력 count 변조와 test tuning 허용 변조를 회귀 테스트로 고정했으며 report-evidence test는 7개에서 9개로 늘었다.

화면과 report snapshot은 바뀌지 않았다. 이 변경은 source artifact의 local 구조·무결성 검증이며 작성자 인증, 실제 Firebase artifact, 현장 데이터 또는 production 발행을 증명하지 않는다.

관련: [ADR-0062](../decisions/ADR-0062-r12-source-assessment-integrity.md), [EVD-20260813-028](../evidence/2026-08-product.md#evd-20260813-028--r12-source-assessment-self-hash-gate)
