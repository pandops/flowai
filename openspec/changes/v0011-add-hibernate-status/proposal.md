## Why

Users need to stop an active agent immediately, release its runtime resources,
and preserve its OpenHands session for a later run.

## What Changes

- Add independent operator/admin action and task state `hibernate`.
- Stop agent execution immediately when hibernate is accepted, snapshot the
  OpenHands recovery files, store them in separate durable storage, and release
  the container or Pod after storage succeeds or fails.
- Expose `hibernate` only after the complete archive is durably committed and
  integrity metadata is recorded; a failed snapshot/storage operation becomes
  terminal `failed`, never a recoverable hibernate.
- Add an authorized later-run request carrying a new user query; it creates a
  pending continuation task linked to the hibernated task.
- Restore the verified OpenHands session into the continuation task's new
  runtime before submitting the new query.

## Capabilities

### New Capabilities

- `session-recovery`: Independent hibernate and later-run recovery of OpenHands
  sessions without retaining compute resources.

### Modified Capabilities

- `state-registry`: Authorize hibernate/later-run requests, project `hibernate`,
  own archive metadata, and preserve tenancy and immutable assignments.
- `executor`: Stop work, snapshot and release on hibernate, then restore a
  verified session for a newly claimed continuation task.

## Impact

State Registry lifecycle, controls, storage adapter, schema, APIs, audit and
authorization; Docker/K8s Executor snapshot/restore behavior; API Gateway
allowlists; Web UI actions; configuration; and recovery E2E tests are affected.
