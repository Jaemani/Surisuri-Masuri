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
});
