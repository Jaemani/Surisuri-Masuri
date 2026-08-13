# ADR-0059 — 센터 검증과 지원금 집행은 분리된 권위 명령과 projection 재조회로 연결한다

- 상태: accepted / local synthetic UI·Firestore Emulator 검증
- 결정일: 2026-08-13
- 영향 범위: 복지관 수리 검증, 지원금 execution, console repair·ledger projection
- 선행 결정: [ADR-0043](./ADR-0043-welfare-center-repair-operations-product-core.md)

## 맥락

기존 콘솔은 `new → assigned → submitted → verified` demo stage만 증가시켰다. `verified` 화면은 실제 `center_verified` 명령이나 지원금 execution 없이도 집행 완료처럼 보일 수 있었고, 실제 Firebase adapter의 명시적 transition·ledger command는 UI에서 사용되지 않았다.

## 결정

- 센터 검증과 지원금 집행을 하나의 원자 명령처럼 표시하지 않는다.
- 순서는 `center_verified command → repairs/ledger projection 재조회 → execution command → projection 재조회`로 고정한다.
- 검증 성공 뒤 집행 또는 read refresh가 실패하면 상태를 `검증 완료·집행 대기`로 남기며 검증 명령을 자동 재전송하지 않는다.
- 두 mutation은 서로 다른 idempotency key를 사용하고 actor는 ID token에서 서버가 결정한다.
- execution은 `center_verified` 또는 `completed` work order에서만 허용한다. `repairer_submitted` 상태에서는 거부한다.
- 운영자 repair projection은 domain status, revision, billed amount와 tenant·person·policy가 검증된 purpose-limited subsidy context만 반환한다. 계정이 연결되지 않았을 때는 해당 이용자의 유효 계정이 정확히 하나일 때만 후보를 제공한다.
- `executed` 표시는 work order 상태가 아니라 실제 immutable execution transaction 존재로 판정한다.
- ledger row는 work order ID가 아닌 고유 transaction ID를 React key와 감사 식별자로 사용한다.

## 검증과 한계

Local synthetic console에서 두 단계 명령과 원장 갱신을 검증했고, Firestore Emulator에서 검증 전 execution 거부·검증 후 execution·typed projection·중복 transaction ID 거부를 확인했다.

현재 console composition은 여전히 명시적 demo source다. Firebase Auth/App Check를 주입한 production composition, 실제 subsidy decision document 검증, 실제 기관·수리·지원금 데이터, staging/production 배포는 완료되지 않았다.
