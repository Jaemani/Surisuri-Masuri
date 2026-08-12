# HR-20260813-06 — 제품 읽기 projection 구현 보고

- 기준일: 2026-08-13
- 상태: draft / 사람 검토 대기
- 환경: WSL2 local / Firestore Emulator / synthetic fixture
- 실제 회의·현장 사용자·production 배포: 없음

## 한 줄 결과

이용자·수리사·복지관이 각자 필요한 제품 정보만 서버에서 읽도록 역할·tenant·개인정보 경계를 구현하고 모바일·콘솔 HTTP adapter까지 연결했다.

## 검증 결과

| 대상 | 결과 |
| --- | --- |
| Domain Command typecheck/build/unit | 통과, unit 10개 |
| Firestore Emulator | command 4개 + projection 5개 통과 |
| 모바일 | typecheck, 17 files / 242 tests 통과 |
| 복지관 콘솔 | typecheck, 10 tests, production build 통과 |

## 사람이 확인할 화면 영향

- 기본 demo 화면은 기존처럼 실행된다.
- production 설정을 선택하면 모바일은 단일 Functions endpoint에서 역할별 projection을 읽는다.
- 콘솔은 하나의 Functions endpoint에 `projection` query를 붙여 9개 업무 화면을 읽는다.
- 실제 token·endpoint가 없으면 production adapter는 `NOT_CONFIGURED`로 중단한다.

## 주장 제한

이 보고는 local code와 합성 Emulator fixture의 권한·DTO 동작만 증명한다. 실제 복지관 운영, 실사용자 개인정보 처리, 현장 성과, Firebase 배포를 증명하지 않는다.

