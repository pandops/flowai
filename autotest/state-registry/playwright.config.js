module.exports = {
  testDir: './tests',
  testMatch: /.*\.spec\.ts$/,
  workers: 1,
  timeout: 120_000,
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
};