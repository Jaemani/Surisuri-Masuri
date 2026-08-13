# HR-20260813-25 — report claim 저장 경계 검토

- 대상 기간: 2026-08-13 local increment
- 작성자: Codex 구현 초안
- 검토자: 프로젝트 책임자 확인 대기
- 로드맵: 10월 R12 persistence
- 상태: generated / 사람 검토 대기

## 실제 변경

보고서 claim을 직접 읽을 때 tenant field만 보지 않고 path의 report/claim ID, 부모 report 존재, 부모와 claim의 Fact bundle hash까지 함께 검사한다. 정상 결합 1건은 허용하고 wrong tenant/report/claim/bundle과 orphan 5종을 거부하는 Emulator 회귀를 추가했다.

## 사람 검토 요청

- console direct read를 유지할지, 향후 purpose-limited backend DTO로 완전히 닫을지
- production writer에서 create-only transaction과 immutable Fact content hash를 어떤 명령 경계로 둘지
- 사람 승인과 발행 provenance를 Firebase UID가 아닌 제한된 server envelope에 어떻게 기록할지

근거는 [ADR-0064](../../decisions/ADR-0064-report-claim-persistence-binding.md), [UPD-20260813-34](../../product-updates/UPD-20260813-34-report-claim-rules-binding.md), [EVD-20260813-030](../../evidence/2026-08-product.md#evd-20260813-030--report-claim-tenantparent-binding)이다. production 배포나 기관 승인을 뜻하지 않는다.
