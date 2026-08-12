# UPD-20260813-12 — 복지관 command adapter 계약 정렬

- 기준일: 2026-08-13
- 상태: 구현·local 검증 완료

복지관 production adapter의 범용 transition option bag을 역할에 맞는 discriminated union으로 좁혔다. 콘솔은 배정, 검토, 수정 요청, 센터 검증, 완료·재개·거절·취소 명령만 생성한다. 수리사 전용 일정·작업 시작·작업 제출 필드와 client-controlled `submittedAt`은 콘솔 계약에서 제거했다.

Firebase handoff에는 상태별 exact field, 서버 소유 제출시각, 구조화 work item과 완료 이력 경계를 추가했다. 8월 15일 고정 리포트에는 계획된 ML 의제와 별개인 실제 code snapshot으로 기록했으며 회의·배포·현장 성과로 표현하지 않았다.

검증: console TypeScript 0 errors, 10 tests, documentation links passed.

