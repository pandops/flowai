# Change: v0002-state-registry

## Why

FlowAI needs a historical record service after Docker execution exists, so task records, events, audit entries, and control requests are no longer local-only observations.

## What Changes

- Add State Registry as the historical platform-state service.
- Persist task records, task events, audit trail, and operator control-request records.
- Expose direct APIs usable before Web UI/API Gateway exist.

## Impact

- Docker Executor can write history and read control requests in a later integration step.
- Router and Web UI changes have a stable historical-state target.

## Non-Goals

- Store environment variables or secret values.
- Queue, dispatch, or schedule tasks.
- Implement Env Registry, Router, Web UI, or auth.
