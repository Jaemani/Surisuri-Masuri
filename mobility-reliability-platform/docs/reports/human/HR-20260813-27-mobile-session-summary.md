# HR-20260813-27 — 모바일 주행 종료 결과 점검

- 발행일: 2026-08-13
- 상태: generated / 사람 검토 대기
- 대상 독자: 프로젝트 운영자·기술 검토자
- 로드맵 게이트: 6월 Role-aware Mobile Foundation 보강

## 사전 계획

주행 종료 뒤 사용자가 기록 보존 여부를 확인할 수 있도록 종료 결과 화면을 추가한다. 개발자용 샘플·큐 정보는 사용자 핵심 화면에서 분리한다.

## 실제 상태

`lastCompletedSession`을 모바일 recorder 상태에 추가하고, native와 Expo Web 경로에서 종료된 세션 요약을 사용자 홈에 유지하도록 구현했다. Playwright 모바일 flow는 주행 시작·종료 후 저장 확인 문구를 검사한다. 실제 Firebase 전송과 실기기 lifecycle은 아직 연결·검증하지 않았다.

## 근거

- [EVD-20260813-015](../../evidence/2026-08-product.md#evd-20260813-015--모바일-주행-종료-저장-확인)
- [UPD-20260813-19](../../product-updates/UPD-20260813-19-mobile-session-completion-summary.md)
- [ADR-0049](../../decisions/ADR-0049-mobile-session-completion-summary.md)

## 계획 대비 차이와 위험

거리·소요시간은 현재 session summary의 시간 정보만 사용하며 GPS 누적거리로 계산하지 않는다. 실제 서버 동기화가 구현되기 전까지 `기관에 전달됨`을 완료 상태로 표시하지 않는다. 실기기에서 알림·접근성·앱 재시작 뒤 요약 보존 정책은 별도 검토가 필요하다.

## 다음 판단

실기기에서 저장 확인 카드의 글자 확대·TalkBack/VoiceOver 순서와 오프라인 전송 대기 문구를 확인한 뒤, telemetry projection이 제공하는 실제 거리·전송 상태를 연결한다.
