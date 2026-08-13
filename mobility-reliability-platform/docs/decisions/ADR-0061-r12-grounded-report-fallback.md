# ADR-0061 — R12 근거 연결형 결정론적 보고서

- 상태: accepted
- 결정일: 2026-08-13
- 범위: local synthetic report evidence slice

## 결정

LLM을 연결하기 전에 R11 synthetic assessment를 typed Fact bundle로 정규화하고, 허용된 deterministic template만으로 복지관 운영자용 보고서를 만든다. 모든 claim은 정확히 하나의 존재하는 Fact ID와 연결되며 text가 해당 fact template과 다르면 거부한다.

Fact bundle과 report는 각각 recursive canonical SHA-256으로 중첩 값까지 봉인한다. report receipt는 source assessment hash와 exact Fact bundle hash를 함께 가진다. LLM은 사용하지 않았고 `fallbackUsed=true`다.

## 보안·제품 경계

- component readiness, fallback policy, synthetic scope 세 fact type만 허용하고 값의 key·enum·정수 범위를 fail-closed 검증한다.
- camelCase와 snake_case 사람·기기·기관 ID, Firebase UID, 좌표·경로·Storage path·수리 자유문 key를 중첩 위치에서도 거부한다.
- 실제 domain event payload 전체, 개별 위험·안전·지원 자격·운영 mutation은 Fact나 claim에 넣지 않는다.
- console은 builder 출력과 exact equality가 검증된 static synthetic snapshot만 표시한다. Firebase report pipeline이나 기관 발행을 의미하지 않는다.

근거: [EVD-20260813-027](../evidence/2026-08-product.md#evd-20260813-027--r12-fact-bundle과-근거-연결형-fallback-보고서)
