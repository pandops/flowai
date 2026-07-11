# mocked-task-server router task list returns empty when no tasks are seeded

- **File**: [`autotest/docker-executor/tests/executor.spec.ts:40`](../../docker-executor/tests/executor.spec.ts)
- **Style**: Cross-service Playwright (real binaries, no Docker required)

## Setup

Both services spawned. No tasks are seeded into the mocked task server
(the test-mode seed endpoint is not called).

## Action

`GET /v1/tasks?filter=openhands` against the mocked task server.

## Assert

- Response status is 200
- `body.tasks` is an empty array

## Purpose

Confirms the router task-list endpoint returns the expected empty-list
shape when no tasks match the filter. Catches wire-format regressions
in the response body and verifies the executor's mock-poll loop will
not crash on an empty list (a common defect when JSON unmarshaling
treats `null` and `[]` differently).
