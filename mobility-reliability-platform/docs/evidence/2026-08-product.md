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

### 주장 경계

- 이 증거는 local code, emulator와 deterministic demo UI의 실행 가능성을 증명한다.
- 실제 Firebase 프로젝트 배포, Domain Command API 연결, 실사용자 데이터 이관, 현장 수리 처리, 공적 보조금 집행을 증명하지 않는다.
- 브라우저 시각 검증은 통합 Playwright 작업 완료 후 별도 항목으로 추가한다.
