# UPD-20260813-06 — 수리 접수·복지관 action UI

- 기준일: 2026-08-13
- 상태: 구현·local 검증 완료 / 사람 검토 대기
- 환경: WSL2, deterministic synthetic demo, Playwright web

## 모바일

하드코딩된 한 문장 접수 버튼을 실제 입력 흐름으로 교체했다.

- 증상 분류 7종, 10~500자 상세 설명
- 지원금 신청 여부를 명시적으로 선택
- 지원 신청 시 서버 계약과 같은 1원~1억원 예상 수리비 필수
- 전화번호·주소·건강정보를 쓰지 않도록 안내
- 작성→최종 확인→전송, 전송 중 중복 입력 방지
- stable idempotency key를 최종 확인 때 생성해 retry에 재사용
- command 성공 후 projection이 늦으면 새 POST 대신 읽기 상태만 재확인
- 사진 첨부 계약이 없으므로 기존 사진 지원 문구 제거

## 복지관 콘솔

수리 카드의 가짜 “다음 단계 시작” toast를 stage-aware action panel로 교체했다.

- 새 요청: 합성 수리소·수리사 필수 선택 후 demo assignment
- 배정 후: 복지관이 수리사 상태를 대신 넘기지 않고 읽기 전용 대기
- 수리 제출: 결과·금액·지원금 적격 체크 뒤 합성 demo 검증
- revision conflict는 오류와 새로고침 경로를 분리
- 화면·ID·결과에 `SYNTHETIC DEMO` 경계를 반복 표시

production repository의 명시적 domain command가 아닌 demo shortcut은 합성 시뮬레이션으로만 동작한다. 실제 Firebase 설정 오류 시 demo로 자동 후퇴하지 않는다.

## 시각 검토

390×844 모바일과 1440×1024 콘솔을 Playwright로 캡처해 확인했다. 모바일 확인 단계 전환 때 기존 스크롤 위치로 제목이 잘리던 문제를 발견해 단계 변화 시 상단으로 복귀하도록 수정했다.

스크린샷은 레이아웃·문구·대상 크기만 확인한다. Android TalkBack, iPhone VoiceOver, 키보드 회피는 실기기에서 별도 확인해야 한다.

