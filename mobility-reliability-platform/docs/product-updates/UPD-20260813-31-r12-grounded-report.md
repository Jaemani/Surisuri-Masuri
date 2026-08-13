# UPD-20260813-31 — R12 근거 연결형 fallback 보고서

- 기준일: 2026-08-13
- 상태: implemented-local / synthetic-only
- version_or_deployment: `report-fact-bundle.v1`, `grounded-operations-report.v1` / production 배포 없음
- 대상 사용자: 복지관 콘솔 데모 검토자·개발자
- 로드맵 위치: 10월 R12 Fact Store·report agent 선행 increment

R11 assessment에서 5개 aggregate Fact를 만들고, 5개 설명 문장 각각에 Fact ID를 연결했다. 문장 변조·dangling/duplicate fact·중첩 hash 변조·snake_case ID/좌표 유출·forged scope를 자동 거부한다. 화면은 `LLM 사용 안 함 · deterministic fallback`, report hash, claim별 Fact ID를 보여준다.

이 변경은 LLM agent 성능, 실제 기관 보고서, 사람 승인, Firebase 저장·발행, 현장 groundedness를 증명하지 않는다. rollback은 static report section과 local package를 제거하는 source rollback이며 durable migration은 없다.

관련: [ADR-0061](../decisions/ADR-0061-r12-grounded-report-fallback.md), [EVD-20260813-027](../evidence/2026-08-product.md#evd-20260813-027--r12-fact-bundle과-근거-연결형-fallback-보고서)
