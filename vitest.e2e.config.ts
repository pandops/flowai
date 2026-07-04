import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['apps/**/test/e2e/**/*.test.{ts,tsx}', 'packages/**/test/e2e/**/*.test.{ts,tsx}'],
    environment: 'node',
    passWithNoTests: true,
    testTimeout: 120_000,
  },
});
