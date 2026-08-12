# UPD-20260813-09 — 모바일 수리사 command adapter

- 기준일: 2026-08-13
- 상태: 구현·unit 검증 완료
- 환경: React Native/Expo, deterministic demo + Firebase production adapter

## 변경

- 모바일 repository에 일정 확정, 작업 시작, 수정 재개, 비용 제출의 typed command를 추가했다.
- production adapter는 범용 option bag 대신 단계별 최소 필드만 `transitionRepairRequest`로 전송한다.
- 모든 mutation은 caller가 보관하는 stable idempotency key를 사용한다.
- 성공 응답만으로 화면을 바꾸지 않고 mobile projection을 다시 읽어 더 높은 revision의 동일 작업을 확인한다.
- revision·idempotency·assignment 오류를 typed error로 보존한다.
- repairer DTO decoder는 spread를 제거하고 허용 필드만 새 객체로 복사한다. 서버가 실수로 지원금 참조를 추가해도 client state에 들어오지 않는다.
- deterministic demo도 같은 비동기 command 경계와 상태 순서를 따른다.

## 검증

- 모바일 unit 18 files, 248 tests passed
- 모바일 TypeScript 0 errors
- exact schedule POST body, idempotency header, authoritative GET refresh 확인
- 서버 응답의 추가 지원금 필드 제거와 revision conflict 단일 호출 확인

실제 Auth/App Check token provider는 아직 앱 런타임에 연결하지 않았다.

