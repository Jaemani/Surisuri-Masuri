# ADR-0006: 신뢰성 모델과 LLM의 책임 분리

- 상태: accepted
- 결정일: 2026-07-21

## 맥락

생성형 AI가 원시 GPS와 수리 데이터를 직접 해석해 고장을 예언하도록 만들면 재현성, calibration, 근거 검증이 어렵다.

## 결정

```text
validated events
  -> deterministic features
  -> versioned reliability model
  -> calibrated risk + abstention
  -> fact store
  -> LLM explanation
  -> claim-evidence validator
  -> human review
```

- 위험도는 버전이 고정된 통계·ML 모델만 계산한다.
- 초기에는 고정 점검주기와 규칙 기반 위험도를 baseline으로 유지한다.
- 데이터가 부족하면 모델은 추측하지 않고 abstain한다.
- LLM은 허용된 fact ID를 사용해 대상별 설명을 생성한다.
- 수리사 판단은 별도 event로 추가하며 기존 예측을 덮어쓰지 않는다.

## 결과

- 모델 평가와 문장 품질 평가를 분리할 수 있다.
- 보고서가 자연스러워도 근거가 없으면 실패로 처리한다.
- 성능뿐 아니라 calibration, 근거 지원 precision, 사람 수정률을 보고한다.
