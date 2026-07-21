# Infrastructure

초기 배포 단위는 작게 유지합니다.

- Firebase Auth, App Check, Firestore, FCM, Crashlytics, Remote Config, Hosting
- Cloud Storage raw telemetry batches with lifecycle rules
- scale-to-zero Go Cloud Run telemetry gateway
- optional BigQuery partitioned analytics when justified by usage
- institution console/API
- ML batch/evaluation job using approved Storage/BigQuery snapshots
- OpenTelemetry-compatible traces and metrics

원본 위치와 실제 사용자 데이터는 로컬 저장소나 Git에 커밋하지 않습니다. 합성 fixture는 명시적인 synthetic label과 함께 별도 관리합니다.
