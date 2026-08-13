import { expect, test } from '@playwright/test';

test.describe('institution console web presentation and repair operations', () => {
  test('renders the operating overview without exposing raw trip paths', async ({ page }) => {
    await page.goto('/');

    await expect(page.getByRole('heading', { name: /좋은 아침이에요|오늘 할 일/ }).first()).toBeVisible();
    await expect(page.getByText(/기기|사용자|수리|점검|보고서/).first()).toBeVisible();
    await expect(page.getByText(/원본 이동경로|원시 위치|좌표/)).toHaveCount(0);
    await expect(page).toHaveScreenshot('console-overview.png', {
      animations: 'disabled',
    });
  });

  test('opens repair operations and keeps empty submissions honest', async ({ page }) => {
    await page.goto('/');

    const repairNavigation = page.getByRole('button', { name: /수리 운영 7/ });
    await expect(repairNavigation).toBeVisible();
    await repairNavigation.click();

    await expect(page.getByRole('heading', { name: '수리 운영' })).toBeVisible();
    await expect(page.getByText('SYNTHETIC DEMO · REVISION 1')).toBeVisible();
    await expect(page.getByRole('button', { name: '파트너 배정하기' })).toBeDisabled();
    await page.getByLabel('수리소 ID (합성)').selectOption('station-hanmaeum');
    await page.getByLabel('담당 수리사 Firebase UID (합성)').selectOption('demo-repairer-kim');
    await expect(page.getByRole('button', { name: '파트너 배정하기' })).toBeEnabled();
    await expect(page).toHaveScreenshot('console-repairs.png', {
      animations: 'disabled',
    });
    await page.getByRole('button', { name: '파트너 배정하기' }).click();
    await expect(page.getByText('파트너 배정 단계로 변경되었습니다.')).toBeVisible();
    await expect(page.getByText('수리사 처리 대기')).toBeVisible();
  });

  test('records center verification and subsidy execution as two projection-backed steps', async ({ page }) => {
    await page.goto('/');
    await page.getByRole('button', { name: /수리 운영 7/ }).click();
    await page.getByRole('button', { name: /등받이 고정 레버 교체/ }).click();

    await expect(page.getByText('센터 검증 · 지원금 집행')).toBeVisible();
    await expect(page.getByLabel('검증 및 집행 순서')).toContainText('1 · 센터 검증');
    await expect(page.getByLabel('검증 및 집행 순서')).toContainText('2 · projection 확인');
    await expect(page.getByLabel('검증 및 집행 순서')).toContainText('3 · 지원금 집행');
    await page.getByLabel('수리 결과를 확인했습니다.').check();
    await page.getByLabel('청구 금액을 확인했습니다.').check();
    await page.getByLabel('지원금 적격성을 확인했습니다.').check();
    await expect(page.getByRole('button', { name: '검증 후 집행 요청' })).toBeEnabled();
    await expect(page).toHaveScreenshot('console-repair-authority-review.png', { animations: 'disabled' });

    await page.getByRole('button', { name: '검증 후 집행 요청' }).click();
    await expect(page.getByText('센터 검증과 지원금 집행을 각각 기록하고 최신 원장을 확인했습니다.')).toBeVisible();
    await expect(page.getByText('지원금 집행 원장까지 확인되었습니다.')).toBeVisible();
    await expect(page.getByText('projection revision 5 · center_verified')).toBeVisible();
    await page.getByRole('button', { name: '지원금 원장' }).first().click();
    await expect(page.getByText('합성 기관 담당자')).toBeVisible();
  });

  test('opens a device record with verified repair history and operational actions', async ({ page }) => {
    await page.goto('/');

    await page.getByRole('button', { name: /기기 관리 4/ }).click();
    await expect(page.getByRole('heading', { name: '기기 관리' })).toBeVisible();
    await page.getByRole('button', { name: 'MOB-24018 상세 보기' }).click();

    await expect(page.getByRole('heading', { name: '기기 타임라인' })).toBeVisible();
    await expect(page.getByText('타이어 점검을 완료했어요')).toBeVisible();
    await expect(page.getByRole('button', { name: /예방점검 열기/ })).toBeVisible();
    await expect(page.getByText(/원본 이동경로|원시 위치|좌표/)).toHaveCount(0);
    await expect(page).toHaveScreenshot('console-device-timeline.png', {
      animations: 'disabled',
    });
  });

  test('separates inspection evidence, abstention, and operational next action', async ({ page }) => {
    await page.goto('/');

    await page.getByRole('button', { name: /예방점검 12/ }).click();
    await expect(page.getByRole('heading', { name: '예방점검' })).toBeVisible();
    await expect(page.getByRole('heading', { name: '근거별 검토함' })).toBeVisible();
    await expect(page.getByText('우선순위 예측이 아닙니다')).toBeVisible();
    await page.getByRole('button', { name: /이경자 · MOB-23874/ }).click();
    await expect(page.getByText('판단 유보', { exact: true }).first()).toBeVisible();
    await expect(page.getByText('decision-time 거리 요약')).toBeVisible();
    await expect(page.getByText(/고장 시점이나 안전을 보증하지 않습니다/)).toBeVisible();
    await expect(page.getByText(/원본 이동경로|원시 위치|좌표|고장 확률/)).toHaveCount(0);
    await expect(page).toHaveScreenshot('console-inspection-evidence.png', {
      animations: 'disabled',
    });
  });

  test('shows report-level synthetic baseline comparison without individual actions', async ({ page }) => {
    await page.goto('/');

    await page.getByRole('button', { name: '보고서', exact: true }).click();
    await expect(page.getByRole('heading', { name: '보고서' })).toBeVisible();
    await expect(page.getByText('SYNTHETIC-ONLY · 배포 보류')).toBeVisible();
    await expect(page.getByText('FIXED INTERVAL')).toBeVisible();
    await expect(page.getByText('CUMULATIVE DISTANCE')).toBeVisible();
    await expect(page.getByText('KAPLAN–MEIER')).toBeVisible();
    await expect(page.getByText('TRAIN CURVE / RULE')).toBeVisible();
    await expect(page.getByText('TEST METRICS')).toBeVisible();
    await expect(page.getByText('R11 · CALIBRATION READINESS')).toBeVisible();
    await expect(page.getByText('전체 판단 유보')).toBeVisible();
    await expect(page.getByText('검증 표본 부족')).toHaveCount(2);
    await expect(page.getByText('기준선 학습 근거 부족')).toBeVisible();
    await expect(page.getByText('실제 기기 위험도, 현장 calibration, 개별 운영 조치가 아닙니다.')).toBeVisible();
    await expect(page.getByText('CONTROLLER / ABSTENTION')).toBeVisible();
    await expect(page.getByText('판단 유보', { exact: true })).toBeVisible();
    await expect(page.getByText(/MOB-|박정호|이경자|윤옥순|최민수/)).toHaveCount(0);
    await expect(page.getByRole('button', { name: /새 보고서|필터|내보내기/ })).toHaveCount(0);
    await expect(page).toHaveScreenshot('console-reports-baseline-comparison.png', {
      animations: 'disabled',
    });
  });
});
