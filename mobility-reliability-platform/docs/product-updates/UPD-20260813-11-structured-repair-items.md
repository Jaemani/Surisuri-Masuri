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

## 제품 연결

- 수리사 모바일에 작업 부위·처리 방법·금액 입력을 연결했다.
- mobile adapter는 표시 label을 제거하고 code·수량·금액만 command로 전송한다.
- 수리사·복지관 projection은 사람이 읽을 label을 서버에서 파생한다.
- 복지관 제출 상세는 구조화 항목을 표시하고 항목이 없으면 검증 버튼을 비활성화한다.
- React Native web flow와 console UI를 Playwright로 함께 검증했다.
