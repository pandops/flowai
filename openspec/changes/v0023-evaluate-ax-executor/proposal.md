# Change: Evaluate Google AX as an Executor backend

## Status

Deferred backlog proposal, recorded at the user's request on 2026-09-23.
Classification: development (future experiment). Implementation is not authorized
or started; adoption of AX has not been decided. A minimal proposed spec delta
records evaluation boundaries. Detailed design, full contract deltas, proposed
ADRs/diagrams, E2E definitions, behaviour matrix, and tasks
are intentionally deferred until this change is resumed. This proposal is not
implementation-ready.

## Why

Evaluate whether [Google AX](https://github.com/google/ax) can take over the
execution-environment lifecycle currently handled directly by FlowAI Executors.
The potential value is managed workspaces, sandbox networking, and suspend/resume.
The experiment must establish whether these benefits justify another control
plane and its operational dependencies, while preserving FlowAI ownership and
task-lifecycle guarantees.

## What Changes

- Explore a separate experimental Executor adapter that submits claimed work to
  AX, initially retaining OpenHands as the agent inside an AX-compatible image.
- Keep State Registry authoritative for intake, deduplication, FIFO discovery
  and atomic claims, immutable task/team/command ownership, canonical events,
  controls, and scoped environment/secret access.
- Preserve Registry-selected `resolved_image` without substitution. Compatible
  images must be explicitly configured through existing Registry image inputs.
- Investigate an OpenHands REST/WS bridge for conversation startup, completion,
  logs, and controls rather than equating sandbox readiness or process exit with
  task success.
- Evaluate deployment, recovery, cleanup, capacity enforcement, tenant isolation,
  and operational cost before deciding whether AX should replace any existing
  execution path. Keep the current Executors available during evaluation.

## Capabilities

### New Capabilities

- `ax-executor`: Candidate integration between the FlowAI Executor contract and
  an AX-managed OpenHands execution environment; subject to feasibility review.

### Modified Capabilities

None at backlog stage. On resumption, identify explicit deltas to `executor`
and any affected deployment or lifecycle contracts before implementation.

## Questions to Resolve

The upstream README and runner documentation consulted during the initial
discussion describe a Kubernetes/Agent Substrate deployment with Redis, an
unstable pre-release contract, a fixed `/usr/local/bin/ax-task-runner` executable,
and HTTP health/readiness endpoints. They also describe workspace restoration
into new processes and no command-exit reporting to the AX control plane.
Recheck these observations against a pinned upstream revision before design:
[README](https://github.com/google/ax/blob/main/README.md),
[runner contract](https://github.com/google/ax/blob/main/docs/runner.md).

- Can OpenHands conversations survive suspend/resume with their identity,
  history, credentials, and result semantics intact?
- How will the adapter reconcile an accepted AX submission after a timeout or
  restart without launching duplicate work? Define stable task/command mapping.
- How will ordered, idempotent FlowAI events and controls survive stream loss,
  adapter restart, cancellation, and terminal cleanup?
- Where can AX persist task environment values and snapshots, and how can
  FlowAI's secret-handling and team-isolation guarantees be preserved?
- Which concrete runtime/tool wire identifier and service directory satisfy
  FlowAI naming rules? AX is an orchestrator; do not assume it is the runtime
  token merely because it is the integration backend.

## Dependencies and Boundaries

- Reconcile with `v0010-add-paused-termination-status` and
  `v0011-add-hibernate-status` before defining pause/resume mappings; this
  proposal introduces no new canonical lifecycle states.
- Recheck interactions with `v0019-add-dynamic-task-continuation` and
  `v0020-add-openhands-goal-execution` when specifying conversation recovery.
- Keep Web UI → API Gateway → State Registry topology. AX is proposed only as
  execution infrastructure behind the adapter, not a new operator backend or
  canonical FlowAI task broker.

## Impact

Potential future work includes a self-contained service under `executor/` with
its own production Containerfile, an AX-compatible agent image, pinned AX and
Agent Substrate deployments, and a cross-service suite under `qa-e2e/`.
No runtime code, baseline specs, accepted ADRs, or deployment configuration is
changed by recording this backlog proposal.

## Evaluation Exit Criteria

Before any adoption decision, define and execute container-based E2E tests for
claim → launch → OpenHands result → Registry terminal event; rejected claims;
submission retry/reconciliation; adapter and runtime failures; stop/cleanup;
team isolation; image fidelity; and conversation restoration after suspension.
Record observed limitations and compare operational cost with the existing
Executors. A negative feasibility result is a valid outcome of this evaluation.

## Out of Scope

- Immediate migration, removal of existing Executors, or production deployment.
- Replacing State Registry, weakening tenancy/FIFO guarantees, or moving
  canonical lifecycle decisions into AX.
- Claiming transparent OpenHands resume or drop-in image compatibility without
  evidence from the future experiment.
