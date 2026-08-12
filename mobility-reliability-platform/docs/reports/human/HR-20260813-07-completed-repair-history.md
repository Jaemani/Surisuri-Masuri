# HR-20260813-07 — 완료 수리 이력 구현 보고

- 기준일: 2026-08-13
- 상태: draft / 사람 검토 대기
- 환경: WSL2 local / Firestore Emulator / synthetic fixture

## 한 줄 결과

수리소 배정의 실재·활성 상태를 검증하고, 완료된 수리를 작업 진행 문서와 분리된 감사 가능한 이력으로 원자적으로 남기도록 구현했다.

## 검증

- Domain Command typecheck·unit·build 통과
- Firestore Emulator: command 5개 + projection 5개, 총 10 scenarios 통과
- 존재하지 않는 수리소 배정 거부
- 요청→검토→배정→일정→작업→수리사 제출→센터 검증→완료 전이
- 완료 시 `/repairs` 한 건 생성, work order·device·station·repairer·금액 연결
- 원본 증상 자유문과 memo 미복사

## 주장 제한

합성 Emulator fixture의 서버 동작만 검증했다. 실제 기관·수리사·보조금 집행·production 배포 증거가 아니다.

