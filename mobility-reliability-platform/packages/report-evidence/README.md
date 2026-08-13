# Report evidence

R12의 계산 결과→Fact→주장 경계를 검증하는 dependency-free Node package다. 현재는 `reliability-calibration-assessment.v1` 합성 aggregate를 입력으로 받아 다섯 개의 목적 제한 Fact와 복지관 운영자용 결정론적 fallback 보고서를 만든다.

- 모든 grounded claim은 정확히 하나의 존재하는 Fact ID를 가져야 한다.
- claim text는 fact type별 결정론적 template과 정확히 일치해야 한다.
- fact와 claim은 source assessment SHA-256에 묶인다.
- 사람·기관·기기 ID, raw 좌표·경로, 수리 자유문 key는 중첩 위치에서도 거부한다.
- 현재 writer는 LLM이 아니라 `deterministic_template.v1`이며 receipt의 `fallbackUsed=true`를 고정한다.
- 실제 기관 보고서, 현장 사실, LLM groundedness, 사람 승인, production 발행을 증명하지 않는다.

```bash
rtk pnpm --filter @mobility-reliability/report-evidence check
rtk pnpm --filter @mobility-reliability/report-evidence test
```
