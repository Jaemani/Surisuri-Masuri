# UPD-20260813-15 — 전체 workspace 회귀검증 수정

- 기준일: 2026-08-13
- 상태: 수정·전체 검증 완료

QR integration 후 패키지 단독 명령과 전체 workspace typecheck의 설정 차이에서 TypeScript 오류 3개가 드러났다.

- Expo Camera 절대 위치 스타일을 명시적 style object로 변경
- 모바일 역할 union을 type guard로 좁혀 repairer job 배열의 optional 오판 제거
- 서버 projection의 document lookup 타입을 query snapshot에서 일반 document snapshot으로 확장

데이터 손상·운영 장애·배포 실패는 발생하지 않았으며 커밋 전 local 회귀검증에서 발견한 개발 오류다. 따라서 incident가 아니라 product update에 기록한다.

검증: workspace `pnpm check`, `pnpm test`, `pnpm build` 통과. Rules Emulator 28, mobile 250, console 10, domain workflows 6, legacy importer 6 및 나머지 workspace 검사가 포함된다.

