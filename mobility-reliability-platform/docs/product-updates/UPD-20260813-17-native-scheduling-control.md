# UPD-20260813-17 — 수리사 네이티브 방문 일정 선택

- 기준일: 2026-08-13
- 상태: local 구현·export·web visual 검증 완료 / 실기기 검증 대기
- 환경: WSL2, Expo SDK 57, synthetic demo

## 바뀐 제품 경험

- 수리사 일정 확정 화면의 고정 예시 시각을 제거했다.
- Android와 iPhone에서는 운영체제의 날짜·시간 선택기를 사용한다.
- 선택값은 `Date.toISOString()`으로 변환해 `scheduledAt` command field로 보낸다.
- 현재 시각 이전 또는 180일을 넘는 시각은 제출할 수 없다. 네이티브 시간 선택 후에도 같은 범위를 다시 검사한다.
- 일정값이 실제로 바뀔 때만 해당 command의 idempotency key를 폐기한다. 실패 후 같은 값으로 다시 시도할 때는 같은 key를 유지한다.
- WSL에서 검토하는 웹 화면에는 같은 상태·검증 계약을 사용하는 조정 버튼을 두었다. 이는 native picker의 대체 구현이 아니라 시각·상태 회귀용 fallback이다.

## 검증

- 모바일 typecheck 통과
- 모바일 20 files / 253 tests 통과
- Android와 iOS Expo export 통과
- Playwright 모바일 2 flows 통과
- 일정 선택 웹 snapshot을 사람이 열어 일정 카드, 조정 버튼, 확정 CTA의 레이아웃을 확인했다.

## 검증 경계

이 결과는 실제 Android/iPhone picker 조작, 취소, 시스템 시간대 변경, TalkBack/VoiceOver, 키보드와 back gesture를 증명하지 않는다. Firebase production 예약, 실제 수리사 일정, 복지관 연동 성과도 아니다.

관련 계약은 [ADR-0044](../decisions/ADR-0044-repairer-transition-command-boundary.md), 상세 증거는 [EVD-20260813-011](../evidence/2026-08-product.md#evd-20260813-011--네이티브-방문-일정-선택)에서 확인한다.
