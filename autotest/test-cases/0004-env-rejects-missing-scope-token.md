# mocked-task-server rejects env lookup without scope_token

- **File**: [`autotest/docker-executor/tests/executor.spec.ts:55`](../../docker-executor/tests/executor.spec.ts)
- **Style**: Cross-service Playwright (real binaries, no Docker required)

## Setup

Both services spawned.

## Action

`GET /v1/env?executor_id=test&routing_target=openhands` — note the
**missing** `scope_token` query parameter.

## Assert

- Response status is 400
- Response body contains the text `"missing scope_token"`

## Purpose

Confirms the env endpoint validates required query parameters and
returns 400 (not 200, not 500) when `scope_token` is missing. A
malformed client request should not cause a server-side panic or
return stale data.
