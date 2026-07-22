# 위험 등록부

## 1. 운영 방식

- 검토 주기: 매월 15일·말일 정기리포트 전
- 기준일: 2026-07-22
- 기록 책임: project owner; 자동 초안은 사람 검토 전 상태를 변경하지 않음
- 다음 정기 검토: 2026-07-31 fixed report 준비 시
- 상태: `watch`, `active`, `mitigated`, `accepted`, `closed`
- 영향: `critical`, `high`, `medium`, `low`
- 가능성: `high`, `medium`, `low`

위험은 미래 가능성이다. 실제 사용자 영향이 발생하면 인시던트 기준을 별도로 적용한다. 위험이 현실화되지 않았는데 사고처럼 쓰거나, 실제 사고를 위험 등록부만 갱신하고 끝내지 않는다.

## 2. 현재 위험

| ID | 위험 | 영향/가능성 | 탐지 신호 | 예방·대응 | 차단 조건 | 상태 |
| --- | --- | --- | --- | --- | --- | --- |
| RSK-01 | 상표·기존 코드 경계 혼입 | high/low | 기존 이름·asset·runtime import | greenfield ADR, dependency/license scan, archive는 참고만 | 외부 제품명·코드 출처 미확정 | active |
| RSK-02 | archive 경로 접근 불가 | low/high | GitHub 404/권한 오류 | 링크를 계보 식별자로만 기록, 로컬 자산·승인 데이터로 진행 | archive 내용을 실제 증거로 인용 | active |
| RSK-03 | Android/iOS background 정책 차이 | high/high | sample gap, OS kill, permission downgrade | development build, device/OS matrix, explicit trip, graceful resume | 기기별 실증 없이 background 완료 주장 | active |
| RSK-04 | GPS 정확도와 배터리 상충 | high/high | accuracy 저하, battery drain, heat | adaptive profile 비교, accuracy filter, Remote Config rollback | 허용치·비교 증거 없이 pilot | watch |
| RSK-05 | 정밀 위치·PII 노출 | critical/medium | 로그/Crashlytics/Git/화면 scan 검출 | identity 분리, no coordinate logs, masking, TTL, 최소권한 | 노출 가능 경로 미해결 | active |
| RSK-06 | 동의 철회·삭제 계보 실패 | critical/medium | withdrawn trip ingest, orphan object | consent revision authorizer와 atomic admission local 검증; ADR-0017에 pending recovery 재검사·generation-pinned deletion target 계획 | 철회를 관측한 pending recovery/sweeper의 새 artifact write=0 evidence 또는 staging 삭제 drill이 없으면 차단; admission commit 뒤 이미 진행 중인 owner는 별도 fence·reconciliation으로 검증 | active |
| RSK-07 | Firebase emulator 설정이 production에 유입 | critical/low | emulator env 존재, 서명검증 우회 | production startup fail-closed, config test, separate projects | guard wiring·startup test 없음 | active |
| RSK-08 | Firebase token은 유효하나 membership이 취소됨 | high/medium | inactive/revoked member request | 매 batch server membership 검사, token 정책 ADR | inactive membership 허용 | active |
| RSK-09 | client tenant/device/trip 주장 신뢰 | critical/medium | cross-tenant fixture가 write 성공 | server authorizer, path/field tenant consistency, Rules | deny matrix 미통과 | active |
| RSK-10 | partial failure로 object/manifest/receipt 불일치 | high/medium | reserved age, active lease 중복 처리, stale finalizer, orphan·stored-missing, hash·CRC·generation mismatch | 3-way transaction·ADR-0016 immutable artifact; ADR-0017에 monotonic fence·bounded classifier·forward/cleanup 분리 계획 | stale mutation=0, raw-only recovery, stored-missing hold, staging lifecycle 중 하나라도 미검증이면 차단 | active |
| RSK-11 | Firestore 비용이 sample 수에 비례 | high/medium | write count가 sample count와 선형 | Storage batch, small receipt/projection, listener 제한 | sample별 write 0을 staging usage test로 확인하지 못함 | active |
| RSK-12 | Docker context와 CI source 불일치 | high/medium | host test 통과·image compile 누락 | directory allowlist, Docker build CI, image smoke | 이미지에 새 adapter 누락 | active |
| RSK-28 | due query에서 손상 receipt가 보이지 않거나 mutable page가 후보를 지연함 | high/medium | `status`·`next_recovery_at` 누락/type drift, page 사이 due/state 변경·backfill, 같은 head 반복, checkpoint 정체 | ADR-0021 fixed-cutoff epoch, deterministic document cursor, malformed advisory item 격리, fresh claim, CAS checkpoint와 head wrap | 별도 bounded control-integrity audit와 production composite index `READY`·staging mutation/cardinality/비용 검증 전에는 scan complete를 snapshot·전체 무결성 complete로 해석하지 않음 | active |
| RSK-29 | reserved expiry cleanup이 이전 forward attempt를 `started`로 고아화해 감사·purge 원장을 모순시킴 | high/low | `cleanup_pending` receipt의 prior attempt가 `started`, lease owner/token 증거 제거 | ADR-0022 exact prior attempt 검증과 attempt+receipt 동일 transaction closure, missing·malformed write-zero, multi-clock earliest guard | 기존 orphan bounded audit와 local/clean CI regression 없이 cleanup claim·purge로 진행 금지 | mitigated |
| RSK-30 | cleanup이 late artifact mutation보다 먼저 시작하거나 forward/cleanup owner가 같은 receipt를 경쟁함 | high/low | quiet boundary 전 claim, cleanup receipt의 `next_recovery_at`, context 완료 뒤 추가 create, 복수 cleanup winner | ADR-0023 immutable transition policy, `11m > 5m lease + 5m StoreBatch`, cleanup-only transaction claim·attempt와 expired takeover, GCS pre/post cancel guard | immutable target·delete capability와 staging lifecycle/IAM·generation drill 전 actual delete 및 runtime activation 금지 | active |
| RSK-13 | WSL host tool 부재·네트워크 차이 | medium/high | Go/adb 부재, device가 localhost 실패 | fixed Docker, Windows ADB, host-gateway/Compose, WSL runbook | 검증 환경을 기록하지 않음 | active |
| RSK-14 | 실제 수리 export 부재·품질 미확정 | high/high | source manifest 없음, ID/필드 혼용 | mapping 설계와 dry-run 분리, quarantine, reconciliation | 실제 ML 성능·이관 완료 주장 | active |
| RSK-15 | 주행 라벨 모호성·표본 편향 | high/high | 낮은 agreement, 기종/사용자 편중 | label guide, uncertainty, group/time split, active review | label 품질 기준 미달 | watch |
| RSK-16 | 생존분석 사건 수 부족 | high/high | 부품별 event·censoring 부족 | baseline·data_insufficient, 범위 축소, CI 공개 | 표본 부족인데 위험 수치 노출 | watch |
| RSK-17 | train/test leakage | high/medium | 같은 기기·시점이 split 교차 | time/group split, manifest, leakage tests | split 재현 불가 | watch |
| RSK-18 | 모델 출력이 안전 보증으로 오해 | critical/medium | 고장 확정 문구, confidence 미표시 | 점검 권장 framing, abstention, 사람 검토, UX review | 금지 문구 노출 | watch |
| RSK-19 | LLM 환각·수치 변조·오염 입력 | high/high | Fact ID 없음, 수치 mismatch, injection 성공 | structured schema, fact allowlist, validator, deterministic fallback | unsupported 핵심 claim 노출 | watch |
| RSK-20 | agent/ML 장애가 핵심 기록을 차단 | high/medium | model/LLM timeout 시 수집 실패 | async 분리, circuit breaker, rules report fallback | graceful degradation 실패 | watch |
| RSK-21 | 고령·장애 사용자 접근성 부족 | high/high | permission/QR/알림 과업 실패 | 큰 글씨, screen reader, 터치 영역, 최소 입력, 보호자/수리사 보조 | 핵심 과업 접근성 실패 | watch |
| RSK-22 | 복지관별 양식 차이로 코어 모델 오염 | medium/high | 기관 필드가 core schema로 증가 | canonical model + versioned adapter, mapping test | 기관별 분기 직접 삽입 | watch |
| RSK-23 | 기술 범위 과다로 end-to-end 미완성 | high/medium | 컴포넌트 수 증가, vertical slice 단절 | monthly gate, kill criteria, thin slice, fallback output | 필수 신뢰 경계보다 장식 기능 우선 | active |
| RSK-24 | 계획 보고가 실제 성과처럼 오해됨 | high/medium | 증거 없는 완료 문장, 참석 정보 자동 생성 | fixed plan/actual 분리, EVD link, human review | 근거 없는 공식 제출 | active |
| RSK-25 | 공개 발표에서 개인정보·기관정보 노출 | critical/medium | screenshot/영상/지도 scan 검출 | synthetic demo, masking, artifact review, access controls | 공개 artifact privacy review 실패 | watch |
| RSK-26 | 복지관과 수리소 간 권한을 tenant membership으로 잘못 단순화 | critical/medium | 수리사를 타 기관 member로 중복 등록하거나 QR만으로 상세 이력 접근 | server-only dataAccessGrant, purpose·대상·만료·철회·audit, backend command only | active grant 없는 cross-org 조회·write 성공 | active |
| RSK-27 | active membership만으로 tenant 내 타인의 민감 domain projection을 client가 직접 조회 | critical/high | beneficiary가 다른 사람의 trip·동의·배정·alert read 성공 | tenant active, owner/role·tenant/person filter matrix와 Emulator 24-case 적용; 복잡한 목적은 backend DTO | staging Rules 배포·실앱 query 검증 전 | active |

## 3. 즉시 관리할 상위 위험

### A. 수집 신뢰 경계

RSK-05~10은 production ingest 이전 차단 위험이다. verifier helper나 interface 존재만으로 완화되지 않는다. 실제 server wiring, membership/consent authorization, transaction, Storage precondition, fail-closed startup을 staging에서 함께 검증해야 한다.

### B. 실기기 수집

RSK-03·04·13은 WSL 정적 빌드로 닫을 수 없다. Android와 iPhone에서 권한·background·강제종료·offline을 각각 실행하고, 기기·OS·build를 기록한다.

### C. 데이터·모델 과장

RSK-14~19는 실제 export와 field data를 받기 전에 숫자로 닫지 않는다. synthetic 파이프라인 결과, 실제 데이터 적합성, field 성능을 세 칸으로 분리한다.

### D. 보고·공개

RSK-24·25는 기술적으로 좋은 결과가 있어도 공식 제출과 발표에서 발생할 수 있다. fixed report와 실제 증거, 회의 증빙, 공개 가능 artifact를 별도 검토한다.

## 4. 위험 갱신 형식

정기 검토 시 변경된 위험만 다음 형식으로 기록한다.

```text
risk_id
reviewed_at
previous_status → new_status
new_signal_or_evidence
decision
next_control / due_gate
related ADR/EVD/INC
```

가능성이나 영향이 낮아졌다면 테스트·배포·사람 확인 근거를 연결한다. 단순히 시간이 지났다는 이유로 `mitigated`로 바꾸지 않는다.

## 5. escalation

- `critical` 위험이 현실화하거나 다른 tenant·위치·동의 경계가 깨지면 관련 write/배포를 중단하고 Incident를 연다.
- `high` 위험의 차단 조건이 해소되지 않으면 다음 프로토타입은 진행할 수 있어도 staging/pilot 승격은 보류한다.
- 외부 권한·상표·개인정보 동의가 필요한 결정은 기술 가정으로 대체하지 않고 사람 승인을 기다린다.
- 어려움·시간 부족만으로 위험을 닫지 않는다. 범위를 줄이고 미완료를 공개한다.
