# mocked-task-server serves /v1/livez

- **File**: [`autotest/docker-executor/tests/executor.spec.ts:33`](../../docker-executor/tests/executor.spec.ts)
- **Style**: Cross-service Playwright (real binaries, no Docker required)

## Setup

Spawn the real `mocked-task-server` binary on port 18080 and the real
`docker-executor` binary on port 18020 (default ports from
`autotest/docker-executor/tests/helpers.ts`).

## Action

`GET /v1/livez` against the mocked task server, via Playwright's
`request` fixture rooted at the mocked server's host.

## Assert

- Response status is 200
- `body.status === "ok"`

## Purpose

Confirms the executor's helper code can probe the mocked task server
and the mocked task server is alive. This is the simplest possible
end-to-end smoke test — if it fails, the test setup itself is broken
(no point running the rest).
