# HR-20260813-18 — 복지관 콘솔 완료 수리 타임라인 설계 점검

- 발행일: 2026-08-13
- 상태: generated / 사람 검토 대기
- 대상 독자: 프로젝트 운영자·기술 검토자
- 로드맵 게이트: R09 Device Timeline & Reliability

## 사람 대상 리포트 필요성 판단

필요하다. 모바일 화면에 먼저 연결된 완료 수리 archive replay를 복지관 콘솔로 확장하면 역할별 projection과 사실 기준이 바뀌므로, 구현 전에 운영자에게 표시 범위와 금지 범위를 설명하고 승인받을 별도 설계 리포트가 유용하다. 다만 이번 문서는 실제 console 기능 출시 보고서가 아니다.

## 확정한 범위

복지관 운영자가 기기 상세에서 완료 수리 이력과 구조화 작업 항목을 확인할 수 있도록 server-side read-time replay를 추가한다. mutable work order, raw issue, UID, subsidy account, GPS와 component linkage 추정을 제외한다.

## 현재 실제 상태

복지관 콘솔의 기기 관리 화면이 목록과 상세 패널로 분리되었고, 현재 상태·센터 검증 완료 수리 이력·예방점검/수리 운영 이동을 한 화면에서 확인할 수 있다. 서버 projection은 mutable work order가 아니라 verified completed repair archive와 구조화 items를 읽는다. console decoder는 누락되거나 잘못된 timeline을 fail-closed한다. 모바일 beneficiary의 선행 범위는 [UPD-20260813-20](../../product-updates/UPD-20260813-20-completed-repair-timeline-replay.md)과 [EVD-20260813-016](../../evidence/2026-08-product.md#evd-20260813-016--완료-수리-archive-기기-타임라인-replay)에 기록되어 있다.

## 결정 후보

- console projection에 목적 제한 `completedRepairTimeline`을 추가한다.
- 동일한 replay/checksum 규칙을 사용한다.
- empty history, corrupt history, tenant mismatch, 민감값 scan을 필수 검증으로 둔다.
- component installation은 명시적 linkage 계약 전까지 생성하지 않는다.

## 근거와 한계

- 설계 근거: [ADR-0051](../../decisions/ADR-0051-console-completed-repair-timeline-read-time-replay.md)
- 계획 업데이트: [UPD-20260813-21](../../product-updates/UPD-20260813-21-console-completed-repair-timeline-plan.md)
- 실행 증거: [EVD-20260813-017](../../evidence/2026-08-product.md#evd-20260813-017--console-완료-수리-타임라인)

실제 구현·배포·기관 사용·현장 수리 성과는 아직 확인하지 않았다. 실제 회의 일시·참석자·사진·지출은 사람 확인 전 입력하지 않는다.

## 다음 회차

authoritative 완료 수리와 합성 점검·등록 이벤트의 화면 출처를 더 명확히 분리하고, R09의 async current projection·legacy import gate로 진행한다. 실제 기관 검토가 이뤄지면 화면 이해도와 업무 조치 적합성을 별도 field evidence로 기록한다.
