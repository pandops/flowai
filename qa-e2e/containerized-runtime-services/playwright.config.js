const { defineConfig } = require("@playwright/test");

module.exports = defineConfig({
  testDir: "./tests",
  timeout: 180_000,
  expect: { timeout: 15_000 },
  forbidOnly: true,
  retries: 0,
  workers: 1,
  reporter: "list",
});
