# docker-executor registers with mocked State Registry

- **File**: [`autotest/docker-executor/tests/executor.spec.ts:85`](../../docker-executor/tests/executor.spec.ts)
- **Style**: Cross-service Playwright (real binaries, no Docker required)

## Setup

Both services spawned. The executor has had time to call its startup
registration against the mocked State Registry.

## Action

1. `GET /v1/livez` against the docker-executor to discover its
   `executor_id`.
2. `PUT /v1/executors/{executor_id}` against the mocked task server
   with a registration body (`executor_id`, `executor_type:
   "docker-openhands"`, `routing_target: "openhands"`, `capacity: 2`,
   metadata).

## Assert

- The PUT response is 200 or 201 (idempotent re-registration)

## Purpose

Confirms the executor's startup registration flow works end-to-end
against the mocked task server. Since the executor already registered
at startup (verified separately by its own unit tests), this test
re-registers the same `executor_id` and confirms the wire surface
round-trips correctly. If the executor's registration POST failed, the
State Registry would never know the executor exists and would never
route tasks to it.
