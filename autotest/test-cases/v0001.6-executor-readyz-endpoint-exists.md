# docker-executor readyz endpoint exists

- **File**: [`autotest/docker-executor/tests/executor.spec.ts:69`](../../docker-executor/tests/executor.spec.ts)
- **Style**: Cross-service Playwright (real binaries, no Docker required)

## Setup

Both services spawned.

## Action

`GET /v1/readyz` against the docker-executor.

## Assert

- Response status is **200 or 503** (both are valid readiness states)

## Purpose

Confirms the `readyz` endpoint exists and is reachable. The specific
status depends on whether the executor has had a chance to register
with the State Registry yet (200) or is still in startup (503). Both
responses are valid — the assertion is "endpoint exists and returns a
sane status code".
