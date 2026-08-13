# ADR-0063 — R12 완전한 Fact·claim profile

- 상태: accepted
- 결정일: 2026-08-13
- 범위: local synthetic report evidence boundary

## 결정

`report-fact-bundle.v1`은 임의 Fact 모음이 아니라 다음 다섯 Fact의 완전한 profile로 검증한다.

1. battery readiness
2. brake readiness
3. controller readiness
4. 고정 점검 일정과 사람 검토 fallback
5. synthetic-only·현장 성능 아님·개별 조치 금지 scope boundary

각 Fact ID, type과 component 조합을 정확히 고정하고, 최종 report에는 각 Fact와 정확히 1:1로 연결된 type-safe claim이 모두 있어야 한다. bundle/report ID도 source assessment hash에서 파생된 값과 일치해야 한다. 이 결정으로 임의 ID를 전화번호·외부 식별자 전달 채널로 쓰거나 안전 경계 claim만 제거한 보고서를 유효하게 봉인할 수 없다.

## 경계

- 이 profile은 현재 R11 synthetic abstention 보고서에만 적용한다. 미래 field report에는 새 schema/version과 별도 profile이 필요하다.
- unsupported candidate claim 처리와 report run lifecycle은 다음 증분이며, 현재 최종 artifact validator는 하나의 불일치도 fail-closed한다.
- ID allowlist는 persistence envelope의 tenant·report binding을 대신하지 않는다.

근거: [EVD-20260813-029](../evidence/2026-08-product.md#evd-20260813-029--r12-complete-factclaim-profile)
