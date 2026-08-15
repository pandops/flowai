module.exports = {
  testDir: "./tests",
  testMatch: /.*\.spec\.ts$/,
  workers: 1,
  timeout: 30_000,
  outputDir: "../reports/video/web-ui",
  preserveOutput: "always",
  use: {
    trace: "on",
    video: "on",
  },
  reporter: [
    ["list"],
    ["html", { outputFolder: "../reports/ui/web-ui", open: "never" }],
  ],
};
