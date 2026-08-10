# Shared contracts

모바일, 게이트웨이, 데이터 처리 작업이 같은 의미를 공유하도록 버전이 고정된 wire contract를 보관합니다.

## 규칙

- 배포된 schema는 같은 버전 안에서 의미를 바꾸지 않습니다.
- 필드 제거·타입 변경·enum 축소는 새 major contract를 만듭니다.
- database row나 ORM model을 wire contract로 직접 노출하지 않습니다.
- tenant는 payload가 아니라 인증 membership으로 검증합니다.
- 날짜는 UTC ISO 8601, 거리는 meter, 속도는 meter/second를 사용합니다.
- 위치 sample에는 이름, 전화번호, 장애정보를 포함하지 않습니다.

## 초기 계약

- `telemetry-batch.v2.schema.json`: 신규 모바일에서 게이트웨이로 보내는 GPS batch. 실제 동의 revision과 installation·trip을 참조하며 현재 ingest 대상이다.
- `telemetry-batch.v1.schema.json`: 초기 설계 compatibility 기록. 사용자별 동의 revision을 식별하지 못하므로 production ingest 대상이 아니다.
- `domain-event.v1.schema.json`: 검증 이후 내부 event log에 기록하는 공통 envelope
- `quality-label.v1.schema.json`: 텔레메트리 trace의 이동 유형·품질 검토 라벨. `unknown_review_required`는 학습 class가 아니라 `review_required`/`abstained` 상태로 표현한다.
- `quality-dataset-manifest.v1.schema.json`: 합성·개발기기·현장 trace의 계보, hash, seed, group/time split 및 benchmark eligibility를 기록하는 ML dataset manifest. `developer_device` trace는 기록할 수 있지만 benchmark loader에서 제외한다.

R07 합성 데이터셋은 각 trace를 `telemetry-batch.v2`로 별도 검증하고, manifest의 `telemetryBatchId`와 `telemetrySha256`로 연결한다. manifest schema는 split 간 `scenarioGroupId` leakage나 집계값 일치를 대신 검증하지 않으며, 이는 dataset builder의 결정론적 검증 단계에서 실패시켜야 한다.
