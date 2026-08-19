import { mkdirSync } from 'node:fs';
import { resolve } from 'node:path';

import { expect, type Page, test } from '@playwright/test';

const evidenceDirectory = resolve(
  process.cwd(),
  'docs/evidence/assets/EVD-20260820-003',
);

mkdirSync(evidenceDirectory, { recursive: true });

async function settlePresentation(page: Page) {
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise<void>((done) => {
      requestAnimationFrame(() => requestAnimationFrame(() => done()));
    });
  });
}

async function capture(page: Page, filename: string) {
  await settlePresentation(page);
  await page.screenshot({
    path: resolve(evidenceDirectory, filename),
    animations: 'disabled',
    caret: 'hide',
    scale: 'device',
  });
}

test.describe('high-resolution console presentation evidence', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
    await expect(page).toHaveTitle('수리수리마수리 | 복지관 운영 콘솔');
  });

  test('captures the operating overview', async ({ page }) => {
    await expect(page.getByRole('heading', { name: /좋은 아침이에요|오늘 할 일/ }).first()).toBeVisible();
    await capture(page, '01-console-overview-2x.png');
  });

  test('captures center verification and subsidy execution', async ({ page }) => {
    await page.getByRole('button', { name: /수리 운영 7/ }).click();
    await page.getByRole('button', { name: /등받이 고정 레버 교체/ }).click();
    await page.getByLabel('수리 결과를 확인했습니다.').check();
    await page.getByLabel('청구 금액을 확인했습니다.').check();
    await page.getByLabel('지원금 적격성을 확인했습니다.').check();
    await expect(page.getByRole('button', { name: '검증 후 집행 요청' })).toBeEnabled();
    await capture(page, '02-console-authority-review-2x.png');
  });

  test('captures inspection evidence', async ({ page }) => {
    await page.getByRole('button', { name: /예방점검 12/ }).click();
    await page.getByRole('button', { name: /이경자 · MOB-23874/ }).click();
    await expect(page.getByText('판단 유보', { exact: true }).first()).toBeVisible();
    await capture(page, '03-console-inspection-evidence-2x.png');
  });

  test('captures baseline comparison and grounded report', async ({ page }) => {
    await page.getByRole('button', { name: '보고서', exact: true }).click();
    await expect(page.getByText('R11 · CALIBRATION READINESS')).toBeVisible();
    await capture(page, '04-console-baseline-comparison-2x.png');

    await page.getByText('R12 · EVIDENCE-GROUNDED REPORT').scrollIntoViewIfNeeded();
    await capture(page, '05-console-grounded-report-2x.png');
  });
});
