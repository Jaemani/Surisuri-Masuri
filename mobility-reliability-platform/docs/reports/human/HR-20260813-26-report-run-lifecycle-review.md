# HR-20260813-26 — R12 생성·검토·발행 상태 화면 검토

- 대상 기간: 2026-08-13 local increment
- 작성자: Codex 구현 초안
- 검토자: 프로젝트 책임자 확인 대기
- 로드맵: 10월 R12 report agent
- 상태: generated / 사람 검토 대기

## 실제 변경

보고서 카드 상단에 `생성 방식 Fallback → 기계 검증 통과 → 사람 검토 대기 → 발행 상태 잠김`을 한 줄로 표시했다. 초록색 “검증 통과”만 보이던 상태에서, 기계 검증과 사람 승인·발행을 오해하지 않도록 분리했다.

## Product Design 화면 점검

1440px Chromium snapshot에서 상태 순서, claim 카드 위계, 미발행 badge와 합성 전용 경계를 확인했다. 네 상태가 report hash와 claim 목록보다 먼저 읽히며 기존 카드 폭 안에서 잘림이 없다. screenshot만으로 keyboard focus, screen reader reading order, 색 대비 수치와 zoom reflow는 확인하지 못했다.

## 사람 검토 요청

- `Fallback`을 비기술 운영자에게 `안전한 기본 보고서`로 풀어쓸지
- `기계 검증 통과`가 내용의 현장 진실성 검증으로 오해되지 않는지
- 승인·발행 기능 구현 전 `잠김` 표현이 충분히 명확한지

근거는 [ADR-0065](../../decisions/ADR-0065-report-generation-review-publication-states.md), [UPD-20260813-35](../../product-updates/UPD-20260813-35-report-run-lifecycle.md), [EVD-20260813-031](../../evidence/2026-08-product.md#evd-20260813-031--r12-report-run-lifecycle과-검토발행-표시)이다.
