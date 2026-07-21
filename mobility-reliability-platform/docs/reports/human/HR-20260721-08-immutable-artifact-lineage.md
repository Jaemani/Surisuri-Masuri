---
id: HR-20260721-08
report_type: requested
status: draft
period_start: 2026-07-21
period_end: 2026-07-21
issued_at: TBD
roadmap_month: M3
technical_gate: Immutable Telemetry Artifact Lineage
author: project owner / Codex draft
reviewer: TBD
audience: project team and technical reviewer
---

# 요청 기술 리포트: 원본·manifest·receipt 저장 계보

## 한눈에 보기

- 이번 회차의 사전 목적: Firestore reservation 뒤 raw GPS batch와 server manifest를 덮어쓰기 없이 저장하고, 부분 실패 재시도가 동일 bytes·generation으로 수렴하도록 한다.
- 보고 기준일의 실제 상태: provider-neutral artifact contract, Cloud Storage adapter, service recovery 흐름과 Firestore 전체 lineage finalizer를 구현했고 local race test와 pinned official Storage testbench를 통과했다.
- 가장 중요한 차이 또는 위험: 실제 staging IAM·lifecycle·retention과 lease·fencing·sweeper는 아직 없으며 executable에도 주입하지 않았다. 따라서 production 저장 완료나 서비스 활성화를 주장하지 않는다.
- 사람에게 필요한 결정·확인: 정밀 원본 30일 기본 보존과 최대 90일 예외 승인 절차, staging Firebase/GCP project와 service account 준비 일정을 확정해야 한다.

## 1. 계획

> 이 섹션은 8개월 로드맵의 7월 2차 Trusted Telemetry Platform gate이며 실제 성과와 분리한다.

- 로드맵상 위치: M3 / atomic admission 이후 immutable Storage artifact와 receipt 연결
- 계획한 기술 주제: deterministic gzip, canonical manifest, generation precondition, partial failure replay, receipt/object lineage
- 예상 산출물: artifact port, Cloud Storage adapter, manifest golden fixture, full-lineage Firestore finalizer, provider integration evidence
- 검토할 질문: 같은 경로를 덮어쓰지 않는가, raw와 manifest 사이 또는 finalizer 앞에서 중단돼도 복구되는가, 실제 저장 bytes를 generation/hash/checksum으로 검증하는가
- 계획 완료 조건: staging bucket IAM·lifecycle·retention과 lease/fencing/sweeper까지 검증한 뒤 verifier·admission·artifact store를 executable에 연결한다.

## 2. 실제

| 항목 | 상태 | 확인된 결과 | 계획 대비 차이 | 검증 환경 |
| --- | --- | --- | --- | --- |
| Artifact domain contract | `검증됨` | raw/manifest 각각 path·SHA-256·CRC32C·size·generation·metageneration과 replay를 표현 | runtime DTO에만 존재 | Docker Go / synthetic |
| Canonical manifest | `검증됨` | ordered snake_case JSON과 golden bytes, UTC 시각, identity 금지 필드를 고정 | manifest version은 현재 v1만 지원 | unit/race test |
| Cloud Storage adapter | `부분 검증` | `DoesNotExist` write, 좁은 412/FailedPrecondition 분류, exact generation·compressed bytes replay 구현 | production ADC/IAM·quota 미검증 | memory backend + official testbench |
| Partial failure recovery | `검증됨` | raw 실패, raw 후 manifest 실패, artifact 후 finalizer 실패가 retry에서 중복 없이 전진 | lease owner와 sweeper는 없음 | service unit/race test |
| Conflict 정책 | `검증됨` | raw mismatch만 terminal reject, manifest/generic mismatch는 reserved 유지 | 운영자 복구 UI·DLQ 없음 | unit/race test |
| Firestore finalizer | `검증됨` | raw·manifest 전체 계보를 receipt revision 하나에 기록하고 필드별 mismatch를 거절 | actual Firestore+Storage 동시 E2E는 미검증 | fake transaction/race test |
| Provider integration | `부분 검증` | official testbench에서 신규·동일 replay·exact compressed generation read·다른 bytes conflict 통과 | testbench는 production이 아님 | WSL2 / pinned container |
| Runtime·staging | `미착수` | `/readyz`와 ingest는 계속 `503 adapters_unconfigured` | 의도적으로 fail-closed | 미검증 |

### 실제 결과 상세

- `body_hash`는 압축 전 HTTP bytes, `object_sha256`은 deterministic gzip bytes로 분리했다.
- raw object가 검증된 뒤 그 generation을 포함한 canonical manifest를 만들므로 manifest가 최신 경로를 암묵적으로 참조하지 않는다.
- collision 후보는 HTTP 412 또는 gRPC FailedPrecondition만 인정한다. 이후 조건 없는 attrs read로 generation을 찾고 exact generation과 `ReadCompressed(true)`로 실제 gzip bytes를 비교한다.
- raw 성공 후 manifest 오류는 raw를 지우지 않는다. 다음 retry가 raw exact replay를 확인한 뒤 manifest를 생성한다.
- manifest 성공 후 Firestore 오류는 다음 retry가 두 artifact를 모두 검증한 뒤 같은 lineage로 finalizer를 다시 호출한다.
- Firestore receipt는 raw와 manifest의 hash/checksum/size/generation을 모두 가지며 이미 stored인 receipt는 전체 필드가 같을 때만 idempotent success다.
- 데이터 유형: `synthetic | test`; 실제 GPS, UID/App ID, 이용자·복지관 데이터 없음

## 3. 근거

| 실제 주장 | 증거 ID·링크 | 검증 상태 | 확인자·확인일 |
| --- | --- | --- | --- |
| immutable raw·manifest·receipt 계약이 결정됨 | [ADR-0016](../../decisions/ADR-0016-immutable-telemetry-artifact-lineage.md) | `accepted` decision; runtime 증거 아님 | 문서 검토 필요 |
| canonical manifest, partial recovery와 finalizer 전체 계보가 local race test와 clean CI를 통과 | [EVD-20260721-016](../../evidence/2026-07.md#evd-20260721-016--immutable-telemetry-objectmanifest-lineage) | `verified` — [CI 29825767754](https://github.com/Jaemani/Surisuri-Masuri/actions/runs/29825767754) | Codex / 2026-07-21 |
| Cloud Storage client가 동일 bytes replay를 같은 generation으로 읽음 | [EVD-20260721-016](../../evidence/2026-07.md#evd-20260721-016--immutable-telemetry-objectmanifest-lineage) | `verified` — local official testbench와 clean CI 범위 | Codex / 2026-07-21 |
| production/staging bucket과 Cloud Run ingest가 활성화됨 | 확인 필요 — 현재 활성화하지 않음 | `미검증` | 해당 없음 |
| lease·fencing·sweeper가 orphan을 복구함 | 확인 필요 — 현재 구현하지 않음 | `미검증` | 해당 없음 |

## 결정·제품 변화·인시던트

- 관련 결정: [ADR-0016](../../decisions/ADR-0016-immutable-telemetry-artifact-lineage.md)
- 실제 제품 업데이트: 해당 없음 — runtime·사용자·운영 경로에는 연결하지 않음
- 인시던트: 해당 없음 — production·field 배포와 사용자 영향 없음
- 열린 위험: 만료 직전 pending replay, orphan raw/manifest, staging lifecycle drift, 실제 IAM/ADC와 startup wiring. [RSK-10](../../plans/RISK_REGISTER.md)은 계속 active다.

## 다음 회차

- 8개월 계획상 다음 주제: pending reservation lease, fencing token, sweeper와 generation-pinned orphan reconciliation
- 실제 상태를 반영한 다음 검증: expired/near-expiry reservation, 두 recovery worker 경쟁, raw-only·manifest-only·stored-missing artifact matrix
- 필요한 사람의 결정·지원: 원본 위치 lifecycle 30일 기본값, 최대 90일 예외 승인, staging project·bucket·service account 일정

## 회의·증빙 확인(실제 회의가 있었을 때만)

- 실제 회의 여부: 아니오
- 실제 일시: 해당 없음
- 실제 참석자: 해당 없음
- 사진·화상회의 증빙: 해당 없음
- 지출·영수증: 해당 없음
- 확인자·확인일: 해당 없음

## 발행 전 검토

- [x] 계획과 실제가 명확히 분리되어 있다.
- [x] memory fake·official testbench와 production/staging을 구분했다.
- [x] 합성·테스트와 field 데이터를 구분했다.
- [x] 제품 업데이트와 인시던트를 생성하지 않았다.
- [x] 참석자·사진·지출을 생성하거나 추정하지 않았다.
- [x] 민감정보와 원본 GPS 좌표가 없다.
- [x] EVD-20260721-016의 local 명령과 결과를 확인했다.
- [x] clean CI commit `478eb4ee677b781c72b3b43bd8b32abca8f17947`·run `29825767754`를 확정했다.
- [ ] reviewer와 발행일을 사람이 확정했다.
