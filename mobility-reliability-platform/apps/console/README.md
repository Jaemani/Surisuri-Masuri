# Institution console

복지관 운영자가 수리 요청 접수, 수리소 배정, 작업 검증, 지원금 집행, 사용자·기기·점검·보고서를 관리하는 신규 콘솔입니다. 기존 `soo-ri-admin`을 포크하지 않습니다.

원본 이동경로는 기본 기능이 아닙니다. 집계, 위험 근거, 데이터 품질, 운영 상태를 우선 표시합니다.

신규 콘솔은 Firebase Auth·App Check를 사용하고 Firebase Hosting에 배포하는 것을 기본으로 합니다. Firestore realtime listener는 작은 운영 projection에만 사용합니다.

## 현재 실행 범위

- `pnpm --filter @mobility-reliability/console dev` — Vite 개발 서버
- `pnpm --filter @mobility-reliability/console web:e2e` — Playwright용 `127.0.0.1:19007`
- `pnpm --filter @mobility-reliability/console build` — 정적 production bundle

현재 화면의 이름·기기·금액·업무 건은 deterministic synthetic demo다. Firebase 운영 데이터, 실제 복지관 업무, 실제 지원금 집행과 연결됐다는 뜻이 아니다.
