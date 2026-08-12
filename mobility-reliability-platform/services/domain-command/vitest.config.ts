import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    testTimeout: 15_000,
    include: ['test/**/*.test.ts'],
    environment: 'node',
    fileParallelism: false,
    maxWorkers: 1,
  },
});
