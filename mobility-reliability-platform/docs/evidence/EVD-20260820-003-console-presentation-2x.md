# EVD-20260820-003 — 복지관 콘솔 발표용 2x 화면

- 생성: 2026-08-20 KST / Codex
- 환경·데이터: local Chromium / synthetic demo
- 상태: verified
- 화면 기준: CSS viewport `1440×1024`, `deviceScaleFactor: 2`
- 출력 규격: 무손실 RGB PNG `2880×2048`
- 재현 명령: `pnpm run capture:console`
- 캡처 조건: `document.fonts.ready` 완료 후 2회의 animation frame을 기다리고 촬영

## 노션·발표용 파일

| 화면 | 파일 | SHA-256 |
| --- | --- | --- |
| 운영 개요 | [01-console-overview-2x.png](./assets/EVD-20260820-003/01-console-overview-2x.png) | `2bc97ae9a11dbe519c39a4fb5e0515aec2f0668ddf47261e25762bcb17e6dc44` |
| 센터 검증·지원금 집행 | [02-console-authority-review-2x.png](./assets/EVD-20260820-003/02-console-authority-review-2x.png) | `3837cc4ad431afdbda951f898e18cd170c3231e076e5859259d3c13e0ec867d3` |
| 예방점검 근거 검토 | [03-console-inspection-evidence-2x.png](./assets/EVD-20260820-003/03-console-inspection-evidence-2x.png) | `f06c280a7985c3293049d1bd7356465b95fc6050d54db1fe289121ef439d2889` |
| 신뢰성 기준선 비교 | [04-console-baseline-comparison-2x.png](./assets/EVD-20260820-003/04-console-baseline-comparison-2x.png) | `48c4ff0426c21d85c4fc6892c2ebaaefba676353aa421760bc4268b1af94d35b` |
| 근거 연결 보고서 | [05-console-grounded-report-2x.png](./assets/EVD-20260820-003/05-console-grounded-report-2x.png) | `b9302794223a53d27063c3a28fc072173dfc72046b7d4de88a528e9ea1d98126` |

## 검증 범위와 한계

- 다섯 파일 모두 `2880×2048`인지 확인하고 원본 해상도로 육안 검토했다.
- `수리수리마수리` 승인 브랜드와 합성 데이터 경계가 표시된다.
- 회귀 테스트용 1x golden image와 발표용 2x 파일을 분리했다.
- 이 증거는 브라우저에서 렌더링한 복지관 콘솔 화면이다. 실제 배포, 현장 데이터 또는 모바일 native 동작을 증명하지 않는다.
