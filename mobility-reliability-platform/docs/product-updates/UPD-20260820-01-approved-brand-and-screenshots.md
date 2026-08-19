# UPD-20260820-01 — 승인 제품명·로고 적용

- 기준일: 2026-08-20
- 상태: implemented-local / synthetic-only / 사람 검토 대기
- 대상: 복지관 콘솔, 모바일 app display name, Playwright 발표 화면
- 배포: production 배포 없음

## 변경

- 임의 placeholder `모두의 이동`을 제거했다.
- 콘솔 sidebar·브라우저 title·description을 `수리수리마수리`와 `복지관 운영 콘솔`로 정정했다.
- 사용자 제공 원본 로고의 byte-identical 사본을 사용하고 배경 제거 없이 CSS로 흰 여백만 crop했다.
- Expo display name을 `수리수리마수리`로 정정했다.
- Playwright가 승인 이름과 title을 확인하고 임의 이름 부재를 회귀검사한다.
- 변경된 콘솔 golden screenshot 7장을 다시 생성했다.

## 제한

- 화면은 local deterministic synthetic demo다.
- 상표 출원·법률 검토, production 배포, native 앱 아이콘·스토어 자산 완료를 주장하지 않는다.
- 실제 Android/iPhone screenshot은 별도 native smoke 이후 생성해야 한다.

관련: [ADR-0067](../decisions/ADR-0067-approved-product-brand-source.md), [EVD-20260820-002](../evidence/EVD-20260820-002-corrected-brand-screenshot-set.md)

