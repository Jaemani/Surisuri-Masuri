# UPD-20260813-29 — 센터 검증·지원금 집행 authority loop

- 기준일: 2026-08-13
- 상태: implemented-local / synthetic adapter·Firestore Emulator server/projection 검증
- version_or_deployment: `authority-loop-local-synthetic-v1` (production deployment 없음)
- 대상 사용자: 복지관 콘솔 운영자와 수리 운영을 유지보수하는 개발자
- 로드맵 위치: 핵심 수리 운영 flow / R10 이후 연결 increment

## 제품 변화

수리사가 제출한 구조화 작업과 청구액을 복지관이 체크한 뒤, 센터 검증과 지원금 집행을 서로 다른 명령으로 처리하도록 콘솔 흐름을 바꿨다. 화면은 세 단계(`센터 검증 → projection 확인 → 지원금 집행`)를 보여주며 두 번째 명령이 실패하면 집행 완료로 표시하지 않는다.

서버는 센터 검증 전 execution을 거부한다. repair projection은 원 domain status와 검증된 subsidy context를 제공하고, ledger는 거래별 고유 ID와 type·numeric amount를 제공한다. execution 여부는 immutable ledger transaction으로 확인한다.

## 변경 전 문제

기존 local console demo는 `verified` 단계 전환과 지원금 집행을 하나의 진행처럼 보이게 할 수 있었다. 검증 명령의 성공, 최신 projection 재조회, 실제 execution transaction의 존재를 별도로 확인하지 않으면 부분 성공을 집행 완료로 오해할 여지가 있었다.

## 현재 구현 범위와 검증 경계

- Playwright는 브라우저가 console의 deterministic synthetic demo adapter를 사용하는 화면 흐름만 검증한다. 이 경로는 production Firebase/Auth/App Check composition이나 실제 원장을 호출하지 않는다.
- Firestore Emulator 검증은 domain-command의 server/store/projection 경계를 대상으로 한다. 브라우저 화면과 결합된 end-to-end Firebase composition 검증이 아니다.
- 검증 전 execution 거부, 승인 decision 문서·계정·예약 binding, 검증 후 typed projection, immutable execution과 reversal 반영을 local Emulator에서 확인한다. production 권한 구성과 실제 기관 lifecycle은 완료 주장에 포함하지 않는다.

## 검증

- console 25 unit tests, typecheck, build 통과
- domain command 32 unit tests 통과
- Firestore Emulator 19 scenarios 통과 (server/projection 경계)
- Playwright console 6 flows 통과 (synthetic demo adapter 경계)
- 신규 1440×1024 authority review snapshot 생성·시각 검토

## 배포·롤백

이 변경은 `authority-loop-local-synthetic-v1`로만 기록된 local increment이며 production/staging runtime에 배포하지 않았다. 따라서 데이터 migration이나 durable runtime rollback은 실행하지 않았다. 향후 배포 전 문제가 발견되면 authority review route를 이전 console adapter로 되돌리고, 실제 데이터에 적용하기 전 server command/projection과 권한 구성을 다시 검증한다.

## 알려진 제한

- 모든 화면 값과 브라우저 경로는 synthetic demo 데이터다.
- Playwright snapshot은 시각적 구조를 증명할 뿐 실제 기관의 승인·집행 이해도나 접근성을 증명하지 않는다.
- Emulator는 local Firebase emulator 상태의 server/projection 동작만 확인하며 production Firebase Auth, App Check, 실제 기관 계정·집행을 증명하지 않는다.
- field 수리 결과, 실제 subsidy decision 문서, 배포 후 운영·감사 결과는 아직 없다.

관련: [ADR-0059](../decisions/ADR-0059-center-verification-subsidy-authority-loop.md), [EVD-20260813-025](../evidence/2026-08-product.md#evd-20260813-025--센터-검증지원금-집행-authority-loop), [HR-20260813-22](../reports/human/HR-20260813-22-center-verification-subsidy-authority-loop.md)
