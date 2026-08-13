# UPD-20260813-35 — R12 report run lifecycle

- 기준일: 2026-08-13
- 상태: implemented-local / synthetic-only
- version_or_deployment: `report-run-lifecycle.v1` local / production 배포 없음
- 대상 사용자: 복지관 보고서 검토자·파이프라인 개발자
- 로드맵 위치: 10월 R12 report agent 선행 increment

직접 `pending → fallback`으로 건너뛰거나 terminal 상태를 다시 바꾸는 전이를 막았다. failure에는 allowlist failure class가 필수이고 primary `completed`와 deterministic `fallback`의 `fallbackUsed` 의미를 교차 검사한다. 현재 합성 보고서는 `fallback / 사람 검토 대기 / 미발행`이다.

콘솔은 네 단계 상태를 별도 카드로 보여주고 “발행 완료” 오표현을 제거했다. 화면 회귀 6개와 새 snapshot을 확인했다.

관련: [ADR-0065](../decisions/ADR-0065-report-generation-review-publication-states.md), [EVD-20260813-031](../evidence/2026-08-product.md#evd-20260813-031--r12-report-run-lifecycle과-검토발행-표시)
