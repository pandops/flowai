import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: [
      'apps/**/test/contract/**/*.test.{ts,tsx}',
      'packages/**/test/contract/**/*.test.{ts,tsx}',
    ],
    environment: 'node',
    passWithNoTests: true,
  },
});
