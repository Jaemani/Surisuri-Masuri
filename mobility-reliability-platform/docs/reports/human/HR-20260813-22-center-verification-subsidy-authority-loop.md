# HR-20260813-22 — 센터 검증·지원금 집행 흐름 점검

- 발행일: 2026-08-13
- 상태: generated / 사람 검토 대기
- 대상 독자: 복지관 운영자·프로젝트 담당자
- 대상 기간: 2026-08-13 local synthetic increment (field 운영 기간 아님)
- 작성자: Codex 작업 실행
- 검토자: 사람 검토 대기
- 로드맵 월/게이트: 2026-09 / R10 Device Timeline & Reliability 후속 운영 gate

## 계획

센터 검증과 지원금 집행을 한 상태로 묶지 않고, `center_verified command → projection 재조회 → execution command → projection 재조회` 순서와 부분 성공 상태를 콘솔에서 설명할 계획이었다. Playwright 화면 검증과 Firestore Emulator server/projection 검증을 서로 분리해 확인하는 것도 계획에 포함했다.

## 실제 구현

수리 제출 건을 열면 제출 파트너·청구액·구조화 작업 항목과 세 개의 검증 체크가 보인다. 모두 확인해야 `검증 후 집행 요청`이 활성화된다. 검증과 집행 사이에는 최신 projection 확인 단계가 있으며, 두 작업은 하나의 성공으로 뭉쳐 표시되지 않는다.

검증만 성공한 경우에는 `집행 대기`, execution transaction까지 조회된 경우에만 `집행 완료`로 표시한다. 원장 행은 같은 수리 요청에 예약·집행이 함께 존재해도 거래 ID가 달라 충돌하지 않는다.

Playwright는 console의 synthetic demo adapter를 이용한 브라우저 흐름만 확인했다. Firestore Emulator 테스트는 domain-command server/store/projection 경계만 확인했다. 두 검증을 합쳐 production Firebase composition이 동작한다고 해석하지 않는다.

## 근거

- console unit/typecheck/build와 Playwright authority review snapshot: 화면의 체크리스트, projection 확인, 집행 대기·완료 표시 경계
- domain-command unit 및 Firestore Emulator 19 scenarios: 검증 전 execution 거부, typed projection, immutable transaction·거래 ID 분리
- 상세 명령과 저장 위치는 [EVD-20260813-025](../../evidence/2026-08-product.md#evd-20260813-025--센터-검증지원금-집행-authority-loop)에 기록했다.

## 사람 검토 요청

- 복지관 업무에서 검증 체크리스트와 3단계 순서가 자연스러운지
- `검증 완료·집행 대기`가 재시도 또는 담당자 확인이 필요한 상태로 명확한지
- 작업 항목과 금액만으로 집행 결정을 검토하기 충분한지, 추가 문서가 필요한지
- local 계정 binding·reversal·decision 검증을 실제 기관 승인 절차와 연결할 때 필요한 추가 문서와 역할이 무엇인지

## 한계

화면의 사람·기기·수리·금액은 합성 데이터다. 실제 복지관 사용자 테스트, 실제 subsidy decision 문서, production Firebase Auth/App Check composition, 계정 binding·reversal lifecycle, 배포·집행을 증명하지 않는다. 회의·참석자·사진·지출은 기록하지 않았다.

근거: [ADR-0059](../../decisions/ADR-0059-center-verification-subsidy-authority-loop.md), [UPD-20260813-29](../../product-updates/UPD-20260813-29-center-verification-subsidy-authority-loop.md), [EVD-20260813-025](../../evidence/2026-08-product.md#evd-20260813-025--센터-검증지원금-집행-authority-loop)
