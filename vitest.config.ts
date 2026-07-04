import { defineConfig } from 'vitest/config';

export default defineConfig({
  test: {
    include: ['apps/**/test/**/*.test.{ts,tsx}', 'packages/**/test/**/*.test.{ts,tsx}'],
    exclude: [
      '**/node_modules/**',
      '**/dist/**',
      '**/coverage/**',
      'apps/**/test/integration/**',
      'apps/**/test/contract/**',
      'apps/**/test/e2e/**',
      'packages/**/test/integration/**',
      'packages/**/test/contract/**',
      'packages/**/test/e2e/**',
    ],
    environmentMatchGlobs: [
      ['packages/ui/**', 'jsdom'],
      ['apps/**', 'node'],
      ['packages/**', 'node'],
    ],
    environment: 'node',
    passWithNoTests: true,
  },
});
