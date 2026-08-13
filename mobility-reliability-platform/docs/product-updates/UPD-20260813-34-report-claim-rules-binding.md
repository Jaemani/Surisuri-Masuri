# UPD-20260813-34 — report claim Rules binding

- 기준일: 2026-08-13
- 상태: implemented-local / Emulator verified
- version_or_deployment: Firestore Rules local / production 배포 없음
- 대상 사용자: 복지관 운영 콘솔 개발자·보안 검토자
- 로드맵 위치: 10월 R12 persistence 선행 increment

잘못된 backend/importer가 다른 tenant의 claim, orphan claim, 다른 report·claim ID 또는 다른 Fact bundle을 tenant 경로 아래 저장하더라도 client가 읽지 못하도록 Rules를 강화했다. 정상 부모·claim 결합은 유지되며 Rules test는 41개가 통과했다. report/claim 경로도 client write-denial 회귀 목록에 추가했다.

관련: [ADR-0064](../decisions/ADR-0064-report-claim-persistence-binding.md), [EVD-20260813-030](../evidence/2026-08-product.md#evd-20260813-030--report-claim-tenantparent-binding)
