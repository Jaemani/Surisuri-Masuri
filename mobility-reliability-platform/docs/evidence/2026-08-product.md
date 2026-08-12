# 2026년 8월 제품 증거

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
