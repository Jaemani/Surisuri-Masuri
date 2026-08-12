# UPD-20260813-10 — 수리사 단계형 모바일 작업공간

- 기준일: 2026-08-13
- 상태: 구현·web visual·cross-platform export 검증 완료
- 환경: React Native/Expo, WSL2, Playwright 390×844

## 제품 변경

읽기 전용 작업 카드와 작동하지 않는 QR·상세 버튼을 실제 단계형 작업공간으로 교체했다.

1. 배정된 작업 목록에서 pseudonymous 고객코드, 기기 공개코드·모델, 증상 분류와 현재 단계를 확인한다.
2. 작업을 열면 현장 기기의 공개코드를 먼저 대조한다.
3. 배정 상태에서는 방문 일정을 확정한다.
4. 일정 상태에서는 현장 확인 후 작업을 시작한다.
5. 작업 중에는 청구 금액을 검토한 뒤 제출한다.
6. 제출 후에는 복지관 검증 대기 읽기 상태가 된다.

각 상태에는 하나의 주 행동만 둔다. 복지관 지원금 검증·최종 완료를 수리사가 수행하는 shortcut은 없다. QR camera/lookup 계약 전에는 스캔 기능이 동작하는 척하지 않고 공개코드 대조로 표시한다.

## 시각·자동 검증

- mobile unit: 18 files, 248 tests
- Android Expo export 성공
- iOS Expo export 성공
- Playwright mobile/console 4 flows passed
- 새 screenshot: repairer list, job workspace

스크린샷 검토에서 390px 폭의 제목·카드·CTA 잘림은 확인되지 않았다. 모바일 웹 검토는 TalkBack/VoiceOver, Android back gesture, iPhone keyboard avoidance를 증명하지 않으므로 실기기 smoke가 남아 있다.

## 알려진 제한

- 일정 입력은 데모 예시 시각이며 native OS date/time picker가 아직 없다.
- 비용 제출만 구현됐고 진단·부품·작업 항목의 구조화 계약은 아직 없다.
- QR camera와 공개코드 lookup endpoint가 아직 없다.

