export default {
  testDir: "./tests",
  testMatch: /.*\.spec\.ts$/,
  workers: 1,
  timeout: 120_000,
  outputDir: "../reports/video/api-gateway",
  preserveOutput: "always",
  use: { trace: "on" },
  reporter: [
    ["list"],
    ["html", { outputFolder: "../reports/api/api-gateway", open: "never" }],
  ],
};
