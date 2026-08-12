# UPD-20260813-14 — 네이티브 QR 기기 확인

- 기준일: 2026-08-13
- 상태: 구현·cross-platform export 검증 완료 / 실기기 camera smoke 대기
- 환경: Expo Camera, Android/iOS export, WSL2

## 변경

- Expo Camera QR scanner를 현장 기기 확인 gate에 연결했다.
- 카메라 권한 요청 문구를 앱 config에 추가하고 Android audio recording은 비활성화했다.
- 수동 공개코드 입력은 권한 거부·카메라 오류 fallback으로 항상 유지한다.
- QR payload는 공개코드 원문 또는 `surisuri://device/<code>`만 허용한다.
- HTTP URL, script scheme, 빈 코드, 64자 초과 payload는 거부하며 외부 URL을 실행하지 않는다.
- scan 결과도 기존 공개코드 일치 gate를 통과해야 action이 활성화된다.

## 검증

- mobile 19 files / 250 tests
- QR parser 정상·악성 형식 unit test
- Android Expo export 성공
- iOS Expo export 성공
- Playwright 4 web flows 통과

Expo Web의 화면 렌더링은 검토했지만 WSL2 브라우저 camera와 Android/iPhone 실제 카메라 스캔 성공을 증명하지 않는다. 다음 실기기 checkpoint에서 권한 승인·거부·재시도·저조도 QR을 확인해야 한다.

