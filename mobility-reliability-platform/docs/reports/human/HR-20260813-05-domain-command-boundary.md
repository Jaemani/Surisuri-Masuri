# HR-20260813-05 — 수리·지원금 Domain Command 구현 보고

- 기준일: 2026-08-13
- 상태: draft / 사람 검토 대기
- 환경: WSL2 local / Firestore Emulator / synthetic fixture
- source commit: `f83612f`

## 한 줄 결과

사용자 수리 접수부터 복지관 배정·수리사 처리·지원금 원장까지의 변경을 Firebase 클라이언트 직접 쓰기가 아니라, 인증·권한·멱등성·감사를 포함한 서버 command transaction으로 처리할 기반을 구현했다.

## 구현된 범위

- `createRepairRequest`, `transitionRepairRequest`, `appendSubsidyTransaction` HTTP 함수
- ID token과 App Check의 이중 token 요구
- 활성 tenant, membership UID·role·유효기간 검사
- 본인 기기 배정과 보호자 관계 확인 후 수리 요청 생성
- 복지관이 배정한 Firebase UID와 일치하는 수리사만 작업 상태 변경
- 공개 지원 대상 수리의 사람·정책·계정 단위 원장
- 지원금 요청액·수리 청구액·수리 상태를 넘는 예약/집행 거부
- 동일 idempotency key의 동일 body replay와 다른 body conflict 분리
- revision 기반 동시 변경 충돌
- snake_case Firestore codec, work-order status history와 domain event

## 검증 결과

| 검증 | 결과 |
| --- | --- |
| TypeScript build | 통과 |
| pure kernel + HTTP | 7 tests 통과 |
| Firestore Emulator adapter | 4 scenarios 통과 |
| 문서 링크 | 통과 |

Emulator 시나리오는 canonical path, storage field, 동일 command replay, body conflict, concurrent revision conflict, 미배정 수리사 거부, 사람별 지원금 account와 tenant 경계를 확인했다.

## 중요한 한계

- 실제 Firebase 프로젝트에 배포하지 않았다.
- Android/iPhone의 실제 Firebase Auth·App Check token으로 호출하지 않았다.
- 모바일/콘솔 projection read endpoint는 아직 구현되지 않았다.
- cross-organization repair station grant와 실제 기관 정책 fixture는 후속 범위다.
- 합성 fixture는 현장 처리 성과나 실제 보조금 집행 증거가 아니다.

## 다음 연결

모바일과 복지관 콘솔 repository에 token/App Check/endpoint provider를 주입하고 command API를 연결한다. 실데이터가 없는 기본 preview는 계속 deterministic demo로 유지하며, Firebase 설정 누락 시 production adapter는 demo로 조용히 후퇴하지 않고 fail closed한다.
