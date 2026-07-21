# Contributing

이 저장소는 작은 논리 단위의 변경과 검증 가능한 근거를 우선합니다.

## 작업 전 Git 작성자 확인

작업을 시작하기 전에 반드시 현재 Git identity가 개인 계정 `Jaemani`인지 확인합니다. 임시 계정이나 자동 생성 계정으로 작업하지 않습니다.

```bash
rtk git config --get user.name
rtk git config --get user.email
```

이름이 `Jaemani`가 아니거나 이메일이 본인 계정과 일치하지 않으면 파일을 수정하거나 커밋하기 전에 설정을 바로잡습니다. 저장소에 커밋된 author 정보도 push 전에 다시 확인합니다.

## 명령 실행 규칙

모든 셸 명령에는 `rtk` 접두사를 사용합니다. 명령을 연결할 때는 각 구간에 각각 붙입니다.

```bash
rtk git status
rtk pnpm check
rtk git add path/to/file && rtk git commit -m "docs: record telemetry decision"
```

## 브랜치와 커밋

- `main`에 직접 큰 변경을 쌓지 말고 목적이 명확한 작업 브랜치를 사용합니다.
- 한 커밋에는 되돌리거나 검토할 수 있는 하나의 논리적 변경만 포함합니다.
- 생성 파일, 포맷 변경, 기능 변경을 무관하게 한 커밋에 섞지 않습니다.
- 커밋 전 `rtk git diff`와 `rtk git status`로 포함 범위를 확인합니다.
- 민감 자료가 들어간 커밋은 수정 커밋으로 덮지 말고 즉시 노출 대응 절차를 시작합니다.

커밋 메시지는 다음 semantic type을 사용합니다.

- `feat`: 사용자에게 보이는 기능
- `fix`: 결함 수정
- `docs`: 문서만 변경
- `test`: 테스트 추가·수정
- `refactor`: 동작을 유지하는 구조 개선
- `perf`: 성능 개선
- `build`: 빌드 또는 의존성 변경
- `ci`: CI·자동화 변경
- `chore`: 그 밖의 유지보수
- `revert`: 이전 커밋 되돌리기

형식은 `<type>(선택적 scope): 명령형 요약`입니다.

```text
feat(mobile): persist telemetry events offline
docs(adr): record location retention decision
```

## 민감 자료 금지

다음 항목은 코드, 커밋, fixture, 로그, 스크린샷, 이슈 또는 PR에 올리지 않습니다.

- 이름, 연락처, 계정 식별자 등 PII
- 원본 GPS 좌표와 출발·도착 민감 위치
- Firebase·클라우드 키, 토큰, 인증서, 서비스 계정 파일
- 복지관이 제공한 식별 가능한 수리·상담 원본 데이터

테스트에는 비식별 합성 데이터를 사용하고, 합성 데이터와 실제 현장 데이터라는 표기를 유지합니다. 비밀이나 민감 정보가 노출되면 즉시 키를 폐기·교체하고 incident 문서를 작성합니다.

## 품질 확인

프로젝트 명령은 `mobility-reliability-platform` 디렉터리에서 실행합니다.

```bash
rtk pnpm install --frozen-lockfile
rtk pnpm check
rtk pnpm test
```

PR을 열기 전 모든 명령이 통과해야 합니다. GPS·PII·데이터 계약·마이그레이션·모델 결과에 영향을 주는 변경은 관련 ADR, 제품 업데이트, incident, 사람용 리포트, evidence의 갱신 여부도 확인합니다.
