import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

import { defineConfig, devices } from '@playwright/test';

const projectRoot = resolve(__dirname);
const consoleUrl = process.env.PLAYWRIGHT_CONSOLE_URL ?? 'http://127.0.0.1:19007';
const localConsole = !process.env.PLAYWRIGHT_CONSOLE_URL;

export default defineConfig({
  testDir: './tests/presentation',
  fullyParallel: false,
  workers: 1,
  reporter: [['list']],
  outputDir: './test-results/presentation',
  use: {
    ...devices['Desktop Chrome'],
    baseURL: consoleUrl,
    viewport: { width: 1440, height: 1024 },
    deviceScaleFactor: 2,
    colorScheme: 'light',
    locale: 'ko-KR',
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
      : undefined,
  },
  webServer: localConsole && existsSync(resolve(projectRoot, 'apps/console/package.json'))
    ? {
        command: 'CI=1 pnpm --filter @mobility-reliability/console run web:e2e',
        cwd: projectRoot,
        url: consoleUrl,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
      }
    : undefined,
});
