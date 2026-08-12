# UPD-20260813-11 — 구조화 수리 항목과 완료 이력

- 기준일: 2026-08-13
- 상태: 서버 구현·local emulator 검증 완료
- 환경: WSL2, Firebase Firestore Emulator

## 변경

- 수리사 제출 command에 allowlisted work item 배열을 추가했다.
- 항목 개수·code·수량·금액·추가 필드를 엄격히 검증한다.
- 구조화 항목이 없거나 항목 합계가 청구액과 다르면 제출을 거부한다.
- work order에는 snake_case map으로 저장한다.
- 최종 완료 transaction에서 immutable repair와 각 repair item을 함께 생성한다.
- 완료 항목에는 자유문자나 고객정보를 복사하지 않는다.

## 검증

- domain-command unit 12 passed
- domain-command TypeScript 0 errors
- Firestore Emulator command 5 + projection 5 passed
- 완료 이력 하위 item 1건과 raw detail 부재 확인

모바일 입력과 복지관 제출 상세 projection/UI는 다음 작업 단위다.

