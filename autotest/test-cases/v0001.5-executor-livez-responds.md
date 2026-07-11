# docker-executor livez responds on the configured bind

- **File**: [`autotest/docker-executor/tests/executor.spec.ts:60`](../../docker-executor/tests/executor.spec.ts)
- **Style**: Cross-service Playwright (real binaries, no Docker required)

## Setup

Both services spawned.

## Action

`GET /v1/livez` against the docker-executor (port 18020).

## Assert

- Response status is 200
- `body.status === "ok"`
- `body.executor_id` is a non-empty string (the auto-generated UUID)

## Purpose

Confirms the executor's platform health API responds on the configured
bind address and exposes the auto-generated `executor_id`. Operators
discovering the executor's ID via `/v1/livez` is the entry point for
debugging and audit ("which executor handled this task?").
