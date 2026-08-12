# 2026년 8월 제품 증거

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
