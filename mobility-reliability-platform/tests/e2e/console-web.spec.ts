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
    await expect(page).toHaveScreenshot('console-repairs.png', {
      animations: 'disabled',
    });

    const registerRepair = page.getByRole('button', { name: /새 수리 요청/ }).first();
    await expect(registerRepair).toBeVisible();
    await registerRepair.click();

    await expect(page.getByText(/새 수리 요청 작성 화면은 데모에서 준비 중입니다/)).toBeVisible();
    await expect(page.getByText('전체 수리 요청')).toBeVisible();
  });
});
