## 1. Registry Lifecycle and Authorization

- [ ] 1.1 Add RED tests for operator/admin authorization, foreign-team non-disclosure, and listener/Executor denial for pause, continue, and interrupt.
- [ ] 1.2 Add RED lifecycle tests for `running -> paused -> running`, `running|paused -> interrupted`, immutable assignment, and terminal interrupted denials.
- [ ] 1.3 Extend persistence, OpenAPI enums, controls, audit, projections, and idempotency with `paused`, `interrupted`, `continue`, and immutable cleanup deadlines.

## 2. Pause and Continue

- [ ] 2.1 Add RED Docker Executor tests for acknowledged pause, retained runtime, same-session continue, failed resume, deadline race, and restart reconciliation.
- [ ] 2.2 Implement configured positive pause wait, OpenHands pause/resume calls, cleanup cancellation, same-session continuation, and capacity accounting.
- [ ] 2.3 Validate cleanup configuration and ensure retries/restarts never extend the Registry-issued absolute deadline.

## 3. Immediate Interrupt

- [ ] 3.1 Add RED tests proving interrupt reuses pause cleanup with wait zero for both running and paused tasks.
- [ ] 3.2 Implement best-effort pause plus immediate runtime removal, capacity release, and terminal `interrupted` acknowledgement without waiting for OpenHands.
- [ ] 3.3 Update active K8s Executor artifacts so Pods follow identical pause, continue, interrupt, and deadline behavior.

## 4. End-to-End Verification

- [ ] 4.1 Add E2E coverage for operator pause then continue before deadline, proving the same OpenHands session resumes.
- [ ] 4.2 Add E2E coverage for pause expiry and for operator/admin interrupt with immediate cleanup; verify foreign users and non-user identities are denied.
- [ ] 4.3 Run affected Go, OpenAPI, Playwright, security, and strict full OpenSpec validation.
