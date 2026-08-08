// The v0005 K8s Executor Playwright suite bootstraps a fresh,
// uniquely-named `k3d` cluster per run, deploys the
// `executor_k8s_openhands` binary as a single-replica Deployment
// with a pinned ServiceAccount and PVC, and drives the State
// Registry through its documented REST surface. Each test owns
// only its `task_id`-scoped state and tears down via the global
// suite-level cleanup so a single failing test does not leak a
// partially-initialised cluster.

const { defineConfig, devices } = require("@playwright/test");

module.exports = defineConfig({
  testDir: "./tests",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: 1,
  reporter: [
    ["list"],
    [
      "html",
      { open: "never", outputFolder: "../reports/executor_k8s_openhands" },
    ],
  ],
  use: {
    trace: "retain-on-failure",
    video: "retain-on-failure",
    screenshot: "only-on-failure",
    baseURL: process.env.EXECUTOR_K8S_BASE_URL || "http://127.0.0.1:18443",
  },
  projects: [
    {
      name: "k8s",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
