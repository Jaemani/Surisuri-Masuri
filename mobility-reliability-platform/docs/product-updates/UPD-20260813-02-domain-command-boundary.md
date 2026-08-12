# UPD-20260813-02 — 수리·지원금 Domain Command 경계

## 사용자에게 보이는 변화

이번 업데이트는 화면 기능을 늘린 것이 아니라 모바일과 복지관 콘솔이 안전하게 공용 데이터를 변경할 수 있는 서버 경계를 완성한 작업이다. 사용자의 수리 접수, 복지관의 배정·검증, 수리사의 작업 제출, 지원금 예약·집행을 동일한 상태기계와 감사 이벤트로 처리할 수 있게 됐다.

## 구현 범위

- Firebase ID token과 App Check를 모두 요구하는 HTTP command 3종
- `repairWorkOrders` 생성 및 revision 기반 상태 전환
- 활성 기기 배정, 보호자 관계, 수리사 배정 검증
- 사람·정책·계정 단위의 `subsidyAccounts/transactions` 원장
- 공개 지원 대상·요청액·청구액·상태에 따른 지원금 검증
- 동일 요청 재시도와 다른 본문 충돌을 구분하는 멱등성 receipt
- Firestore snake_case codec과 append-only domain event/status history

## 검증 범위

- 순수 kernel/HTTP 단위 테스트
- Firestore Emulator의 실제 transaction·경로·field 검증
- 동일 command replay, body conflict, 동시 revision conflict
- 미배정 수리사 거부와 tenant/account/person 경계 확인

## 아직 주장하지 않는 것

- 실제 Firebase 프로젝트 배포
- 현장 계정 및 실제 개인정보 연동
- 복지관 또는 수리사의 현장 사용 결과
- 기관 간 수리소 공유 grant

데이터 분류: **LOCAL EMULATOR / SYNTHETIC FIXTURE**
