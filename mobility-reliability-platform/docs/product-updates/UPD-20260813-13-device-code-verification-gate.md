# UPD-20260813-13 — 현장 기기 공개코드 확인 gate

- 기준일: 2026-08-13
- 상태: 구현·web visual 검증 완료
- 환경: React Native/Expo Web, Playwright 390×844

수리사 작업 상세에 현장 기기 공개코드 입력을 추가했다. 서버 projection의 공개코드와 대소문자·공백을 정규화해 비교하며 일치하기 전에는 일정 확정과 작업 시작 버튼을 비활성화한다. 이는 카메라가 없거나 권한을 거부했을 때도 사용할 수 있는 수동 fallback이다.

QR camera를 연결할 때도 스캔값을 같은 확인 함수에 넣는다. 현재는 카메라·lookup endpoint가 구현되지 않았으므로 QR 스캔이 되는 것처럼 표시하지 않는다.

검증: mobile 248 tests, TypeScript 0 errors, Playwright 4 flows. 새 스크린샷에서 390px 화면의 코드 입력·상태·CTA 잘림이 없음을 확인했다. 이 UI gate는 서버 권한을 대체하지 않으며 실제 기기 소유권·QR 위조 방지 증명도 아니다.

