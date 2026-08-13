# 제품 업데이트 기록

제품 업데이트는 **제품 또는 제품을 이루는 공학 기반에서 실제로 발생했고, 지정된 환경에서 검증된 변화**만 기록한다. 작업 계획, 단순 커밋 목록, 회의 메모가 아니다.

사용자 릴리스와 로컬 공학 증분은 같은 상태로 취급하지 않는다. 로컬·합성·Emulator에서만 확인한 변화도 향후 제품 동작을 구성하는 독립 증분이면 기록할 수 있지만, `배포 환경`, `데이터 유형`, `제외 범위`와 사람 검토 상태를 명시한다. 이런 문서는 production 배포나 사용자 이용 가능성을 주장하지 않는다.

## 작성 트리거

다음 중 하나가 발생하면 작성한다.

- 사용자·수리사·복지관 콘솔에서 확인 가능한 기능이 배포되었을 때
- 데이터 계약, 동기화, 권한, 모델 또는 보고서 동작이 운영 관점에서 달라졌을 때
- 중요한 성능·접근성·프라이버시 개선이 측정되어 반영되었을 때
- 인시던트 수정이 실제 환경에 반영되고 재발 방지 검증이 끝났을 때
- 제품의 실행 경계를 이루는 계약·보안·복구 증분이 local/test 환경에서 재현 가능하게 검증되고, 아직 연결되지 않은 범위를 명시했을 때

아직 구현·검증되지 않은 변경은 제품 업데이트가 아니라 사람 대상 리포트의 `계획` 또는 작업 추적 도구에 둔다. 구현됐지만 사람 검토 전인 문서는 `draft`, 주장 범위와 증거가 확인된 문서는 `verified`로 구분한다.

## 파일과 식별자

- 파일명: `YYYY-MM-DD-short-slug.md`
- ID: `UPD-YYYYMMDD-NN`
- 한 문서에는 한 배포 단위 또는 사용자가 이해할 수 있는 하나의 변화 묶음만 기록한다.
- [`_TEMPLATE.md`](./_TEMPLATE.md)를 복사해 작성한다.

## 필수 필드

- 업데이트 ID, 날짜, 상태, 버전 또는 배포 식별자
- 대상 사용자와 8개월 로드맵상의 마일스톤
- 변경 전 문제와 변경 후 동작
- 범위와 제외 범위
- 검증 방법, 환경, 결과와 증거 ID
- 배포·롤백 방법과 알려진 제한
- 관련 ADR, 인시던트, 사람 대상 리포트

## 금지사항

- 예정 기능을 완료한 것처럼 기록하지 않는다.
- 검증 없이 `안정적`, `빠름`, `정확함`, `해결됨`이라고 표현하지 않는다.
- local bundle·unit·Emulator 결과를 앱스토어, staging, production 또는 field 사용 가능성으로 확대하지 않는다.
- 개인정보, 원본 GPS 좌표, 비밀정보를 포함하지 않는다.
- 여러 커밋을 그대로 붙여 넣거나 의사결정 과정을 장문으로 복제하지 않는다.
- 장애 사후분석을 이 문서로 대체하지 않는다.

## 상호 링크

- 중요한 기술 선택: [`../decisions/`](../decisions/)
- 화면·테스트·측정 근거: [`../evidence/`](../evidence/)
- 장애 수정: [`../incidents/`](../incidents/)
- 해당 회차 보고: [`../reports/human/`](../reports/human/)

제품 업데이트에서 실제 결과를 주장하는 항목은 적어도 하나의 `EVD`를 가져야 한다. 장애 수정이라면 `INC`도 필수다.

## 전체 인벤토리

이 목록은 현재 상태 요약이 아니라 누락 방지를 위한 append-only 문서 인벤토리다. 최신 구현 경계는 [문서 인덱스의 현재 상태](../INDEX.md#4-2026-08-14-현재-검증된-구현-경계)를 먼저 본다.

- [UPD-20260721-01](./UPD-20260721-01-foundation.md)
- [UPD-20260721-02](./UPD-20260721-02-executable-foundation.md)
- [UPD-20260721-03](./UPD-20260721-03-firebase-security-foundation.md)
- [UPD-20260721-04](./UPD-20260721-04-foreground-telemetry.md)
- [UPD-20260723-05](./UPD-20260723-05-cleanup-retry-hold-control.md)
- [UPD-20260723-06](./UPD-20260723-06-cleanup-terminal-orchestration.md)
- [UPD-20260723-07](./UPD-20260723-07-receipt-purge-admission.md)
- [UPD-20260723-08](./UPD-20260723-08-nested-recovery-attempt-purge.md)
- [UPD-20260723-09](./UPD-20260723-09-legacy-purge-link-backfill.md)
- [UPD-20260723-10](./UPD-20260723-10-linked-cleanup-target-purge.md)
- [UPD-20260723-11](./UPD-20260723-11-mobile-upload-protocol.md)
- [UPD-20260723-12](./UPD-20260723-12-mobile-upload-ledger.md)
- [UPD-20260723-13](./UPD-20260723-13-mobile-upload-materializer.md)
- [UPD-20260723-14](./UPD-20260723-14-android-foreground-smoke.md)
- [UPD-20260723-15](./UPD-20260723-15-mobile-upload-lease.md)
- [UPD-20260723-16](./UPD-20260723-16-background-gps-static-boundary.md)
- [UPD-20260723-17](./UPD-20260723-17-android-background-native-smoke.md)
- [UPD-20260811-01](./UPD-20260811-01-mobile-upload-disposition.md)
- [UPD-20260811-02](./UPD-20260811-02-r07-synthetic-dataset-contract.md)
- [UPD-20260811-03](./UPD-20260811-03-r07-feature-rules-baseline.md)
- [UPD-20260813-01](./UPD-20260813-01-product-centered-mobile-and-domain.md)
- [UPD-20260813-02](./UPD-20260813-02-domain-command-boundary.md)
- [UPD-20260813-03](./UPD-20260813-03-product-http-repositories.md)
- [UPD-20260813-04](./UPD-20260813-04-purpose-limited-product-projections.md)
- [UPD-20260813-05](./UPD-20260813-05-completed-repair-history.md)
- [UPD-20260813-06](./UPD-20260813-06-repair-intake-and-center-actions.md)
- [UPD-20260813-07](./UPD-20260813-07-repairer-command-hardening.md)
- [UPD-20260813-08](./UPD-20260813-08-repairer-projection-contract.md)
- [UPD-20260813-09](./UPD-20260813-09-mobile-repairer-command-adapter.md)
- [UPD-20260813-10](./UPD-20260813-10-repairer-mobile-workspace.md)
- [UPD-20260813-11](./UPD-20260813-11-structured-repair-items.md)
- [UPD-20260813-12](./UPD-20260813-12-console-command-contract-alignment.md)
- [UPD-20260813-13](./UPD-20260813-13-device-code-verification-gate.md)
- [UPD-20260813-14](./UPD-20260813-14-native-qr-device-verification.md)
- [UPD-20260813-15](./UPD-20260813-15-workspace-regression-fixes.md)
- [UPD-20260813-16](./UPD-20260813-16-r07-pytorch-candidate.md)
- [UPD-20260813-17](./UPD-20260813-17-native-scheduling-control.md)
- [UPD-20260813-18](./UPD-20260813-18-mobile-multiple-repair-work-items.md)
- [UPD-20260813-19](./UPD-20260813-19-mobile-session-completion-summary.md)
- [UPD-20260813-20](./UPD-20260813-20-completed-repair-timeline-replay.md)
- [UPD-20260813-21](./UPD-20260813-21-console-completed-repair-timeline-plan.md)
- [UPD-20260813-22](./UPD-20260813-22-device-current-state-replay-plan.md)
- [UPD-20260813-23](./UPD-20260813-23-firestore-shadow-promotion-plan.md)
- [UPD-20260813-24](./UPD-20260813-24-legacy-repair-dry-run-bridge-plan.md)
- [UPD-20260813-25](./UPD-20260813-25-reliability-baseline-result-contract.md)
- [UPD-20260813-26](./UPD-20260813-26-r10-synthetic-reliability-baseline.md)
- [UPD-20260813-27](./UPD-20260813-27-console-inspection-evidence-ui.md)
- [UPD-20260813-28](./UPD-20260813-28-r10-synthetic-reliability-presentation.md)
- [UPD-20260813-29](./UPD-20260813-29-center-verification-subsidy-authority-loop.md)
- [UPD-20260813-30](./UPD-20260813-30-calibration-readiness-presentation.md)
- [UPD-20260813-31](./UPD-20260813-31-r12-grounded-report.md)
- [UPD-20260813-32](./UPD-20260813-32-r12-source-integrity.md)
- [UPD-20260813-33](./UPD-20260813-33-r12-complete-profile.md)
- [UPD-20260813-34](./UPD-20260813-34-report-claim-rules-binding.md)
- [UPD-20260813-35](./UPD-20260813-35-report-run-lifecycle.md)
- [UPD-20260813-36](./UPD-20260813-36-unsupported-claim-omission.md)
- [UPD-20260813-37](./UPD-20260813-37-web-visual-validation.md)
