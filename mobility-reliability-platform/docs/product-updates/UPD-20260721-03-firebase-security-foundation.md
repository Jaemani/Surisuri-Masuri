---
id: UPD-20260721-03
date: 2026-07-21
status: draft
version_or_deployment: firebase-security-foundation-v0
roadmap_month: M3
owner: project owner
reviewed_at: 2026-07-21
---

# 제품 업데이트: Firebase 우선 보안·로컬 검증 기반 추가

## 요약

Firebase를 운영 control plane으로 사용하는 결정을 반영하고, production 자격증명 없이 Emulator Suite에서 tenant 격리와 client 접근 차단을 검증할 수 있는 기반을 추가했다. GPS sample을 Firestore에 개별 저장하지 않는 비용 경계도 데이터 모델과 규칙 책임에 반영했다.

## 변경 전 문제

- PostgreSQL/PostGIS 중심의 초기 런타임 결정은 소규모 실증의 배포·관리 비용 목표와 맞지 않았다.
- Firebase를 사용하더라도 tenant 문서, server-only projection, raw telemetry의 client 접근 경계가 실행 가능한 규칙으로 고정되지 않았다.
- WSL2에서 Android·iPhone·Firebase Emulator를 어떤 경로로 검증할지 재현 절차가 없었다.

## 변경 후 동작

- ADR-0007이 Firebase Auth·App Check·Firestore control plane과 Cloud Run·Storage telemetry plane을 현재 결정으로 고정한다.
- Firestore Rules는 active membership을 기반으로 tenant read를 제한하고, 허용된 역할만 수리·점검 문서를 정해진 필드 계약으로 생성·수정하게 한다.
- tenant·member·device·ingest receipt·device state·report는 client write를 거부한다.
- Storage Rules는 모든 client read/write를 거부하며 raw telemetry는 후속 Cloud Run Admin SDK 경계에서만 다루도록 남겨둔다.
- demo project와 Firebase Emulator Suite만 사용하는 8개 회귀 시나리오가 통과한다.
- WSL runbook과 doctor가 `/home` workspace, Java, Android ADB, Go, iOS EAS 제약을 구분해 보고한다.

## 범위

- 포함: Firebase 로컬 설정, Firestore·Storage Rules, rules tests, Firebase-first 데이터 문서, WSL2 runbook·doctor.
- 제외: production Firebase project 연결·배포, service account, 실제 Auth/App Check, Cloud Run gateway, Storage batch upload, 실기기 네트워크 연결.
- 배포 환경: `local emulator only`
- 데이터 유형: `synthetic rules fixture`

## 검증

| 완료 조건 | 검증 방법 | 결과 | 증거 ID·링크 |
| --- | --- | --- | --- |
| tenant·역할·server-only Firestore 경계 | Emulator Suite rules test 6건 | `pass` | [EVD-20260721-005](../evidence/2026-07.md#evd-20260721-005--firebase-security-rules-회귀검사) |
| Storage client 전면 차단 | Emulator Suite rules test 2건 | `pass` | [EVD-20260721-005](../evidence/2026-07.md#evd-20260721-005--firebase-security-rules-회귀검사) |
| 현재 WSL2 개발 요건 | `pnpm doctor` | Node·Java `pass`, ADB·Go `warn` | [EVD-20260721-006](../evidence/2026-07.md#evd-20260721-006--wsl2-개발환경-진단) |

## 배포와 롤백

- production 배포는 수행하지 않았다. `.firebaserc.example`의 demo project만 사용한다.
- 실제 프로젝트 연결 전 별도 환경·권한·budget gate와 사람 검토가 필요하다.
- 규칙을 완화할 때는 먼저 실패해야 하는 회귀 테스트를 추가하며, client raw telemetry 접근은 ADR 변경 없이 허용하지 않는다.

## 알려진 제한과 후속 작업

- Rules test는 production IAM, Admin SDK와 App Check를 검증하지 않는다.
- 현재 WSL PATH에 `adb`/`adb.exe`와 Go가 없다.
- Android는 Windows platform-tools와 `adb reverse`, iPhone은 EAS development build와 실제 장비로 후속 검증한다.
- 다음 제품 변경은 foreground 위치 권한, 명시적 주행 세션, SQLite append-only outbox다.

## 관련 기록

- 결정: [ADR-0007](../decisions/ADR-0007-firebase-first-hybrid.md)
- 증거: [EVD-20260721-005](../evidence/2026-07.md#evd-20260721-005--firebase-security-rules-회귀검사), [EVD-20260721-006](../evidence/2026-07.md#evd-20260721-006--wsl2-개발환경-진단)
- 인시던트: 해당 없음 — production runtime과 사용자 영향 없음
- 사람 대상 리포트: [HR-20260721-01](../reports/human/HR-20260721-01-project-initiation.md)
- 대체하는 업데이트: 해당 없음

## 검토

- 검토자: Codex 자체 검토 — 사람 검토 필요
- 실제 주장과 근거 일치 여부: Emulator Suite rules test와 doctor 관측 범위에서 일치
- 검토 메모: production 배포·실기기·실사용 보안이 검증된 것으로 확대 해석하지 않는다.
