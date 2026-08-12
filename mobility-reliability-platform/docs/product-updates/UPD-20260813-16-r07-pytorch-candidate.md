# UPD-20260813-16 — R07-C PyTorch 재현 후보

- 기준일: 2026-08-13
- 상태: 구현·local CPU 검증 완료 / deployment 유보
- 데이터: deterministic synthetic 48 trace only

## 구현

- WSL에 pipx 격리 방식으로 uv 설치
- Python 3.12 및 `pytorch-cpu` explicit index lock
- 13개 coordinate-free feature, 16 hidden unit, 4 class, 총 292 parameter MLP
- train 16 / validation 16 / test 16 frozen group-time split
- seed `20260813`, deterministic algorithms, single CPU thread
- model state SHA-256와 prediction lineage 기록
- rules baseline과 동일 dataset hash·split에서 test macro-F1 delta 산출

## 판단

합성 generator의 class가 rules로 완전히 분리되어 rules test macro-F1이 이미 1.0이다. 후보가 이 데이터에 맞더라도 field 일반화나 rules 대비 개선을 증명하지 못한다. 따라서 ONNX export와 모바일 탑재를 유보한다.

## 검증 경계

Python test는 deterministic byte-equal result, split count, parameter count, state hash, coordinate-free prediction, deployment defer를 확인한다. 실제 GPS, 실기기, 현장 사용자, 복지관 데이터, 모델 calibration은 사용하지 않았다.

