module.exports = {
  testDir: "./tests",
  testMatch: /.*\.spec\.ts$/,
  workers: 1,
  timeout: 120_000,
  // v0006 supersedes definition-addressed environment opening and removes
  // task-owned environment definitions. Their former v0002 acceptance tests
  // are retired from the executable current-state suite; v0006.12, .15,
  // .26, .27, .32 and .33 cover the replacement contract end to end.
  grepInvert: /\bv0002\.(?:8|11|19|40|45|46|47|49|78)\b/,
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
