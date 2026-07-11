module.exports = {
  testDir: './tests',
  testMatch: /.*\.spec\.ts$/,
  timeout: 30000,
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
};
