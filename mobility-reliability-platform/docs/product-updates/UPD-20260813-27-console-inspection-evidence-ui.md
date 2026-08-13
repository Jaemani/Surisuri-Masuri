# UPD-20260813-27 — 복지관 예방점검 근거·유보 UI

- 기준일: 2026-08-13
- 상태: implemented / local synthetic web visual 검증
- 로드맵 위치: R10 Device Timeline & Reliability

## 제품 변화

예방점검 화면의 합성 위험 표를 근거 중심 검토 workspace로 교체했다. 상단은 운영 검토·판단 유보·등록 일정 건수를 출처와 함께 보여주고, 목록 선택 시 현재 사실·부족한 정보·다음 운영 조치를 상세 패널에서 분리한다.

`판단 유보` 항목에는 score/confidence를 만들지 않으며, 이용자 진술과 센터 검증·운영 일정을 서로 다른 근거로 표시한다. 개별 기기 고장 확률이나 R10 합성 aggregate metric은 노출하지 않는다.

## 검증

- console typecheck/test/build 통과
- console Playwright 4 flows 통과
- 신규 `console-inspection-evidence` 1440×1024 snapshot 생성·시각 검토

이는 local synthetic web UI 결과다. 실제 기관 사용, native UI, field reliability metric, production 배포를 증명하지 않는다.

관련: [ADR-0057](../decisions/ADR-0057-console-inspection-evidence-ui.md), [EVD-20260813-023](../evidence/2026-08-product.md#evd-20260813-023--console-예방점검-근거유보-ui), [HR-20260813-20](../reports/human/HR-20260813-20-console-inspection-evidence-ui.md), [R10](../reports/fixed/2026-09-30.md)

