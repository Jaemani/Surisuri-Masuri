# UPD-20260813-08 — 수리사 전용 작업 projection

- 기준일: 2026-08-13
- 상태: 구현·local emulator 검증 완료
- 환경: WSL2, Firebase Firestore Emulator

## 변경

수리사 모바일이 실제 상태 변경을 수행할 수 있도록 배정 작업 projection에 다음 권위 필드를 추가했다.

- domain status와 revision
- 기기 공개코드·모델
- 검증된 증상 분류
- 일정 ISO 시각과 한국어 표시값
- 청구액·제출시각의 제한된 읽기 상태
- 서버가 계산한 `allowedActions`

Firestore 조회는 tenant의 전체 work order를 읽은 뒤 필터링하지 않고 `repairer_firebase_uid == actor.uid` 조건으로 제한한다. 결과의 active 상태와 문서 수를 다시 검증한다.

## 개인정보·업무 경계

- 고객 법적 이름, 전화번호, 주소, 생년월일을 조회하지 않는다.
- 원문 증상, Firebase UID, person ID, 지원금 계정·잔액·예약액, GPS와 Storage 경로를 DTO에 포함하지 않는다.
- 고객 표시는 기관용 공개코드 기반 pseudonymous label이다.
- 수리사가 가능한 다음 action은 클라이언트가 추측하지 않고 서버 상태에서 파생한다.

## 검증

- exact DTO key 검증
- 다른 수리사 작업 제외
- 민감정보·원문·지원금 sentinel 부재 확인
- domain-command unit 12, Firestore Emulator command/projection 10 통과

실제 Firebase composite index 및 운영 데이터 규모의 query latency는 아직 검증하지 않았다.

