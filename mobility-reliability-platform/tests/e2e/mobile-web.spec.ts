import { expect, test } from '@playwright/test';

test.describe('mobile web visual preview', () => {
  test('renders and completes the primary recording state transition', async ({ page }) => {
    await page.goto('/');

    await expect(page.getByRole('heading', { name: '안녕하세요, 김정자님' })).toBeVisible();
    await expect(page.getByText('진행 중인 수리', { exact: true })).toBeVisible();
    await expect(page.getByText('오른쪽 바퀴에서 소리가 나요')).toBeVisible();
    await expect(page.getByText('남은 수리 지원금', { exact: true })).toBeVisible();
    await expect(page.getByText('이동 사용량 기록', { exact: true })).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-home.png', { animations: 'disabled' });

    await page.getByRole('button', { name: '이동 기록 시작' }).click();
    await expect(page.getByText('이동 사용량을 기록하고 있어요')).toBeVisible();
    await expect(page.getByRole('button', { name: '이동 마치기' })).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-recording.png', { animations: 'disabled' });
  });

  test('covers repair request, support, and repairer workspaces', async ({ page }) => {
    await page.goto('/');

    await page.getByRole('button', { name: '수리', exact: true }).click();
    await expect(page.getByText('수리 도움', { exact: true })).toBeVisible();
    await expect(page.getByText('오른쪽 바퀴에서 소리가 나요')).toBeVisible();
    await expect(page.getByText('따뜻한바퀴 수리센터')).toBeVisible();
    await page.getByRole('button', { name: '새 수리 요청 작성' }).click();
    await expect(page.getByRole('heading', { name: '수리 요청 내용' })).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-repair-intake.png', { animations: 'disabled', fullPage: true });
    await page.getByRole('radio', { name: '브레이크' }).click();
    await page.getByLabel('증상 상세 설명').fill('어제부터 브레이크가 늦게 잡히고 소리가 나요');
    await page.getByRole('radio', { name: '복지관 수리비 지원 신청' }).click();
    await page.getByLabel('예상 수리비').fill('120000');
    await page.getByRole('button', { name: '입력 내용 확인' }).click();
    await expect(page.getByRole('heading', { name: '보내기 전 확인해 주세요' })).toBeVisible();
    await expect(page.getByText('신청 · 120,000원')).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-repair-review.png', { animations: 'disabled', fullPage: true });
    await page.getByRole('button', { name: '수정하기' }).click();
    await page.getByRole('button', { name: '진행 중인 요청으로 돌아가기' }).click();

    await page.getByRole('button', { name: '복지지원' }).click();
    await expect(page.getByRole('button', { name: '복지지원' })).toHaveAttribute('aria-label', '복지지원');
    await expect(page.getByText('전동보장구 수리 지원금')).toBeVisible();

    await page.getByRole('button', { name: '설정·알림' }).click();
    await expect(page.getByRole('button', { name: '설정·알림' })).toHaveAttribute('aria-label', '설정·알림');
    await page.getByRole('button', { name: '개발용 역할 전환' }).click();
    await expect(page.getByRole('heading', { name: '오늘의 작업' })).toBeVisible();
    await expect(page.getByText('오늘 처리할 작업')).toBeVisible();
    await expect(page.getByText('김정자 님')).toBeVisible();
  });
});
