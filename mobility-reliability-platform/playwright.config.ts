import { existsSync } from 'node:fs';
import { resolve } from 'node:path';

import { defineConfig, devices } from '@playwright/test';

const projectRoot = resolve(__dirname);
const mobileUrl = process.env.PLAYWRIGHT_MOBILE_URL ?? 'http://127.0.0.1:19006';
const consoleUrl = process.env.PLAYWRIGHT_CONSOLE_URL ?? 'http://127.0.0.1:19007';
const consolePackageExists = existsSync(resolve(projectRoot, 'apps/console/package.json'));
const consoleEnabled =
  Boolean(process.env.PLAYWRIGHT_CONSOLE_URL) ||
  consolePackageExists ||
  process.env.PLAYWRIGHT_ENABLE_CONSOLE === '1';

const mobileCommand =
  process.env.PLAYWRIGHT_MOBILE_COMMAND ??
  'CI=1 pnpm --filter @mobility-reliability/mobile run web:e2e';
const consoleCommand =
  process.env.PLAYWRIGHT_CONSOLE_COMMAND ??
  'CI=1 pnpm --filter @mobility-reliability/console run web:e2e';

const webServers = [
  ...(process.env.PLAYWRIGHT_MOBILE_URL
    ? []
    : [
        {
          command: mobileCommand,
          cwd: projectRoot,
          url: mobileUrl,
          reuseExistingServer: !process.env.CI,
          timeout: 120_000,
        },
      ]),
  ...(consoleEnabled && !process.env.PLAYWRIGHT_CONSOLE_URL
    ? [
        {
          command: consoleCommand,
          cwd: projectRoot,
          url: consoleUrl,
          reuseExistingServer: !process.env.CI,
          timeout: 120_000,
        },
      ]
    : []),
];

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
    video: 'retain-on-failure',
  },
  webServer: webServers.length > 0 ? webServers : undefined,
  projects: [
    {
      name: 'mobile-chromium',
      testMatch: /mobile-web\.spec\.ts/,
      use: {
        ...devices['Desktop Chrome'],
        baseURL: mobileUrl,
        launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
          ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
          : undefined,
        viewport: { width: 390, height: 844 },
      },
    },
    ...(consoleEnabled
      ? [
          {
            name: 'console-chromium',
            testMatch: /console-web\.spec\.ts/,
            use: {
              ...devices['Desktop Chrome'],
              baseURL: consoleUrl,
              launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
                ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH }
                : undefined,
              viewport: { width: 1440, height: 1024 },
            },
          },
        ]
      : []),
  ],
});
