# ADR-0062 — R12 source assessment 무결성 재검증

- 상태: accepted
- 결정일: 2026-08-13
- 범위: local synthetic report evidence boundary

## 결정

R12 report builder는 입력의 `assessmentSha256` 형식만 신뢰하지 않는다. R11 Python evaluator와 동일하게 self-hash 필드를 제외한 전체 assessment를 key 정렬 canonical JSON으로 직렬화해 SHA-256을 재계산한다. 또한 보고서 Fact로 축소하기 전에 root, lineage, 평가 정책, fact boundary, limitations와 부품별 판단 유보 구조를 allowlist로 다시 검증한다.

canonical key 정렬은 실행 환경 locale에 의존하는 `localeCompare` 대신 명시적인 code-unit 순서를 사용한다. hash 일치 여부는 artifact 운반 중 변조를 발견하는 무결성 경계이며 작성자 신원이나 신뢰성을 인증하지 않는다.

## 결과와 경계

- assessment의 중첩 count를 hash 갱신 없이 바꾸면 `ASSESSMENT_HASH_MISMATCH`로 거부한다.
- test tuning, field scope, 개별 조치 허용처럼 R11 목적을 약화하면 hash를 다시 만들었더라도 정책 검증에서 거부한다.
- 현 local package는 R11의 세 부품 모두 `not_estimable`인 synthetic assessment만 받는다.
- JSON Schema와 Python dataset/result binding을 대체하지 않으며 Firebase Fact Store, 서명, KMS, production provenance를 구현한 것이 아니다.

근거: [EVD-20260813-028](../evidence/2026-08-product.md#evd-20260813-028--r12-source-assessment-self-hash-gate)
