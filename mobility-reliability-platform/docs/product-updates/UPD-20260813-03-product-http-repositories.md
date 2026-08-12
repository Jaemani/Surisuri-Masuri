# UPD-20260813-03 — 모바일·복지관 콘솔 HTTP repository

## 제품 변화

모바일과 복지관 콘솔의 합성 데이터 adapter와 운영 adapter를 명확히 분리했다. 데모는 발표·화면 검증을 위해 결정론적 합성 상태를 유지하고, 운영 adapter는 Firebase Auth·App Check와 Domain Command/서버 read projection을 사용한다.

## 모바일

- ID token, App Check token, tenant/person/device scope와 endpoint를 composition root에서 주입
- 수리 요청을 `createRepairRequest` 서버 command로 전송
- command 성공 후 서버 projection의 동일 resource ID를 확인한 뒤 화면 상태 갱신
- 운영 환경의 임의 역할 전환 차단
- 인증·App Check·네트워크·응답·projection 실패 시 합성 데이터로 자동 후퇴하지 않음

## 복지관 콘솔

- 화면별 purpose-limited read projection GET adapter
- revision과 상태별 명시 필드를 요구하는 수리 transition command
- 사람·정책·계정·수리 건을 명시하는 지원금 transaction command
- client body의 actor ID를 권한 근거로 보내지 않음
- command마다 cryptographic idempotency key 요구
- 잘못된 DTO, 4xx, token 누락을 명시적 오류로 처리

## 주장 경계

adapter와 계약 테스트가 구현됐으며 실제 Firebase project의 token이나 배포 URL은 아직 연결하지 않았다. read projection endpoint는 후속 서버 구현 범위다. 기본 UI는 의도적으로 `DEMO · SYNTHETIC DATA` 상태다.
