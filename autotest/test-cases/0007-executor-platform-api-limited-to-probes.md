# docker-executor platform API is limited to health probes only

- **File**: [`autotest/docker-executor/tests/executor.spec.ts:74`](../../docker-executor/tests/executor.spec.ts)
- **Style**: Cross-service Playwright (real binaries, no Docker required)

## Setup

Both services spawned.

## Action

For each of the following paths, send `POST` to the docker-executor:

- `/v1/tasks`
- `/v1/tasks/abc-123/events`
- `/v1/executors`

## Assert

Every response is `404` or `405`.

## Purpose

Enforces the design rule that the executor exposes **only** `/v1/livez`
and `/v1/readyz` on its platform health API. Task-control endpoints
(`/v1/tasks`, `/v1/executors`) belong to the Router / State Registry
surfaces, **never** to the Executor. A `404` on these paths confirms
the surface is restricted — the executor is not a backdoor into task
control or a proxy for OpenHands. This is the design contract from
`AGENTS.md § Connection matrix`.
