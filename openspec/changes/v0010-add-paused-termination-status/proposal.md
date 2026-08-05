## Why

FlowAI currently collapses OpenHands pause behavior into failure and does not
distinguish a resumable operator pause from an immediate permanent interruption.
Operators and system administrators need both controls with deterministic,
bounded runtime cleanup.

## What Changes

- Add resumable task state `paused`, entered only by an authorized operator or
  system-administrator pause request.
- Add terminal task state `interrupted`, entered only by an authorized operator
  or system-administrator interrupt request.
- Add an explicit `continue` action that resumes a paused task before its cleanup
  deadline and projects it back to `running`.
- Start an immutable configured cleanup wait on pause; if no continue request is
  accepted before its deadline, clean up the runtime.
- Implement interrupt through the same pause-and-cleanup path with cleanup wait
  fixed to zero, causing immediate runtime cleanup and no continue opportunity.
- Add authorization, audit, idempotency, restart reconciliation, API, and E2E
  coverage for pause, continue, and interrupt.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `state-registry`: Authorize and project pause, continue, and interrupt controls,
  including immutable cleanup deadlines and the new `paused`/`interrupted` states.
- `executor`: Pause and resume agent work, enforce cleanup deadlines, and implement
  immediate interruption as pause with zero cleanup wait.

## Impact

State Registry lifecycle and control contracts, OpenAPI enums, audit and
authorization, Docker Executor OpenHands pause/resume behavior, configuration,
the planned K8s Executor contract, Web UI controls, and integration/E2E tests
are affected.
