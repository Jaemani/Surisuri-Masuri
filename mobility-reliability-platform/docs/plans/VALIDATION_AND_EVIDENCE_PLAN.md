# 검증·증거 계획

## 1. 목적

이 계획은 “작동한다”, “안전하다”, “개선됐다”는 주장을 어떤 환경·측정·증거로 인정할지 정의한다. 테스트 개수만 늘리는 것이 아니라 모바일→수집→저장→projection→모델→보고서의 실패 경계를 재현하고, 실제보다 높은 증거 수준을 주장하지 않는 것이 목적이다.

## 2. 증거 수준

| 수준 | 이름 | 의미 | 허용 주장 |
| --- | --- | --- | --- |
| L0 | design | 문서·스키마·mockup만 존재 | 설계됨, 결정됨 |
| L1 | local-synthetic | 개발 환경과 합성 fixture에서 실행 | 로컬 합성 입력에서 통과 |
| L2 | clean-runner | 고정 Docker/CI 새 환경에서 재실행 | 클린 환경에서 재현됨 |
| L3 | device | 실제 Android/iPhone에서 측정 | 기록한 기기·OS·조건에서 동작 |
| L4 | staging | 실제 Firebase/GCP 비운영 프로젝트에서 통합 | staging 구성에서 end-to-end 통과 |
| L5 | field | 적법한 실제 사용자·현장 조건에서 확인 | 명시한 표본·기간·버전에서 관측 |

L1의 synthetic 부하 테스트를 field 성능으로, static export를 background GPS 실기기 증거로, unit-tested helper를 활성화된 production guard로 표현하지 않는다.

## 3. 공통 실험 manifest

모든 중요한 측정은 다음 정보를 가진다.

```text
evidence_id
claim
evidence_level
started_at / ended_at / timezone
git_commit / dirty_state
app_build / service_image_digest
device_model / os_version / permission_state
firebase_project_class: demo | staging | production
dataset_manifest / synthetic_or_field
commands_or_protocol
sample_size / exclusions
metrics / units
result: pass | fail | inconclusive
limitations
reviewer / reviewed_at
```

원본 좌표, 실제 이용자 이름·전화번호, token, service account key는 manifest나 Git에 넣지 않는다.

## 4. 시스템 KPI와 통과 조건

### 4.1 모바일 수집

| 지표 | 계산 | 초기 통과 조건 | 증거 수준 |
| --- | --- | --- | --- |
| local persistence | 생성 event 대비 재시작 후 존재 event | scripted recovery에서 설명 없는 손실 0 | L3 |
| queue recovery | offline 생성 batch 대비 ack/보류/reject 합 | 모든 batch가 한 상태로 reconciliation | L3/L4 |
| logical duplication | 같은 client event가 projection에 반영된 수 | 반복 전송 시 중복 0 | L2/L4 |
| sample cadence | 연속 sample 간 시간 분포 | profile별 p50/p95와 결측 구간 보고 | L3 |
| GPS accuracy | OS accuracy meter 분포 | profile·환경별 분포와 exclusion 공개 | L3/L5 |
| battery impact | 동일 기기·기간의 baseline 대비 소모 | 숫자 승인 전 측정 프로토콜과 편차 보고 | L3 |

배터리·정확도는 초기부터 임의의 절대 합격 숫자를 고정하지 않는다. 2개 이상 sampling profile을 동일 조건에서 비교한 뒤 제품 허용치를 ADR로 확정한다.

### 4.2 인증·권한·수집

| 지표 | 통과 조건 | 필수 실패 시나리오 |
| --- | --- | --- |
| token enforcement | ID token과 App Check 중 하나라도 없거나 invalid면 write 0 | missing, duplicate, combined, expired/invalid, unlisted app |
| scope authorization | inactive membership, 미배정 기기, 타 tenant, 무효 trip/동의는 write 0 | cross-tenant, revoked/expired, device mismatch, consent withdrawn |
| idempotency | 같은 key/body는 같은 receipt, 같은 key/다른 body는 409 | concurrent retry, app restart, reordered batch |
| lease/fencing | active owner 1명, takeover 뒤 stale token mutation 0 | concurrent claim, exact expiry, renew-vs-takeover, stale stored/rejected finalizer |
| receipt/object lineage | hash, count, batch ID, path, generation 불일치 0 | object write 후 receipt 실패, receipt reserve 후 timeout |
| receipt recovery | valid raw-only/complete만 forward 완료, 추정 복구·latest fallback 0 | no artifact, manifest-only, 동일/상이 bytes의 복수 manifest generation, metadata drift, stored-missing, consent withdrawal, unknown validator |
| expiry cleanup | cleanup transition/claim winner 1, origin별 replay 의미 보존, 완료 후 live generation 0, terminal evidence 전 receipt/index purge 0 | recovery claim-vs-begin-cleanup, stale finalizer, accepted deletion replay, rejected 무승인 cleanup, late Storage create, concurrent cleanup, raw-delete crash, manifest-delete crash, pre/post-expiry hold, soft-delete |
| receipt purge | nested attempt·cleanup target·integrity finding orphan 0, 세 집합 empty 증거 뒤 마지막 linkage transaction 1 | transaction limit 초과 attempt, page crash/restart, concurrent target/finding create, parent-first delete |
| conservative time | consent/deadline acceptance는 max clock, 조기 cleanup 방지는 min clock, 허용 skew 초과 mutation 0 | app clock ±offset, exact deadline/expiry, read-time reversal |
| Firestore write shape | GPS sample별 document write 0 | 최대 sample batch, repeated upload |
| privacy logging | token·PII·정밀좌표 검출 0 | validation error, SDK error, crash, trace export |

### 4.3 공간·삭제

- raw→filtered→masked 거리와 점 수 변화를 계산한다.
- 마스킹이 시작·도착 민감 위치를 숨기는지와 실제 이동거리 왜곡을 함께 측정한다.
- 익명 집계는 최소 그룹 기준 미달 셀을 숨긴다.
- 삭제 요청은 Firestore metadata, Storage object, 활성화된 파생 dataset까지 lineage로 추적한다.
- 삭제 완료는 “검색되지 않음”만이 아니라 object generation/receipt/deletion job 결과로 검증한다.

### 4.4 projection·복구

- 동일 event set을 순서·batch가 달라도 canonical sort 규칙으로 replay한다.
- replay 전후 device/part/trip projection checksum을 비교한다.
- projector version·checkpoint·replay run ID를 보존한다.
- replay 중 FCM·SMS·외부 API side effect가 0인지 검사한다.
- DLQ의 input count = reprocessed + terminal rejected + pending을 만족한다.

### 4.5 ML

모든 모델 성능표는 다음을 함께 제공한다.

- dataset 기간, 대상, manifest hash, synthetic/legacy/field 구분
- train/validation/test의 시간·기기 분리
- 규칙·고정주기 baseline
- class/outcome별 표본 수와 confidence interval
- discrimination, calibration, abstention coverage
- 결측·기종·기관·사용량 구간별 실패 분석
- feature/model/threshold version과 재현 명령

정확도 하나만으로 배포하지 않는다. baseline 대비 이득, calibration, 실패 비용, 모바일 latency, 데이터 부족 시 유보를 함께 판단한다.

### 4.6 AI 보고서

| 평가 | 측정 방식 | 차단 조건 |
| --- | --- | --- |
| schema validity | JSON Schema 통과율 | 필수 필드가 invalid인데 사용자에게 노출 |
| fact coverage | 주요 claim 중 valid Fact ID 비율 | 근거 없는 위험·점검 권고 |
| numeric fidelity | 출력 수치와 fact 값 비교 | 단위·기간·값 변조 미탐지 |
| temporal validity | report 기준일과 fact 유효기간 | 오래된 fact를 현재 상태로 단정 |
| forbidden claims | 안전 보증·고장 확정·의료 판단 검사 | 금지 주장 노출 |
| fallback | LLM timeout/invalid/근거 부족 | 최소 사실 보고서조차 생성 실패 |

평가셋에는 정상 입력뿐 아니라 prompt injection, 모순 fact, 누락 field, 오래된 fact, 비정상 수치, timeout을 포함한다.

## 5. 실기기 시나리오 매트릭스

| 시나리오 | Android | iPhone | 기록할 항목 |
| --- | --- | --- | --- |
| foreground 수집 | 필수 | 필수 | 권한, cadence, accuracy, queue |
| 화면 잠금/background | 필수 | 필수 | OS/build/background mode |
| recent apps 제거 | OEM별 기록 | 플랫폼 한계 기록 | event gap, 복구 여부 |
| 프로세스 강제종료 | 필수 | 필수 | OS 허용 범위, 열린 trip 처리 |
| 기기 재부팅 | 필수 | 가능 범위 | 자동 재개 여부를 과장하지 않음 |
| 네트워크 단절/재연결 | 필수 | 필수 | batch identity, retry, ack |
| 권한 철회/정밀 위치 off | 필수 | 필수 | UX, event state, 원본 저장 중단 |
| 큰 글씨/스크린리더 | 필수 | 필수 | 과업 성공, focus, 터치 영역 |

WSL에서는 iOS native build를 로컬 증명할 수 없다. EAS development build의 build ID와 실제 iPhone 결과를 기록한다. Android는 Windows ADB/USB reverse 또는 명시한 네트워크 경로를 사용한다.

## 6. 테스트 계층

1. contract: JSON schema fixture, cross-language idempotency vector, migration compatibility
2. unit/property/fuzz: parser, state machine, feature, projector, validator
3. rules/emulator: tenant·role·field allow/deny, server-only collection, Storage deny
4. integration: Firebase token/App Check debug provider, Firestore transaction, Storage precondition
5. container/clean runner: Go race/vet, image build, non-root, health/readiness/fail-closed
6. device: GPS lifecycle, offline, battery, accessibility, ONNX latency
7. staging: end-to-end, deletion, restore, cost, trace
8. field: 동의된 제한 범위, support, 실제 사용성·운영 지표

## 7. 증거 저장과 연결

- text 중심 결과와 안전한 요약은 `docs/evidence/YYYY-MM.md`에 EVD ID로 기록한다.
- 큰 영상·원본 측정 artifact는 승인된 외부 저장소에 두고 hash와 접근 범위만 기록한다.
- 실제 위치 지도는 민감 지점을 제거하고 공개 가능 여부를 검토한다.
- Evidence 항목은 관련 ADR, Product Update, Human Report, Incident로 역링크한다.
- clean runner 재실행 전은 `generated`, 사람 검토와 조건 확인 후에만 `verified`로 바꾼다.

## 8. 월말 검증 패키지

각 월은 최소 다음을 남긴다.

- 실행 가능한 코드 또는 명시적인 설계 artifact
- 성공 테스트 하나와 실패/제약 테스트 하나
- 화면·그래프·지도·상태도 중 하나
- `무엇을 아직 증명하지 않았는가` 목록
- 다음 게이트로 넘어가도 되는지에 대한 pass/conditional/fail 결정

## 9. 최종 증거 매트릭스

12월에는 최종 발표의 각 주장마다 다음 열을 채운다.

```text
claim_id | audience | claim | evidence_id | level | version | limitation | approved
```

EVD가 없거나 evidence level이 주장보다 낮으면 발표 문구를 낮추거나 제거한다.
