# UPD-20260813-18 — 모바일 복수 수리항목 편집·검토

- 기준일: 2026-08-13
- 상태: local 구현·test·web visual 검증 완료
- 환경: WSL2, synthetic demo

## 바뀐 제품 경험

- 수리사 제출 화면을 단일 부위·금액 입력에서 1~20개 구조화 수리항목 편집으로 확장했다.
- 각 항목은 부위, 처리 방식, 수량, 항목 금액만 받는다. 자유문자·사진 경로·고객정보·지원금정보를 command에 싣지 않는다.
- 총 청구액은 별도 입력하지 않고 항목 금액 합계에서 파생한다.
- 최종 command 전에 항목별 내용과 총액을 한 화면에서 검토하며, 수정 화면으로 돌아가도 입력이 유지된다.
- 전송 adapter는 항목 수, 수량·금액 범위와 합계 일치를 다시 검사하고 label을 제외한 exact server field만 보낸다.
- 동일 revision·동일 payload의 재시도는 같은 idempotency key를 사용하고 payload가 달라지면 새 key를 만든다.

## 검증

- 모바일 typecheck 통과
- 모바일 21 files / 258 tests 통과
- Playwright 모바일 2 flows 통과
- 복수 항목 검토 snapshot을 사람이 열어 항목 구분, 합계, 수정·제출 CTA를 확인했다.

## 검증 경계

현장 부품 catalog, 사진 첨부, 실제 수리사 사용성, 실제 복지관 검증과 Firebase production 연결은 증명하지 않는다. 모바일 client 검증은 서버 exact-key·합계 검증을 대체하지 않는다.

관련 계약은 [ADR-0045](../decisions/ADR-0045-structured-repair-work-items.md), 상세 증거는 [EVD-20260813-012](../evidence/2026-08-product.md#evd-20260813-012--모바일-복수-구조화-수리항목)에서 확인한다.
