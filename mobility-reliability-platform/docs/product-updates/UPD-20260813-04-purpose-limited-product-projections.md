# UPD-20260813-04 — 목적 제한 제품 projection 연결

- 기준일: 2026-08-13
- 상태: 구현·local 검증 완료 / 사람 검토 대기
- 환경: WSL2, Firestore Emulator, synthetic fixture

## 결과

모바일과 복지관 콘솔이 operational Firestore 문서를 직접 조합하지 않고, 인증된 서버 projection을 통해 화면에 필요한 최소 DTO만 읽는 경계를 구현했다.

- `getMobileProductSnapshot`: 이용자 본인의 기기·수리·지원금 또는 수리사에게 배정된 작업 목록
- `getConsoleOperationsSnapshot`: 복지관 담당자용 dashboard/users/devices/repairs/ledger/inspections/partners/reports/services projection
- Firebase ID token, App Check, 정확히 하나의 tenant scope 요구
- 응답 `private, no-store`, `nosniff`
- production adapter는 오류 시 합성 demo로 후퇴하지 않음
- 실제 Firebase 설정 전 앱과 콘솔의 기본 preview는 명시적 deterministic demo 유지

## 보호 경계

- `privatePeople`, 원본 GPS/trip, Storage object path를 projection 생성에 사용하지 않는다.
- 수리사에는 배정된 작업만 제공하며 지원금·임의 기기 placeholder를 제공하지 않는다.
- 복지관·수리사 수리 문구는 검증된 redaction 또는 category label만 제공한다.
- partner 연락처, ledger actor, 점검·보고서 자유문을 운영 DTO에 그대로 전달하지 않는다.
- nested 문서의 `tenant_id`, 알 수 없는 상태, 중복·유효기간 밖 기기 배정은 fail closed한다.
- collection별 200건 상한과 ledger 전체 200건 fan-out 상한을 둔다.

## 계약 변경

모바일 응답을 역할별 discriminated union으로 분리했다. 이용자 응답에는 기기·수리·지원금만, 수리사 응답에는 배정 작업만 존재한다. 앱 parser와 화면 selector도 같은 union을 사용한다.

## 한계

- 실제 Firebase Auth/App Check token과 production deployment는 아직 검증하지 않았다.
- guardian 대상 선택과 관계 검증 projection은 아직 fail closed다.
- cross-organization 수리소 grant는 아직 지원하지 않는다.
- 현재 bounded scan은 소규모 파일럿용이며 200건 초과 시 pagination/query projection으로 교체해야 한다.
- 여러 collection을 읽는 dashboard는 transaction 시점이 같은 원자적 snapshot이라고 주장하지 않는다.

