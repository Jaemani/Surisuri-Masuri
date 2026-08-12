# ADR-0046 — 합성 PyTorch 후보 후 ONNX 배포를 유보한다

- 상태: accepted
- 결정일: 2026-08-13
- 영향 범위: R07-C 학습, R08 ONNX, 모바일 데이터 품질 판별

## 맥락

R07 synthetic dataset은 네 class가 의도적으로 분리되어 rules baseline의 test macro-F1이 1.0이다. 같은 데이터에서 PyTorch 모델을 학습하면 학습 harness는 검증할 수 있지만 규칙보다 나은 일반화나 현장 효용을 증명할 수 없다.

## 결정

- frozen manifest와 group/time split을 그대로 사용하는 CPU PyTorch 최소 후보를 구현한다.
- 좌표 대신 검증된 13개 feature를 사용하고 train split 통계만 normalization에 사용한다.
- seed·deterministic algorithm·parameter count·state hash를 기록한다.
- synthetic 결과와 무관하게 deployment decision은 `defer`로 고정한다.
- ONNX·양자화·모바일 inference는 실제 동의 field holdout 또는 rules 오류 개선을 평가할 수 있을 때만 재개한다.

## 결과

직접 PyTorch 학습·평가·재현성 경험과 code evidence는 남지만 이를 제품 AI 성능으로 과장하지 않는다. 현재 운영 fallback은 해석 가능한 rules와 review/abstain이다.

