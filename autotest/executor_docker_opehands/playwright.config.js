module.exports = {
  testDir: './tests',
  testMatch: /.*\.spec\.ts$/,
  // workers:1 keeps shared subprocess state isolated. The mocked task
  // server and the executor_docker_opehands each bind to fixed ports,
  // so parallel workers would collide on a single host. With workers:1,
  // each spec runs to completion before the next starts; the dedicated
  // alt ports (MOCKED_BIND_ALT / EXECUTOR_BIND_ALT, and the per-spec
  // container port ranges) further prevent bleed across runs.
  workers: 1,
  timeout: 60_000,
  reporter: [['list'], ['html', { outputFolder: 'playwright-report', open: 'never' }]],
};
