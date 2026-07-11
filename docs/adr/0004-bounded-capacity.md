# ADR-0004: Executor process runs bounded concurrent containers

- **Status**: Accepted
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

## Context

The Executor must run enough agent containers to be useful as a local worker
while preserving predictable resource usage. It could plausibly be designed to:

- Run multiple containers concurrently with a configured capacity limit.
- Run one container and let that container handle concurrent tasks.
- Run one unbounded container per queued task.
- Run one container per Executor process for the lifetime of the Executor.

Each model has different implications for: resource isolation, observability,
restart semantics, scheduling complexity, and operational burden.

## Decision

The Executor runs **multiple OpenHands containers concurrently up to configured
capacity**. Capacity is controlled by properties such as `EXECUTOR_MAX_CONTAINERS`
and the configured host port range. Each accepted FlowAI task gets one OpenHands
container labeled with `flowai.executor_id`, `flowai.runtime`, and `flowai.task_id`.

## Rationale

- **Useful local worker capacity**: v0001 can run more than one task without
  requiring multiple Executor processes.
- **Bounded resource usage**: `EXECUTOR_MAX_CONTAINERS` and port-range validation
  prevent accidental unbounded Docker growth.
- **Per-task isolation**: each task gets its own OpenHands container and
  lifecycle events.
- **Simple restart semantics**: Executor death ends owned containers. No
  re-attach across Executor restarts.
- **Future runtime isolation**: if the platform needs Claude Code support, it
  spins up a separate Executor process (probably a different image or config),
  not a multi-runtime abstraction inside one process. This keeps each
  Executor's blast radius small.
- **Global scheduling remains Router's problem**: Executor enforces local
  capacity only; Router decides which tasks exist and which tags they carry.

## Consequences

Positive:

- v0001 can exercise real bounded concurrency and per-task container lifecycle.
- Operational mental model remains one container per task — well-understood.
- Different agent runtimes can have different Executor implementations without
  sharing process boundaries.
- Restart semantics are simple: process death = all owned containers die.

Negative:

- **More bookkeeping**: Executor must track slots, task-to-container mappings,
  host ports, and per-container terminal state.
- **Container failure is per-task**: if one OpenHands container crashes, only
  that task fails; remaining containers continue. This is more complex than
  failing the whole Executor.
- **Port-range configuration matters**: bad ranges can prevent capacity from
  being usable; validation is required at startup.

## Alternatives considered

- **Exactly one container per Executor**: Simpler, but insufficient for the
  desired v0001 worker capacity.
- **One long-lived container handling concurrent tasks**: Avoids port/slot
  bookkeeping but pushes concurrency into OpenHands and weakens task isolation.
- **Unbounded one container per task**: Maximally flexible but unsafe for local
  resources.
- **Multi-runtime Executor (one process, multiple containers of different
  types)**: Adds runtime-routing logic inside the Executor; blurs the "Executor
  = one workload" model. Rejected; use separate Executors per runtime instead.

## References

- v0001-executor-docker proposal.md "What Changes" and "Non-Goals".
- AGENTS.md "Connection matrix" — Executor row is generic; per-runtime
  Executors are an implementation detail.
- v0001-executor-docker design.md "Capacity and scheduling" section.