# Report evidence

R12의 계산 결과→Fact→주장 경계를 검증하는 dependency-free Node package다. 현재는 `reliability-calibration-assessment.v1` 합성 aggregate를 입력으로 받아 다섯 개의 목적 제한 Fact와 복지관 운영자용 결정론적 fallback 보고서를 만든다.

- 모든 grounded claim은 정확히 하나의 존재하는 Fact ID를 가져야 한다.
- 세 component readiness, fallback, synthetic boundary의 정확한 5-Fact profile과 1:1 claim coverage를 강제하며 ID는 source/type에서 결정론적으로 파생한다.
- claim text는 fact type별 결정론적 template과 정확히 일치해야 한다.
- fact와 claim은 source assessment SHA-256에 묶인다.
- source assessment 자체도 Python evaluator와 같은 canonical JSON 규칙으로 self-hash를 재계산하고, R11의 목적·분할·판단 유보 계약을 다시 검사한다.
- 사람·기관·기기 ID, raw 좌표·경로, 수리 자유문 key는 중첩 위치에서도 거부한다.
- 현재 writer는 LLM이 아니라 `deterministic_template.v1`이며 receipt의 `fallbackUsed=true`를 고정한다.
- report run은 `pending → validated | failed`, `validated → completed | fallback | failed`만 허용한다. 현재 결정론적 결과는 `fallback / 사람 검토 대기 / 미발행`이다.
- 실제 기관 보고서, 현장 사실, LLM groundedness, 사람 승인, production 발행을 증명하지 않는다.

```bash
rtk pnpm --filter @mobility-reliability/report-evidence check
rtk pnpm --filter @mobility-reliability/report-evidence test
```
