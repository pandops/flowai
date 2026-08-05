## Context

OpenHands can pause and resume a live conversation. FlowAI needs two user-facing
operations: pause temporarily while retaining the runtime for a configured
period, and interrupt permanently with immediate cleanup. Both may be invoked by
an operator authorized for the task's team or by a system administrator.

## Goals / Non-Goals

**Goals:**

- Model `paused` as resumable and `interrupted` as terminal.
- Resume the same assigned task and OpenHands session when continue arrives in
  time.
- Use one Executor mechanism, parameterized by cleanup wait, for both operations.
- Preserve authorization, idempotency, audit, and restart-safe deadlines.

**Non-Goals:**

- Allow listeners or Executors to originate pause, continue, or interrupt.
- Reassign a paused task to another Executor.
- Persist or recover a paused session after its cleanup deadline.

## Decisions

1. The lifecycle expands to `running -> paused -> running` for pause/continue and
   `running | paused -> interrupted` for permanent interruption. `paused` is
   non-terminal; `interrupted` is terminal.
2. Pause and interrupt are the same Executor operation with different effective
   cleanup waits. Pause uses the positive configured interval. Interrupt uses
   exactly zero, so cleanup begins immediately after the best-effort OpenHands
   pause request and the task becomes `interrupted`.
3. State Registry calculates and persists the absolute cleanup deadline when it
   accepts the control. Retries and Executor restarts reuse it and cannot extend
   retention.
4. Continue is accepted only while the canonical state is `paused`, the original
   Executor remains assigned, the runtime still exists, and server time is before
   the cleanup deadline. Acceptance cancels pending cleanup, invokes OpenHands
   resume, and returns the task to `running` after acknowledgement.
5. If a pause deadline expires first, the Executor cleans up the runtime. The
   task remains `paused` but is no longer directly continuable and exposes that
   cleanup outcome. Hibernate and later recovery are independent v0011 actions;
   pause does not implicitly preserve session files.
6. Team operators are authorized only for same-team tasks. A system administrator
   may act across teams through an authenticated admin route. Both paths produce
   team-scoped, request-attributed audit records.

## Risks / Trade-offs

- [Continue races cleanup] → Registry serializes continue against deadline and
  cleanup acknowledgement; exactly one outcome wins.
- [Resume acknowledgement fails] → Keep canonical state `paused`, retain the
  original deadline, and do not extend the cleanup window.
- [Executor restart loses timer] → Reconcile the Registry-persisted absolute
  deadline and current control state on restart.
- [Interrupt waits on OpenHands] → Its zero wait makes pause best-effort; runtime
  cleanup and terminal `interrupted` are not delayed by acknowledgement.

## Migration Plan

1. Deploy Registry schema, enums, controls, authorization, and deadline support.
2. Deploy Executors supporting pause/resume and deadline reconciliation.
3. Enable operator/admin pause and continue, then enable zero-wait interrupt.
4. Roll back UI/control exposure first; retain additive states and audit records.

## Open Questions

None.
