# ADR-0066 — unsupported candidate claim 기본 제외

- 상태: accepted-local
- 결정일: 2026-08-13
- 범위: R12 candidate claim validation

## 결정

후보 문장은 최종 report hash 봉인 전에 개별 검증한다. exact Fact, claim ID/type과 결정론적 문장에 맞는 후보만 `include/grounded`하고, 근거 누락·type 불일치·문장 변조·민감 key가 있는 후보는 기본 `omit`한다. disposition 결과에는 후보 원문이나 임의 ID를 복사하지 않고 index와 안정적인 validation code만 둔다.

`[확인 필요]` 강등 문구는 사람 검토용 별도 초안 계약이 생길 때까지 사용하지 않는다. 현재 final report에는 grounded claim만 포함한다.

근거: [EVD-20260813-032](../evidence/2026-08-product.md#evd-20260813-032--unsupported-candidate-claim-disposition)
