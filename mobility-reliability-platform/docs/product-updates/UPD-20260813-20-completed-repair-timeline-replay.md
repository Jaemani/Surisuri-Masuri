# UPD-20260813-20 — 완료 수리 archive 기반 기기 타임라인

- 기준일: 2026-08-13
- 상태: local code·emulator·web visual 검증
- 데이터: deterministic synthetic / emulator only

## 사용자 변화

- `내 기기`의 완료 수리 이력이 mutable 요청 내용이 아니라 검증 완료된 수리 archive와 구조화 작업항목에서 나온다.
- 진행 중 요청은 기존 수리 카드에 남고, 타임라인은 완료 이력과 기기 등록 기록을 분리해 보여준다.
- 작동하지 않던 `전체 보기` control을 제거하고 화면 제목에 header semantics를 추가했다.
- 데모 badge는 demo device에만 나타난다.

## 기술 변화

- 완료 repair header/items의 순서 독립 replay와 canonical checksum
- tenant/device/identity/source quality/category/action/quantity fail-closed 검증
- beneficiary projection이 bounded `repairs/items`를 읽어 purpose-limited 타임라인 DTO로 변환
- 원 증상·수리사 UID·지원금·GPS·Storage path를 출력하지 않는 emulator sentinel test

## 검증 경계

순수 projector와 Firestore Emulator, 모바일 Playwright snapshot을 검증했다. production Firebase, 실제 수리, 실제 사용자·복지관, async current projection, 부품 설치·제거 상태와 전체 Digital Twin은 아직 아니다.

관련 결정: [ADR-0050](../decisions/ADR-0050-completed-repair-timeline-replay.md)
