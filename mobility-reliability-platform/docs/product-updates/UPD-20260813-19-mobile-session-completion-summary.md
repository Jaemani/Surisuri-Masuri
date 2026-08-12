# UPD-20260813-19 — 모바일 주행 종료 저장 확인

- 기준일: 2026-08-13
- 상태: local 구현·typecheck·web visual 검증 완료 / 사람 검토 대기
- 로드맵 위치: 6월 Role-aware Mobile Foundation 보강, 8월 사용자 경험 신뢰성 보강
- 환경: WSL2, Expo Web, synthetic preview

## 바뀐 제품 경험

사용자가 이동 기록을 마치면 홈 화면이 즉시 초기화되지 않고, 최근 기록을 저장했다는 확인 카드가 남는다. 기록 시간과 휴대폰 보관 상태를 사용자 언어로 표시하며, 원시 샘플·세션 ID·내부 큐 수는 노출하지 않는다.

## 검증

- `pnpm --filter @mobility-reliability/mobile typecheck`
- `pnpm exec playwright test tests/e2e/mobile-web.spec.ts --project=mobile-chromium`

## 범위와 제한

이 변경은 세션 종료 결과를 UI에 유지하는 local 제품 증분이다. 실제 GPS 거리 계산, Firebase upload ACK, Android/iPhone 백그라운드 종료와 접근성 검증은 포함하지 않는다.

관련: [ADR-0049](../decisions/ADR-0049-mobile-session-completion-summary.md), [EVD-20260813-015](../evidence/2026-08-product.md#evd-20260813-015--모바일-주행-종료-저장-확인)
