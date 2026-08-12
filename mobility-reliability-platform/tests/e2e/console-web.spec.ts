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
});
