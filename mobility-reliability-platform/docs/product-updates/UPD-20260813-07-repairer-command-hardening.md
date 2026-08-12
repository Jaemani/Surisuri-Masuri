# UPD-20260813-07 — 수리사 command 보안 경계와 일정 저장

- 기준일: 2026-08-13
- 상태: 구현·local emulator 검증 완료
- 환경: WSL2, Firebase Firestore Emulator

## 변경

- transition별 exact field allowlist를 추가했다.
- `scheduledAt` command·domain model·Firestore codec을 연결했다.
- 과거/비정상적으로 먼 일정 입력을 제한했다.
- `submittedAt`을 서버 처리시각으로 생성하도록 변경했다.
- 수리 관련 복합 역할도 수리사 전이에서는 배정 UID 검사를 우회하지 못하게 했다.
- 기존 전체 수리 lifecycle 테스트를 새 계약에 맞췄다.

## 막은 오류

수리사가 일정 확정 요청에 `repairerFirebaseUid`, `repairStationId`, 지원금 계정·판정 값을 끼워 넣어 권위 필드를 바꾸는 과다 입력 경로를 차단했다. `submittedAt`을 과거 또는 미래로 조작하는 요청도 허용하지 않는다.

## 검증 결과

- domain-command unit: 12 passed, emulator 미실행 10 skipped
- domain-command TypeScript: 0 errors
- Firestore Emulator: command 5 + projection 5 = 10 passed

이 결과는 실제 Firebase 배포나 실사용자 검증을 뜻하지 않는다.

