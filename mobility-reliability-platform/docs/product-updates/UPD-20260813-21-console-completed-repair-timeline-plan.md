# UPD-20260813-21 — 복지관 콘솔 완료 수리 타임라인 증분 계획

- 기준일: 2026-08-13
- 상태: implemented / local·Emulator·synthetic·web visual 검증
- 로드맵 위치: R09 Device Timeline & Reliability
- 대상: 복지관 운영자 콘솔의 기기 상세·수리 운영 읽기 projection

## 제품 변화

복지관 콘솔에 완료 수리 archive 기반의 기기 타임라인을 추가한다. 모바일의 `내 기기`와 같은 `repairs/{repairId}` 및 `items` 사실을 기준으로 사용하되, 콘솔에는 운영에 필요한 공개 기기 코드·완료일·구조화된 작업 항목만 목적 제한해 제공한다.

진행 중 수리 요청은 수리 운영 queue에서 계속 관리하고, 완료 archive timeline에는 넣지 않는다. 부품 catalog/component linkage가 없는 현재 범위에서는 `replace` action을 실제 부품 설치 상태로 해석하지 않는다.

## 완료 범위

- server-side console read-time replay와 tenant/device 검증
- console DTO와 repository decoder의 명시적 contract
- device detail 또는 repair detail에서의 timeline presentation
- checksum·identity·민감값 누출·빈 history 테스트
- local synthetic/Firestore Emulator/Playwright visual 증거

## 확인 결과

- console repository typecheck와 16개 parser/adapter test 통과
- domain-command local 20개 test 및 Firestore Emulator 11개 scenario 통과
- console production build 및 Playwright 3개 flow 통과
- `console-device-timeline` screenshot을 직접 확인해 목록→상태→타임라인→다음 조치 위계를 검토
- dashboard의 기기 metric이 기기 관리로 이동하도록 routing 오류 수정

이 결과는 local synthetic 및 Emulator 증거다. production Firebase, 실제 기관 데이터, field 성과, component lifecycle, async current projection을 주장하지 않는다.

관련: [ADR-0051](../decisions/ADR-0051-console-completed-repair-timeline-read-time-replay.md), [R09](../reports/fixed/2026-09-15.md), [EVD-20260813-017](../evidence/2026-08-product.md#evd-20260813-017--console-완료-수리-타임라인-증거-계획)
