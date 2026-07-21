# Institution console

복지관 운영자가 사용자·기기·수리·점검·보고서·실증 상태를 확인하는 신규 콘솔입니다. 기존 `soo-ri-admin`을 포크하지 않습니다.

원본 이동경로는 기본 기능이 아닙니다. 집계, 위험 근거, 데이터 품질, 운영 상태를 우선 표시합니다.

신규 콘솔은 Firebase Auth·App Check를 사용하고 Firebase Hosting에 배포하는 것을 기본으로 합니다. Firestore realtime listener는 작은 운영 projection에만 사용합니다.
