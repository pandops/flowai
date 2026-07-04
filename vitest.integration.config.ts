import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: [
      'apps/**/test/integration/**/*.test.{ts,tsx}',
      'packages/**/test/integration/**/*.test.{ts,tsx}',
    ],
    environment: 'node',
    passWithNoTests: true,
    testTimeout: 30_000,
  },
});
