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
    await expect(page.getByText('처리할 작업')).toBeVisible();
    await expect(page.getByText('이용자 C-1042')).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-repairer-list.png', { animations: 'disabled', fullPage: true });
    await page.getByRole('button', { name: '일정 정하기' }).click();
    await expect(page.getByRole('heading', { name: '오른쪽 바퀴 소음 점검' })).toBeVisible();
    await expect(page.getByText('MR-2208', { exact: true })).toBeVisible();
    await expect(page.getByText('지원금 잔액 없이', { exact: false })).toHaveCount(0);
    await expect(page.getByRole('button', { name: '기기 코드 확인 필요' })).toBeDisabled();
    await page.getByLabel('현장 기기 공개코드').fill('mr-2208');
    await expect(page.getByText('기기 코드가 일치합니다')).toBeVisible();
    await expect(page).toHaveScreenshot('mobile-repairer-workspace.png', { animations: 'disabled', fullPage: true });
    await page.getByRole('button', { name: '방문 일정 확정' }).click();
    await expect(page.getByRole('heading', { name: '방문 일정 선택' })).toBeVisible();
    await expect(page.getByText('Android와 iPhone에서는 기기의 날짜·시간 선택기가 열립니다.')).toBeVisible();
    await page.getByRole('button', { name: '30분 뒤' }).click();
    await expect(page).toHaveScreenshot('mobile-repairer-schedule.png', { animations: 'disabled', fullPage: true, mask: [page.getByTestId('repairer-schedule-summary')] });
    await page.getByRole('button', { name: '이 일정으로 확정' }).click();
    await expect(page.getByText('현장 확인 후 작업을 시작하세요')).toBeVisible();
    await page.getByRole('button', { name: '현장 확인 후 작업 시작' }).click();
    await expect(page.getByText('작업을 마치면 결과를 제출하세요')).toBeVisible();
    await page.getByRole('button', { name: '비용 입력 및 제출' }).click();
    await page.getByLabel('수리 청구 금액').fill('85000');
    await page.getByRole('radio', { name: '브레이크' }).click();
    await page.getByRole('radio', { name: '교체' }).click();
    await page.getByRole('button', { name: '작업 내용 확인하고 제출' }).click();
    await expect(page.getByText('복지관 검증을 기다리고 있어요')).toBeVisible();
  });
});
