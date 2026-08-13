# UPD-20260813-33 — R12 완전한 Fact·claim profile

- 기준일: 2026-08-13
- 상태: implemented-local / synthetic-only
- version_or_deployment: `report-fact-bundle.v1`, `grounded-operations-report.v1` stricter validator / production 배포 없음
- 대상 사용자: 보고서 파이프라인 개발자·검토자
- 로드맵 위치: 10월 R12 Fact Store·report agent 선행 increment

합성 범위와 사람 검토 fallback을 제거한 축약 보고서, 임의 Fact/Claim ID, claim type 바꿔치기, source와 무관한 report ID를 거부한다. 최종 보고서는 정확히 다섯 Fact와 다섯 1:1 claim을 가져야 한다. report-evidence test는 12개다.

현재 UI snapshot의 내용은 이미 완전한 profile이어서 바뀌지 않았다. 실제 Firebase 저장, 사람 승인, 기관 발행과 LLM 실행을 증명하지 않는다.

관련: [ADR-0063](../decisions/ADR-0063-r12-complete-fact-claim-profile.md), [EVD-20260813-029](../evidence/2026-08-product.md#evd-20260813-029--r12-complete-factclaim-profile)
