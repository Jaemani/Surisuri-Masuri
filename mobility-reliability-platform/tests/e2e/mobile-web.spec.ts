import { expect, test } from '@playwright/test';

test.describe('mobile web visual preview', () => {
  test('renders and completes the primary recording state transition', async ({ page }) => {
    await page.goto('/');

    await expect(page.getByRole('heading', { name: /오늘의 이동을/ })).toBeVisible();
    await expect(page.getByText('기록 대기')).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-home.png', { animations: 'disabled' });

    await page.getByRole('button', { name: '주행 시작' }).click();
    await expect(page.getByText('주행 기록 중')).toBeVisible();
    await expect(page.getByRole('button', { name: '주행 종료' })).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-recording.png', { animations: 'disabled' });

    await page.getByRole('button', { name: '주행 종료' }).click();
    await expect(page.getByText('기록 대기')).toBeVisible();
  });
});
