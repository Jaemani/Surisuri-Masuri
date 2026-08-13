# 2026년 8월 제품 증거

## EVD-20260813-025 — 센터 검증·지원금 집행 authority loop

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local WSL2 / synthetic console / Firestore Emulator
- 출처: domain command store·purpose-limited projections, console repository·UI, unit·Emulator·Playwright tests
- 상태: generated / 사람 검토 대기
- 확인 항목: center verification과 execution 분리, 명령 사이 projection refresh, partial-success 표시, distinct idempotency, verification 전 execution 거부, validated subsidy context, immutable transaction 기반 executed 상태, unique ledger transaction ID
- 저장 위치: 코드 `apps/console/src/App.tsx`, `apps/console/src/data/productOperationsRepository.ts`, `services/domain-command/src/firebase-store.ts`, `services/domain-command/src/projection-store.ts`; 테스트 `apps/console/src/data/productOperationsRepository.test.ts`, `services/domain-command/test/firestore-emulator.test.ts`, `services/domain-command/test/projection-emulator.test.ts`, `tests/e2e/console-web.spec.ts`; 시각 증거 `tests/e2e/console-web.spec.ts-snapshots/console-repair-authority-review-console-chromium-linux.png`
- 접근 권한: 모두 repository working tree에 있으며 repository 접근 권한이 있는 개발자만 읽을 수 있다. Firestore Emulator 데이터는 local process에만 존재하고 production Firebase project나 public bucket에 업로드하지 않았다.
- 재현 명령 — console synthetic adapter: `pnpm --filter @mobility-reliability/console test`, `pnpm --filter @mobility-reliability/console typecheck`, `pnpm --filter @mobility-reliability/console build`, `pnpm test:e2e --project=console-chromium` (local Vite server 필요)
- 재현 명령 — Emulator server/projection 경계: `pnpm --filter @mobility-reliability/domain-command test`, `pnpm --filter @mobility-reliability/domain-command test:emulator` (Firestore Emulator 필요)
- 검증: console 25 tests/typecheck/build, domain 32 tests, Emulator 19 scenarios, Playwright console 6 flows. Playwright와 Emulator 수치는 서로 다른 adapter/server 경계의 결과이며 하나의 production composition 테스트가 아니다.
- 시각 증거 SHA-256: `a80adf54fb43c17d9e79526008e3caec1f51201cdc815dc03cbcff0f8e9613df` (snapshot binary). 소스 working tree가 미커밋 상태이므로 source commit hash는 이 증거에 고정하지 않았다. 파일과 결과는 SHA-256 재계산 가능하다.
- 현재 증명하지 않는 것: 실제 기관·수리·지원금·사용자 데이터, 실제 승인·집행, production Firebase Auth/App Check composition·배포, 실제 기관 계정 lifecycle, 기관 이해도·접근성
- 사용처: [ADR-0059](../decisions/ADR-0059-center-verification-subsidy-authority-loop.md), [UPD-20260813-29](../product-updates/UPD-20260813-29-center-verification-subsidy-authority-loop.md), [HR-20260813-22](../reports/human/HR-20260813-22-center-verification-subsidy-authority-loop.md)

## EVD-20260813-024 — R10 합성 reliability 비교 presentation

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local WSL2 / CPU / synthetic only / web visual
- 출처: presentation JSON Schema·fixture, Python builder·validator, generated console artifact, React UI, Playwright snapshot
- 상태: generated / 사람 검토 대기
- 확인 항목: dataset/result/self-hash lineage, train curve와 untouched test metric 분리, aggregate-only component, controller abstention, UI의 generated artifact binding, field/per-device/production/safety/CTA false
- 검증: contracts 38 fixture cases, ML 전체 suite, console typecheck·16 tests·build, Playwright console 5 passed
- 시각 증거: `tests/e2e/console-web.spec.ts-snapshots/console-reports-baseline-comparison-console-chromium-linux.png`
- 현재 증명하지 않는 것: 실제 수리·주행·기관 데이터, field 성능·calibration, 개별 위험도, 실제 기관 이해도·접근성, Firebase·production 배포
- 사용처: [ADR-0058](../decisions/ADR-0058-r10-synthetic-reliability-presentation.md), [UPD-20260813-28](../product-updates/UPD-20260813-28-r10-synthetic-reliability-presentation.md), [HR-20260813-21](../reports/human/HR-20260813-21-r10-synthetic-reliability-presentation.md), [R10](../reports/fixed/2026-09-30.md)

## EVD-20260813-023 — console 예방점검 근거·유보 UI

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local WSL2 / synthetic / Expo web·Playwright console visual
- 출처: console 예방점검 React UI, E2E flow, visual snapshot
- 상태: generated / 사람 검토 대기
- 확인 항목: 운영 검토·판단 유보·등록 일정 요약, 근거 출처 분리, 확인 사실/부족 데이터/다음 조치, 점수 없는 abstention, 고장·안전 보증 금지 문구
- 검증: console typecheck/test/build, Playwright console 4 passed
- 시각 증거: `tests/e2e/console-web.spec.ts-snapshots/console-inspection-evidence-console-chromium-linux.png`
- 현재 증명하지 않는 것: 실제 기관 이해도·접근성, 실제 점검·field metric, Firebase·production 배포
- 사용처: [ADR-0057](../decisions/ADR-0057-console-inspection-evidence-ui.md), [UPD-20260813-27](../product-updates/UPD-20260813-27-console-inspection-evidence-ui.md), [HR-20260813-20](../reports/human/HR-20260813-20-console-inspection-evidence-ui.md), [R10](../reports/fixed/2026-09-30.md)

## EVD-20260813-022 — R10 local synthetic reliability baseline

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local WSL2 / CPU / synthetic only
- 출처: `reliability_dataset.py`, `reliability_baseline.py`, 신규 unit tests
- 상태: generated / 사람 검토 대기
- 확인 항목: deterministic episode/hash, explicit replacement risk reset, device-group/time holdout, future-label leakage 차단, fixed interval·distance·Kaplan–Meier 동일 cohort 평가, data-insufficient abstention, semantic count reconciliation
- 검증: 신규 reliability 14 tests, 전체 ML 87 tests, Ruff format·lint 통과
- privacy·안전 경계: 결과에 episode/group ID·outcome time·raw GPS·좌표·PII 없음, `trainingPerformed=false`, deployment false/defer
- 현재 증명하지 않는 것: 실제 수리·주행 export, 실제 cohort/metric/risk curve, field 성능, 모델 학습, 모바일 추론, Firebase·production 배포
- 사용처: [ADR-0056](../decisions/ADR-0056-r10-synthetic-reliability-baseline.md), [UPD-20260813-26](../product-updates/UPD-20260813-26-r10-synthetic-reliability-baseline.md), [HR-20260813-19](../reports/human/HR-20260813-19-r10-synthetic-reliability-baseline.md), [R10](../reports/fixed/2026-09-30.md)

## EVD-20260813-021 — reliability baseline time-split·censoring·abstention contract

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local / synthetic / contract fixture
- 출처: `reliability-baseline-result.v1.schema.json`, valid/invalid fixture, `packages/contracts/scripts/validate-fixtures.mjs`
- 상태: generated / 사람 검토 대기
- 확인 항목: synthetic-only 범위, `device-group-time-holdout.v1`과 세 시간창, leakage flag, counts reconciliation flag, fixed interval·누적거리·Kaplan–Meier 구조, component abstention, `deploymentAuthorized=false`/`deploymentDecision=defer`, raw coordinate·field/training/deployment claim 차단
- 검증: `rtk pnpm --filter @mobility-reliability/contracts test` — 전체 36개 fixture case 통과, reliability baseline valid 1개·invalid 1개 포함
- privacy·안전 경계: raw GPS·좌표·PII를 허용하지 않으며 부품 linkage 없는 상태 추정을 계약 결과로 만들지 않음
- 현재 증명하지 않는 것: 실제 수리·주행 export, event/censoring 산출, risk curve·calibration·confidence interval, 부품별 성능, field·production 사용과 배포
- 사용처: [ADR-0055](../decisions/ADR-0055-reliability-baseline-result-contract.md), [UPD-20260813-25](../product-updates/UPD-20260813-25-reliability-baseline-result-contract.md), [R10](../reports/fixed/2026-09-30.md)

## EVD-20260813-020 — legacy repair→device-state event local dry-run bridge

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local / synthetic / contract fixture
- 출처: strict legacy bridge, `legacy-device-event-dry-run.v1`, unit tests
- 상태: generated / 사람 검토 대기
- 확인 항목: verified source 승인, device·repair UUID crosswalk, deterministic event ID, quarantine, input/output hash, dry-run/write/deployment false
- 검증: legacy importer 9 passed, contracts 34 fixture cases passed
- privacy·안전 경계: raw text·PII·money·UID·GPS 제외, component event 추정 금지, Firestore API 호출 없음
- 현재 증명하지 않는 것: 실제 legacy export·이관 건수·Firestore import·current/shadow 반영, production·복지관·field 성과
- 사용처: [UPD-20260813-24](../product-updates/UPD-20260813-24-legacy-repair-dry-run-bridge-plan.md), [R09](../reports/fixed/2026-09-15.md)

## EVD-20260813-019 — Firestore shadow projection atomic promotion

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local / synthetic / Firestore·Rules Emulator
- 출처: device state projection store, Emulator tests, Firestore Rules tests
- 상태: generated / 사람 검토 대기
- 확인 항목: shadow write 후 authoritative device current와 per-device checkpoint의 atomic promotion, 동일 replay의 idempotent convergence, input drift·corrupt shadow·checksum drift fail-closed, client direct read/write deny
- 검증: domain-command Emulator 14 passed, Rules Emulator 40 passed
- side-effect 경계: promotion/replay retry에서 FCM·SMS·외부 API·수리 command·지원금 ledger 변경이 없음
- 현재 증명하지 않는 것: production Firebase, 실제 복지관·field 성과, Cloud Tasks/Pub/Sub 운영 worker와 배포
- 사용처: [UPD-20260813-23](../product-updates/UPD-20260813-23-firestore-shadow-promotion-plan.md), [R09](../reports/fixed/2026-09-15.md)

## EVD-20260813-018 — device current-state deterministic pure replay

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local / synthetic / contract fixture
- 출처: `device-state-event.v1`, domain-command pure projector와 unit test
- 상태: generated / 사람 검토 대기
- 확인 항목: normalized repair·explicit part/component·inspection·trip summary event의 canonical replay, projector version/checkpoint value, output checksum, out-of-order와 `asOf`, fail-closed validation
- 검증: contract 32 fixture cases, domain-command local 32 passed / 11 skipped
- privacy·안전 경계: raw GPS·좌표·PII·UID·지원금 account가 state/checkpoint/log에 없음; 명시적 linkage 없는 category/action은 component 상태를 만들지 않음
- 현재 증명하지 않는 것: Firestore shadow/checkpoint/pointer·async worker, production Firebase, 실제 복지관·field 성과, legacy import, 전체 component lifecycle
- 사용처: [UPD-20260813-22](../product-updates/UPD-20260813-22-device-current-state-replay-plan.md), [R09](../reports/fixed/2026-09-15.md)

## EVD-20260813-017 — console 완료 수리 타임라인

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local / Firestore Emulator / synthetic / web visual
- 출처: domain-command `devices` projection, console repository·기기 관리 UI, Playwright snapshot
- 상태: generated / 사람 검토 대기
- 확인 항목: verified completed repair/items의 기기별 bounded read, 기존 순서 독립 replay 재사용, tenant/device/identity/source 검증, strict console decoder, 민감값 미노출, 기기 선택→상태·타임라인→운영 CTA
- 검증: domain-command local 20 passed, Emulator 11 passed, console 16 passed/typecheck/build, Playwright console 3 passed, docs 227 files
- 시각 증거: `tests/e2e/console-web.spec.ts-snapshots/console-device-timeline-console-chromium-linux.png`
- Product Design 확인: 1440×1024에서 목록/상세 균형과 기기 상태→검증 이력→다음 조치 위계를 확인했다. screenshot만으로 실제 복지관 이해도·키보드 전체 순서·screen reader 접근성을 증명하지 않는다.
- 현재 증명하지 않는 것: production Firebase, 실제 복지관 사용, field 수리·데이터 이관, component lifecycle, async current projection
- 사용처: [UPD-20260813-21](../product-updates/UPD-20260813-21-console-completed-repair-timeline-plan.md), [HR-20260813-18](../reports/human/HR-20260813-18-console-completed-repair-timeline.md), [R09](../reports/fixed/2026-09-15.md)

## EVD-20260813-015 — 모바일 주행 종료 저장 확인

- 생성: 2026-08-13 / Codex 작업 실행
- 환경·데이터: local / synthetic / Expo Web preview
- 출처: 현재 작업 트리, `pnpm --filter @mobility-reliability/mobile typecheck`, `pnpm exec playwright test tests/e2e/mobile-web.spec.ts --project=mobile-chromium`
- 상태: generated / 사람 검토 대기
- 확인 항목: 시작→기록 중→종료 후 `방금 이동 기록을 저장했어요`와 `휴대폰에 안전하게 보관됨` 표시
- 증명: 종료 직후 사용자에게 보존 결과가 보이는 UI 상태 전이
- 한계: 실제 Android/iPhone OS 종료, background location, Firebase ACK, 거리 계산과 현장 사용을 증명하지 않음
- 사용처: [UPD-20260813-19](../product-updates/UPD-20260813-19-mobile-session-completion-summary.md), [ADR-0049](../decisions/ADR-0049-mobile-session-completion-summary.md), [HR-20260813-05](../reports/human/HR-20260813-05-mobile-session-summary.md)

## EVD-20260813-001 — 제품 중심 모바일과 수리·지원금 도메인 기반

- 상태: generated / 사람 검토 대기
- 환경: WSL2, Node.js 22, local/synthetic demo only
- 관련 결정: [ADR-0043](../decisions/ADR-0043-welfare-center-repair-operations-product-core.md)
- 관련 업데이트: [UPD-20260813-01](../product-updates/UPD-20260813-01-product-centered-mobile-and-domain.md)

### 검증 항목

| 대상 | 명령 | 결과 |
| --- | --- | --- |
| 계약 | `pnpm --filter @mobility-reliability/contracts test` | valid/invalid fixture 통과 |
| 상태기계·원장 | `pnpm --filter @mobility-reliability/domain-workflows test` | Node test 6개 통과 |
| 권한 | `pnpm check:firebase` | Firestore/Storage Emulator 통과 |
| 모바일 타입 | `pnpm --filter @mobility-reliability/mobile typecheck` | 통과 |
| 모바일 회귀 | `pnpm --filter @mobility-reliability/mobile test` | 15 files, 229 tests 통과 |
| 웹 제품 흐름 | `pnpm exec playwright test` | 모바일 2개, 복지관 콘솔 2개 통과 |
| 워크스페이스 | `pnpm check && pnpm test && pnpm build` | 문서·계약·Rules·모바일·콘솔·Go·Python 포함 통과 |

### 시각 기준

- 모바일 snapshot: `tests/e2e/mobile-web.spec.ts-snapshots/`
- 콘솔 snapshot: `tests/e2e/console-web.spec.ts-snapshots/`
- 모바일 홈은 진행 중 수리·다음 약속·지원금·기기 상태를 GPS 사용량 기록보다 먼저 표시한다.
- 콘솔은 오늘 할 일과 수리 상태 보드, 수리 상세, 지원금 원장을 기본 업무 정보로 표시한다.

### 주장 경계

- 이 증거는 local code, emulator와 deterministic demo UI의 실행 가능성을 증명한다.
- 실제 Firebase 프로젝트 배포, Domain Command API 연결, 실사용자 데이터 이관, 현장 수리 처리, 공적 보조금 집행을 증명하지 않는다.
- Playwright는 Expo Web/Vite에서의 UI·상태 전이를 증명하며 Android/iPhone native 동작을 증명하지 않는다.
# EVD-20260813-02 — Firebase Domain Command transaction 경계

- 분류: `LOCAL_EMULATOR`, `SYNTHETIC`
- 대상: 수리 접수·상태 전환·지원금 원장
- 실행: `pnpm --filter @mobility-reliability/domain-command test:emulator`
- 결과: Firestore Emulator 4개 시나리오 통과
- 확인 항목: canonical path, snake_case storage, idempotent replay/conflict, optimistic concurrency, assigned-repairer enforcement, person-scoped subsidy account
- 제한: production deployment와 field evidence가 아니며 실제 사용자·기관 데이터를 사용하지 않음

# EVD-20260813-03 — 모바일·콘솔 운영 repository adapter

- 분류: `LOCAL_TEST`, `SYNTHETIC`
- 모바일: 17 files / 240 tests, typecheck 통과
- 콘솔: 9 tests, typecheck와 production build 통과
- 확인 항목: ID token·App Check 주입, command body, Idempotency-Key, revision, server projection validation, fail-closed error
- 금지 경계: 두 adapter 모두 Firestore direct write와 production→demo error fallback 없음
- 제한: 실제 Firebase token·배포·현장 data를 사용하지 않았고 native device network 호출을 증명하지 않음

# EVD-20260813-04 — 목적 제한 제품 읽기 projection

- 분류: `LOCAL_EMULATOR`, `SYNTHETIC`, `LOCAL_TEST`
- 서버: Domain Command unit 10개, Firestore Emulator command 4개 + projection 5개 통과
- 모바일: typecheck, 17 files / 242 tests 통과
- 콘솔: typecheck, 10 tests, production build 통과
- 확인 항목: Firebase ID token·App Check·tenant scope HTTP 경계, 역할별 mobile union, operator-only console projection, assigned-repairer filter, DTO redaction, nested tenant fail-closed, bounded reads, Functions endpoint 정렬
- 금지 경계: `privatePeople`, raw GPS/trip, Storage path, repairer subsidy projection, production→demo fallback 없음
- 제한: production Firebase 배포/Auth/App Check/native network/현장 사용 증거가 아니며 guardian 대상 projection과 cross-organization grant는 미구현

# EVD-20260813-05 — 완료 수리 이력 materialization

- 분류: `LOCAL_EMULATOR`, `SYNTHETIC`
- 실행: `pnpm --filter @mobility-reliability/domain-command test:emulator`
- 결과: command 5개 + projection 5개, 총 10 scenarios 통과
- 확인 항목: 활성 tenant 수리소 exact read, 미등록 수리소 거부, 전체 수리 상태 전이, 완료 work order와 immutable repair의 atomic write, source quality와 금액·기기·수리소 linkage
- 개인정보 경계: 완료 repair에 raw issue summary와 memo를 복사하지 않음
- 제한: repair items·부품 설치/제거·production 배포·실제 현장 수리 완료는 미검증

# EVD-20260813-06 — 수리 접수·복지관 action UI

- 분류: `LOCAL_TEST`, `SYNTHETIC`, `WEB_VISUAL`
- 모바일: typecheck, 18 files / 246 tests, Android/iOS Expo export 통과
- 콘솔: typecheck, 10 tests, production web build 통과
- Playwright: 모바일 2개 + 콘솔 2개 통과
- 시각 증거: `mobile-repair-intake`, `mobile-repair-review`, `console-repairs` snapshot
- 확인 항목: 모바일 category/detail/funding/amount validation과 review, stable idempotency input, projection-pending read-only recovery, console stage-aware synthetic assignment와 operator read-only wait
- 제한: 실제 Firebase command, native TalkBack/VoiceOver, 현장 직원·이용자 사용 증거가 아님

# EVD-20260813-07 — 수리사 단계형 작업공간

- 분류: `LOCAL_EMULATOR`, `LOCAL_TEST`, `SYNTHETIC`, `WEB_VISUAL`
- 서버: 수리사 command exact allowlist, server-owned submitted time, 배정 UID query와 purpose-limited projection
- 모바일: typecheck, 18 files / 248 tests, Android/iOS Expo export 통과
- Playwright: 모바일 2개 + 콘솔 2개 통과
- 시각 증거: `mobile-repairer-list`, `mobile-repairer-workspace` snapshot
- 확인 항목: 일정 확정 → 작업 시작 → 비용 제출 → 복지관 검증 대기, 공개코드 대조, 한 단계 한 CTA, authoritative projection refresh
- 제한: native picker, QR camera/lookup, 구조화 작업 항목, 실제 Firebase 배포와 실기기 접근성은 미검증

# EVD-20260813-08 — 구조화 수리 항목 end-to-end

- 분류: `LOCAL_EMULATOR`, `LOCAL_TEST`, `SYNTHETIC`, `WEB_VISUAL`
- command: category/action/quantity/line amount exact allowlist, 합계-청구액 일치 검증
- storage: work order structured map 및 완료 repair item atomic materialization
- UI: 수리사 부위·처리 선택, 복지관 제출 항목 검증과 빈 항목 fail-closed
- 검증: mobile 248 tests, console 10 tests/build, domain 12 tests, Emulator 10, Playwright 4 passed
- 제한: 현장 분류 적합성, 복수 항목 편집 UX, 부품 catalog/component linkage와 실제 배포는 미검증

# EVD-20260813-09 — 현장 기기 공개코드 gate

- 분류: `LOCAL_TEST`, `SYNTHETIC`, `WEB_VISUAL`
- 확인 항목: projection 공개코드와 수동 입력 일치 전 일정·작업 action 비활성화, 대소문자 정규화, 일치 상태 안내
- 검증: mobile 248 tests, TypeScript, Playwright 4 passed, updated repairer workspace snapshot
- 제한: QR camera/lookup, 위조 방지, 기기 소유권, native accessibility는 미검증

# EVD-20260813-10 — 네이티브 QR scanner integration

- 분류: `LOCAL_TEST`, `NATIVE_EXPORT`, `SYNTHETIC`, `WEB_VISUAL`
- 확인 항목: Expo Camera permission flow, QR-only scanner, bounded payload parser, 수동 입력 fallback, 동일 기기-code gate
- 검증: mobile 19 files / 250 tests, Android/iOS Expo export, Playwright 4 passed
- 제한: 실제 Android/iPhone 카메라, 저조도·훼손 QR, 위조 방지와 서버 lookup은 미검증

# EVD-20260813-11 — 네이티브 방문 일정 선택

- 분류: `LOCAL_TEST`, `NATIVE_EXPORT`, `WEB_VISUAL`, `SYNTHETIC`
- 확인 항목: 현재 이후·180일 이내 client guard, native `Date`의 canonical ISO 변환, Asia/Seoul 표시, 선택값 변경 시에만 idempotency key reset, WSL web fallback
- 검증: mobile typecheck, 20 files / 253 tests, Android/iOS Expo export, Playwright mobile 2 flows passed
- 시각 증거: `tests/e2e/mobile-web.spec.ts-snapshots/mobile-repairer-schedule-mobile-chromium-linux.png`
- 제한: 실제 Android/iPhone picker 조작, 취소·시간대 경계·native accessibility와 production 예약은 미검증

# EVD-20260813-12 — 모바일 복수 구조화 수리항목

- 분류: `LOCAL_TEST`, `SYNTHETIC`, `WEB_VISUAL`
- 확인 항목: 1~20개 항목 editor, 항목별 category/action/quantity/line amount, 합계 파생, 제출 전 검토, 수정 후 입력 보존, payload-signature idempotency key
- command 확인: label과 임의 field를 제외한 exact `workItems` POST body, billed total 일치 client guard
- 검증: mobile typecheck, 21 files / 258 tests, Playwright mobile 2 flows passed
- 시각 증거: `tests/e2e/mobile-web.spec.ts-snapshots/mobile-repairer-submit-review-mobile-chromium-linux.png`
- 제한: 실제 수리·수리사·복지관·보조금 집행, 현장 분류 타당성, native keyboard/accessibility와 production Firebase는 미검증

# EVD-20260813-13 — field holdout·feature admission 계약

- 분류: `LOCAL_TEST`, `CONTRACT_FIXTURE`, `NO_FIELD_DATA`
- 확인 항목: field_pilot evaluation-only manifest, training/deployment 차단, raw-coordinate field 금지, collection→label freeze→evaluation chronology, 가명 group·trace identity, known-label eligibility, exact trace·batch·hash linkage, label-free field feature output
- 검증: Node valid/invalid JSON fixture와 Python schema·semantic·hash·tamper validator test
- 안전 경계: 오류에는 path/reason만 포함하고 fixture value·좌표를 출력하지 않음
- 제한: 무작위 fixture와 bridge 검증용 합성 batch만 사용했으며 실제 동의, Android/iPhone trace, server-only consent/artifact 대조, field metric과 ONNX 진입은 미검증

# EVD-20260813-14 — frozen PyTorch load-only artifact

- 분류: `LOCAL_TEST`, `SYNTHETIC`, `NO_FIELD_DATA`
- 확인 항목: model state, train-only normalization, ordered feature/class keys, training dataset·manifest, weights와 metadata의 독립 hash; CPU `weights_only` strict load; gradient-off inference; review feature abstain; coordinate/label-free prediction output
- 검증: Node model artifact valid/invalid fixture와 Python deterministic export, load, prediction, weights·metadata tamper test
- 결정: artifact metadata의 training source는 synthetic, deployment decision은 `defer`
- 제한: 실제 field data·동의·복지관·사용자·실기기, ONNX·양자화·모바일 추론·production 배포와 현장 성능을 증명하지 않음

# EVD-20260813-15 — field evaluation-only harness 계약

- 분류: `LOCAL_TEST`, `CONTRACT_FIXTURE`, `SYNTHETIC_BRIDGE`, `NO_FIELD_DATA`
- 확인 항목: evaluation window, frozen model state·rules version 일치, exact holdout/trace/batch/feature hash linkage, 동일 scored cohort의 rules/PyTorch 비교, label/feature review·missing count reconciliation, 학습 API 미호출
- 결과 계약: `trainingPerformed=false`, `deploymentAuthorized=false`, `deploymentDecision=defer`, `evaluation_only_no_deployment`
- privacy 경계: result에 pseudonymous group, consent digest, feature values, raw samples·coordinates, Firebase/Storage identity 없음
- 제한: 생성 batch 하나로 evaluation 경계를 검증했으며 실제 field metric, 동의, 참가자 수, 모델 효용, confidence interval, ONNX·모바일·production 배포 증거가 아님

# EVD-20260813-16 — 완료 수리 archive 기기 타임라인 replay

- 분류: `LOCAL_TEST`, `LOCAL_EMULATOR`, `SYNTHETIC`, `WEB_VISUAL`
- 확인 항목: 입력 순서 독립 replay와 checksum, as-of 제외, duplicate/device/tenant/source/category/action/quantity fail-closed, verified repair/items 기반 beneficiary timeline, 민감값 미노출
- 검증: domain-command pure tests 7개 포함 local suite, Firestore Emulator command/projection 10 scenarios, mobile 21 files / 258 tests, Playwright mobile 2 flows
- 시각 증거: `tests/e2e/mobile-web.spec.ts-snapshots/mobile-device-timeline-mobile-chromium-linux.png`
- Product Design 확인: 390×844 snapshot에서 기기 요약→타임라인→수리 도움 위계와 하단 tab 상태를 확인; screenshot만으로 native 접근성·TalkBack/VoiceOver는 확인하지 못함
- 제한: read-time completed-repair slice이며 전체 Digital Twin, async current projection, legacy import, 부품 lifecycle, 실제 수리·사용자·production 배포가 아님
# EVD-20260813-026 — R11 calibration estimability와 판단 유보

- 범위: local deterministic synthetic evaluator·contract·console presentation
- 산출물: `reliability-calibration-assessment.v1`, `r11-calibration-estimability.v1`, R11 보고서 readiness section
- 결과: validation 배터리 8건·브레이크 6건·컨트롤러 3건으로 최소 30건 미달. 세 component 모두 `not_estimable`; calibration metric·curve 0개, fallback `fixed_interval_and_human_review`, deployment defer.
- 계보: dataset SHA-256 `e322548d33bb1c8a3014a21ae0c527a8862c2780f49c1589c1106d51064e63a2`, R10 result SHA-256 `c6712452fbc2b967b3d896ca1877fc6810df372aacd487587155cc3dd7b9278f`, assessment SHA-256 `a2be7299b68d29a71ed0f89fd12e8571988942d1f4f8acf0eb9898c4d5eb8de7`.
- 저장 위치: `packages/contracts/schemas/reliability-calibration-assessment.v1.schema.json`, valid/invalid fixture, `services/ml/src/mobility_ml/reliability_calibration.py`, Python test, `apps/console/src/App.tsx`, Playwright report snapshot. Snapshot SHA-256 `1a46fe4fe64094a01e7e8a6b5fff9305071a22449f1b8c363fadcf427fb9b0e5`.
- 재현: `pnpm --filter @mobility-reliability/contracts test`; `uv --directory services/ml run --locked --extra dev pytest -q`; `pnpm exec playwright test tests/e2e/console-web.spec.ts --project=console-chromium`.
- 접근: repository 접근 개발자. 합성 fixture 외 production Firebase·외부 저장소 접근 없음.
- 경계: 합성 holdout evaluator의 fail-closed 동작만 증명한다. 실제 수리 fact, 현장 보정, 개별 위험, 고장 시점, 안전, 운영 조치, 배포·기관 검증을 증명하지 않는다.

관련: [ADR-0060](../decisions/ADR-0060-r11-calibration-estimability-gate.md), [UPD-20260813-30](../product-updates/UPD-20260813-30-calibration-readiness-presentation.md), [HR-20260813-23](../reports/human/HR-20260813-23-calibration-readiness-review.md)
# EVD-20260813-027 — R12 Fact bundle과 근거 연결형 fallback 보고서

- 범위: local deterministic synthetic report package·console presentation
- 결과: R11 assessment→5 typed Facts→5 grounded claims. LLM 0회, fallbackUsed true, 모든 claim Fact coverage 100%.
- 검증: report-evidence 7 tests, recursive report/bundle hash, typed fact value, forged scope, dangling/duplicate evidence, camel/snake PII·좌표 key 차단, console snapshot exact equality, Playwright console 6 flows.
- 저장: `packages/report-evidence`, `apps/console/src/data/r12GroundedReport.json`, `apps/console/src/App.tsx`, `tests/e2e/.../console-reports-grounded-evidence-console-chromium-linux.png`.
- 재현: `pnpm --filter @mobility-reliability/report-evidence test`; `pnpm --filter @mobility-reliability/console typecheck`; `pnpm exec playwright test tests/e2e/console-web.spec.ts --project=console-chromium`.
- 경계: 합성 aggregate fallback validator와 UI만 증명한다. 실제 Fact Store, LLM, 기관·사용자 보고서, production Firebase, 사람 승인·발행·운영 조치를 증명하지 않는다.

관련: [ADR-0061](../decisions/ADR-0061-r12-grounded-report-fallback.md), [UPD-20260813-31](../product-updates/UPD-20260813-31-r12-grounded-report.md)

# EVD-20260813-028 — R12 source assessment self-hash gate

- 범위: local deterministic Node report boundary·synthetic R11 fixture
- 확인: R11 Python canonical JSON과 호환되는 locale 비의존 key 정렬, assessment 전체 self-hash 재계산, root/lineage/policy/fact-boundary/limitations/component allowlist
- 공격 회귀: hash를 갱신하지 않은 nested count 변조 거부, test tuning을 허용하도록 의미를 바꾼 입력 거부
- 검증: `pnpm --filter @mobility-reliability/report-evidence check`; package test 9/9 통과
- 경계: local source 구조·무결성만 검증한다. 작성자 인증, 디지털 서명, 실제 dataset/result 재계산, Firebase Fact Store, 기관 검토·발행과 production 배포를 증명하지 않는다.

관련: [ADR-0062](../decisions/ADR-0062-r12-source-assessment-integrity.md), [UPD-20260813-32](../product-updates/UPD-20260813-32-r12-source-integrity.md), [HR-20260813-24](../reports/human/HR-20260813-24-r12-source-integrity-review.md)
