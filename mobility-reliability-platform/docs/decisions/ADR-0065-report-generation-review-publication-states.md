# ADR-0065 — 보고서 생성·검토·발행 상태 분리

- 상태: accepted-local
- 결정일: 2026-08-13
- 범위: R12 local lifecycle·console presentation

## 결정

보고서 생성 상태는 `pending → validated | failed`, `validated → completed | fallback | failed`만 허용한다. `completed`와 `fallback`은 생성 terminal일 뿐 발행 완료가 아니다. 생성 terminal은 `reviewStatus=pending`, `publicationStatus=unpublished`로 끝나며 사람 승인과 발행은 별도 후속 명령·receipt가 생기기 전에는 전환하지 않는다.

현재 deterministic template은 LLM primary 결과가 아니므로 `fallback`이다. console은 한 badge로 축약하지 않고 생성 방식, 기계 검증, 사람 검토, 발행 상태 네 단계를 함께 표시한다. 기존 projection의 `completed → 발행 완료` 번역은 `생성 완료 · 검토 대기`로 수정한다.

## 경계

- 이번 증분은 승인·거절·발행 명령을 구현하지 않는다. 따라서 모든 R12 결과는 미발행이다.
- unsupported candidate claim disposition은 다음 증분이다. 현재 sealed report는 모든 claim을 grounded로 검증한다.
- 상태기는 local pure function이고 Firebase persistence writer·production worker와 연결되지 않았다.

근거: [EVD-20260813-031](../evidence/2026-08-product.md#evd-20260813-031--r12-report-run-lifecycle과-검토발행-표시)
