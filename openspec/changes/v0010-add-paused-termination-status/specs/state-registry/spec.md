## ADDED Requirements

### Requirement: State Registry authorizes operator and administrator execution controls

State Registry SHALL accept `pause`, `continue`, and `interrupt` only from an authenticated operator authorized for the task's immutable team or from an authenticated system administrator. Listener and Executor identities SHALL NOT originate these controls. The Registry SHALL authorize before reading or mutating the task and SHALL append an audit record carrying the actor identity, actor kind, task and team identifiers, action, idempotency key, request identifier, outcome, and timestamps without task payload or session-file contents.

#### Scenario: Same-team operator pauses a task

- **WHEN** an authenticated operator requests pause for a running task owned by the operator's immutable team
- **THEN** State Registry accepts the control, attributes it to that operator, and appends a team-scoped audit record

#### Scenario: System administrator interrupts a task

- **WHEN** an authenticated system administrator requests interrupt for a running or paused task
- **THEN** State Registry accepts the control through the admin-authorized path and audits the administrator identity and affected team

#### Scenario: Foreign operator is denied before task disclosure

- **WHEN** an operator requests a control for a task owned by another team
- **THEN** State Registry returns the same non-revealing response used for an unknown task and creates no control or domain mutation

#### Scenario: Listener or Executor originates a user control

- **WHEN** a listener or Executor identity requests pause, continue, or interrupt
- **THEN** State Registry rejects the request without creating a control or changing task state

### Requirement: State Registry projects resumable paused and terminal interrupted states

For an accepted `pause` control on a `running` task, State Registry SHALL calculate and persist an immutable absolute `cleanup_deadline` using the configured positive cleanup wait and SHALL project `paused` only after the assigned Executor reports OpenHands pause acknowledgement. `paused` SHALL be non-terminal but SHALL remain assigned to the original Executor and excluded from discovery and claim. For an accepted `interrupt` control on a `running` or `paused` task, the effective cleanup wait SHALL be exactly zero and the Registry SHALL project terminal `interrupted`; `interrupted` SHALL reject claim, continue, lifecycle writes, controls, scope tokens, and environment opens except idempotent retries.

#### Scenario: Pause creates a resumable window

- **WHEN** an authorized pause is accepted for a running task and OpenHands acknowledges pause
- **THEN** State Registry projects `paused`, preserves the original assignment, and exposes the immutable future cleanup deadline to authorized readers and the assigned Executor

#### Scenario: Interrupt uses zero cleanup wait

- **WHEN** an authorized interrupt is accepted for a running or paused task
- **THEN** State Registry records effective cleanup wait zero, projects terminal `interrupted`, and exposes a cleanup deadline equal to the accepted server time

#### Scenario: Control retry does not extend cleanup

- **WHEN** an accepted pause or interrupt is retried with the same idempotency key
- **THEN** State Registry returns the original control and cleanup deadline without creating a duplicate or extending retention

#### Scenario: Interrupted task cannot continue

- **WHEN** an operator or administrator requests continue for an interrupted task
- **THEN** State Registry rejects the request without changing state or assignment

### Requirement: State Registry continues paused work only before cleanup wins

State Registry SHALL accept `continue` only for a task canonically in `paused`, still assigned to its original Executor, with a live runtime reported by that Executor, and with server time strictly before `cleanup_deadline`. Continue acceptance and cleanup completion SHALL be serialized so exactly one wins. After the assigned Executor acknowledges OpenHands resume, the Registry SHALL project the same task back to `running`, cancel pending cleanup, and retain its immutable assignment and command identifier. A failed resume SHALL leave the task `paused` with its original deadline.

#### Scenario: User continues before deadline

- **WHEN** an authorized user requests continue while the task is paused, its runtime is live, and server time is before cleanup deadline
- **THEN** State Registry accepts continue and, after Executor resume acknowledgement, projects the same assigned task back to `running`

#### Scenario: Continue arrives after cleanup

- **WHEN** a continue request races with cleanup and cleanup completion commits first
- **THEN** State Registry rejects continue without reviving the runtime, extending the deadline, or changing assignment

#### Scenario: Resume acknowledgement fails

- **WHEN** OpenHands resume is rejected or cannot be acknowledged
- **THEN** State Registry leaves the task `paused` with the original cleanup deadline and records the failed control outcome
