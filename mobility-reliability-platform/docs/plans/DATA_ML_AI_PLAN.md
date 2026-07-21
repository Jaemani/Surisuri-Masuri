# 데이터·ML·AI 실행계획

## 1. 목적과 경계

이 계획은 모바일 위치, 수리·점검 이력, 모델 예측, LLM 설명이 어떤 순서와 책임으로 연결되는지 정의한다. 목표는 AI 기능의 수를 늘리는 것이 아니라 다음을 증명하는 것이다.

- 데이터 출처와 변환을 재현할 수 있다.
- 규칙 baseline과 직접 학습한 모델을 공정하게 비교한다.
- 불확실하거나 데이터가 부족하면 판단을 유보한다.
- LLM이 통계·위험도를 다시 계산하지 않고 검증된 fact만 설명한다.
- 사용자·수리사·복지관에게 같은 값을 다른 깊이로 전달하되 근거는 하나로 유지한다.

## 2. 데이터 종류와 허용 목적

| 데이터 | 출처 | 허용 목적 | 금지 |
| --- | --- | --- | --- |
| synthetic GPS/event | versioned generator | 계약·부하·복구·UI·초기 모델 파이프라인 | field 성능·사용자 행동으로 주장 |
| developer device GPS | 명시적 자체 테스트 | 실기기 lifecycle·accuracy·배터리 | 공개 Git, 기관 사용자 결과로 일반화 |
| legacy repair export | 승인된 과거 DB export | 이관·수리 범주·신뢰성 후보 분석 | 원본 권한 확인 전 사용, 과거 LLM score를 label로 사용 |
| pilot GPS/repair | 동의된 신규 사용자 | field 품질·운영·제한적 모델 평가 | 동의 범위 밖 2차 사용, 정밀 경로 공개 |
| public data | 출처·license가 고정된 자료 | 맥락·시설·지역 단위 보조 분석 | 개인 수준 사실처럼 결합 |
| model prediction | versioned inference | 검토 신호·fact 생성 | 원본 사실 수정, 고장 확정 |
| human review | 수리사·담당자 review event | label 개선·override 근거 | 과거 model output 덮어쓰기 |

## 3. 데이터 계보

```text
Firebase principal headers ──authorize only──┐
                                            ▼
mobile GPS event ─> telemetry-batch.v2 ─> raw object + receipt
                                            └─> quality/masking ─> trip summary/features

domain command/import ─> repair/inspection/part/consent events
                                            │
trip summary/features + domain events ──────┴─> device/part twin
                                                  ├─> quality/reliability datasets
                                                  └─> deterministic facts
                                                        └─> report run/claims/human review
```

Firebase UID와 App ID는 header에서 검증된 principal이며 raw telemetry JSON·object에 합쳐 저장하지 않는다. 각 데이터 변환 화살표는 input hash, schema/processor version, output manifest, reject/quarantine count를 가져야 한다.

## 4. 단계별 데이터 계획

### Stage A — 계약과 합성 fixture

- telemetry/domain event JSON Schema와 valid/invalid fixture를 고정한다.
- UUID, sample sequence, time ordering, coordinate range, duplicate key를 검사한다.
- synthetic generator는 seed·route class·noise profile·device profile을 manifest에 기록한다.
- 실제 사용자처럼 보이는 이름·전화·주소를 fixture에 넣지 않는다.

**완료 조건:** TypeScript와 Go에서 같은 fixture와 idempotency vector가 동일 결과를 낸다.

### Stage B — 모바일 자체 데이터

- 사용자 명시적 trip, installation, session, consent revision을 분리한다.
- raw coordinate는 로컬 encrypted-at-rest 가능성과 보존 기간을 플랫폼별로 검토한다.
- 업로드 승인 전 `development_local_only` 데이터를 production으로 보내지 않는다.
- permission·accuracy·motion·sampling profile을 coordinate와 분리된 metadata로 기록한다.

**완료 조건:** 실제 Android/iPhone 결과가 기기·OS·권한·build 정보와 연결된다.

### Stage C — 레거시 수리 이관

- source snapshot과 SHA-256을 고정하고 read-only dry-run한다.
- Mongo ObjectId, Firestore document ID, Firebase UID, public vehicle ID를 source namespace와 함께 crosswalk한다.
- 누락값을 0·false·현재 시각으로 보정하지 않는다.
- 수리 범주, 금액, 날짜, 기기 참조 오류는 accepted/quarantine/rejected로 reconciliation한다.
- 실제 export가 없으면 mapping 설계까지만 완료로 표시한다.

**완료 조건:** [Migration Gates](../data/MIGRATION_GATES.md)의 count·referential·PII·Rules·rollback gate를 통과한다.

### Stage D — pilot 데이터

- consent purpose/version/validity를 trip과 batch가 참조한다.
- 출발·도착 민감 위치와 원본 보존 기간을 별도 정책으로 적용한다.
- dataset snapshot은 삭제 요청과 추적 가능한 lineage를 유지한다.
- 실제 사용자 수·기간·표본은 관측값만 보고한다.

**완료 조건:** 삭제·철회·접근·export·audit 시나리오가 staging과 pilot runbook에서 검증된다.

## 5. 모델 1 — 주행 데이터 품질·이동 유형 보조 판별

### 질문

수집된 window가 전동보장구 주행으로 분석 가능한지, 차량 이동·장기 정지·GPS 노이즈 가능성이 높은지 보조 신호를 만들 수 있는가?

### 입력 후보

- speed·acceleration·jerk 분포
- stop ratio와 연속 정지 길이
- heading change와 displacement/path ratio
- OS accuracy와 sample interval·missingness
- motion activity 또는 플랫폼에서 허용된 센서 요약
- 기기·OS·sampling profile metadata

원본 좌표 자체보다 이동 특성과 품질 요약을 우선하고, 불필요한 민감 feature를 제외한다.

### 라벨

- `mobility_aid_likely`
- `vehicle_likely`
- `stationary`
- `gps_noise_or_insufficient`
- `unknown_review_required`

라벨 지침, 검토자, confidence, disagreement를 보존한다. model 예측을 사람 label로 자동 승격하지 않는다.

### 비교

1. 규칙 기반 threshold
2. logistic/tree baseline
3. PyTorch 1D CNN 또는 GRU 최소 후보
4. 필요할 때만 더 복잡한 sequence model

### 배포

- 선택 모델을 ONNX로 export한다.
- Python/TypeScript/native feature parity와 float/quantized output parity를 검사한다.
- 모바일 모델 실패 시 raw 수집을 중단하지 않고 rules/unknown으로 fallback한다.

### 배포 차단

- time/device split에서 baseline 이득이 불명확함
- 중요한 class의 recall/calibration이 허용 범위를 충족하지 못함
- Android/iPhone feature parity 불일치
- 기종·sampling profile 변화에서 성능 급락
- 모델 출력이 사용자 안전 보증처럼 보이는 UX

## 6. 모델 2 — 부품별 time-to-inspection

### 질문

수리·교체 후 경과시간과 누적 주행량을 이용해 향후 일정 기간의 “점검 필요 위험”을 고정주기보다 잘 보조할 수 있는가?

### outcome 정의 후보

- 실제 고장 수리
- 예방 교체
- 점검에서 확인된 이상
- 상담·행정성 기록

위 유형을 하나로 합치지 않는다. 부품별 사건 수가 부족하면 해당 outcome을 모델링하지 않는다.

### 시간축

- 관측 시작과 종료
- right censoring
- 부품 교체 후 risk clock reset
- 소유자 변경·사용 중단·주행량 결측
- 동일 기기의 반복 event와 cluster

### 비교

1. 고정 6개월 등 운영 규칙
2. 누적 주행거리 threshold
3. Kaplan–Meier/Weibull/Cox baseline
4. Random Survival Forest 또는 boosting 후보

### 출력

- 향후 30일 등 정해진 horizon의 점검 필요 위험
- 권장 점검 구간
- 주요 입력 fact와 데이터 기준일
- confidence/calibration band
- `data_insufficient`, `out_of_distribution`, `review_required`

### 배포 차단

- outcome·censoring 규칙을 재현할 수 없음
- 부품별 사건 수·기관/기종 편향이 공개 기준보다 부족함
- baseline 대비 calibration 또는 의사결정 가치가 없음
- 정밀 주행 결측이 결과를 과도하게 바꿈
- “고장 예측” 또는 안전 보증으로 해석되는 표현

## 7. Active Learning과 사람 검토

- 낮은 confidence, 모델 간 불일치, 새 기종·새 수리 범주를 review queue로 보낸다.
- 수리사의 판정은 `ReviewSubmitted` event로 추가하고 원 예측과 threshold를 보존한다.
- review reason, reviewer role, timestamp, referenced fact/prediction을 기록한다.
- label 변경이 모델 재학습에 반영될 때 dataset version과 inclusion 기준을 남긴다.
- 동일 사람이 같은 사례를 반복 검토해 생기는 leakage를 분할에서 통제한다.

## 8. MLOps 최소 구성

| artifact | 필수 내용 |
| --- | --- |
| dataset manifest | input object/hash, query/export, 기간, 필터, split, label version |
| feature manifest | feature schema, code commit, missing/default 정책 |
| experiment | config, seed, environment, metrics, artifact hash |
| model card | 목적, 비목표, 데이터, 성능, calibration, 편향, 제한 |
| model package | ONNX/checkpoint hash, opset, quantization, threshold |
| deployment receipt | app/service version, model version, rollout/rollback |
| monitoring | input drift, missingness, abstention, calibration proxy, 오류 |

MLflow 등 registry는 이 계보·비교·승격을 실제로 단순화할 때 도입한다. 도구 채택 자체를 성과로 세지 않는다.

## 9. Fact Store

Fact는 LLM 이전에 결정론적으로 계산된 최소 근거 단위다.

```text
fact_id
tenant_id
subject_type / subject_id
fact_type
value / unit
effective_period
source_refs
calculation_version
model_version? / confidence? / abstention_reason?
created_at / expires_at
sensitivity
```

예측 fact와 관측 fact를 같은 type으로 숨기지 않는다. 원본 좌표를 report fact로 노출하지 않고 필요한 거리·기간·지역 집계만 생성한다.

## 10. 근거형 보고서 에이전트

### 실행 상태

```text
select audience/schema
  → assemble authorized fact bundle
  → plan allowed claim types
  → write structured report
  → validate schema/fact/numeric/date/forbidden claims
  → retry with classified failure or deterministic fallback
  → human review when required
  → immutable run receipt
```

### 대상별 출력

- 사용자: 현재 상태, 이해 가능한 점검 이유, 다음 행동, 불확실성
- 수리사: 부품·수리·주행 fact, 모델 version/confidence, 검토·수정 UI
- 복지관: 운영 예외, 기기·점검 현황, 익명 집계, 기관 양식 근거
- 정책 자료: 비식별 집계, 데이터 품질, 기간·모수·제한

### agent가 하지 않는 일

- 위험도·통계 재계산
- raw GPS·PII 검색
- 권한·동의 결정
- 수리 기록·예측 fact 직접 수정
- 근거 없이 서비스·지원 자격 확정
- 사람 검토 없이 안전 관련 확정 문구 발행

## 11. 평가와 ablation

최종 포트폴리오에는 최소 다음 비교를 남긴다.

- 규칙 품질 판별 vs PyTorch 모델
- GPS feature만 vs motion/quality feature 추가
- float vs quantized ONNX
- 고정주기 vs 거리 규칙 vs survival model
- calibration 전후와 abstention 적용 전후
- 일반 LLM 생성 vs Fact-restricted + validator
- validator component별 ablation: schema, numeric, temporal, evidence

결과가 나쁜 ablation도 숨기지 않고 최종 설계가 왜 필요한지 설명하는 증거로 사용한다.

## 12. 월별 산출물

| 월 | 데이터·ML·AI 산출물 |
| --- | --- |
| 5 | 데이터 분류, lifecycle, 최소 event/schema |
| 6 | 실기기 trace manifest, offline batch identity |
| 7 | immutable raw/receipt lineage, privacy/quality flags |
| 8 | label guide, baseline, PyTorch experiment, ONNX/model card |
| 9 | event replay, importer manifest, survival dataset/model card |
| 10 | calibration/abstention, Fact Store, agent eval/receipt |
| 11 | field monitoring, review queue, drift·incident evidence |
| 12 | frozen time-split evaluation, cards, reproducible package |
