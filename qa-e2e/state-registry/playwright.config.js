module.exports = {
  testDir: "./tests",
  testMatch: /.*\.spec\.ts$/,
  workers: 1,
  timeout: 120_000,
  outputDir: "../reports/video/state-registry",
  preserveOutput: "always",
  use: {
    trace: "on",
    video: "on",
  },
  reporter: [
    ["list"],
    ["html", { outputFolder: "../reports/api/state-registry", open: "never" }],
  ],
};
