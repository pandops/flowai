## ADDED Requirements

### Requirement: State Registry validates immutable Goal configuration

State Registry SHALL allow an authenticated system administrator to register a
task type with immutable `execution_mode = single | openhands_goal`. Goal mode
SHALL require a positive `goal_max_iterations_limit`. Ingestion for such a task
type SHALL require a non-empty bounded `goal_objective` and may request a
positive `goal_max_iterations` not exceeding that limit; omission SHALL use the
limit. Registry SHALL persist the effective values immutably and return them
only through authorized same-team detail and successful assigned claim paths.

#### Scenario: Valid Goal task is ingested

- **WHEN** an authorized listener submits a bounded objective and iteration request within its Goal task-type limit
- **THEN** State Registry persists one pending task with immutable Goal mode, objective, and effective cap before acknowledgement

#### Scenario: Iteration request exceeds policy

- **WHEN** ingestion requests an iteration count above the task-type limit
- **THEN** State Registry rejects the task before persistence and reveals no foreign metadata

#### Scenario: Discovery omits objective

- **WHEN** any Executor discovers a pending Goal task
- **THEN** its summary identifies eligibility without exposing the Goal objective or verdict content

### Requirement: State Registry persists ordered Goal progress

State Registry SHALL accept normalized Goal events only from the authenticated
Executor assigned to the parent task. Each event SHALL carry a stable provider
event ID, status from `running | complete | capped | interrupted`, positive
iteration not exceeding the immutable cap, occurred time, and optional bounded
content-safe verdict summary. Appends SHALL be idempotent, legally ordered, and
atomically update the Goal projection and audit. Same-team reads and streams
SHALL filter ownership before replay, cursors, counts, or serialization.

#### Scenario: Assigned Executor mirrors progress

- **WHEN** the assigned Executor appends a new valid native Goal update
- **THEN** State Registry commits it once, advances the projection, and publishes one team-bound goal frame

#### Scenario: Duplicate provider event is retried

- **WHEN** the same provider event ID and body are submitted again
- **THEN** State Registry returns the original result without another event, projection update, or frame

#### Scenario: Unauthorized progress append

- **WHEN** a browser, listener, unassigned Executor, or foreign-team Executor appends Goal progress
- **THEN** State Registry applies its existing non-revealing denial and mutates nothing

#### Scenario: Invalid ordering is rejected

- **WHEN** an event regresses iteration, follows a terminal Goal status, or exceeds the immutable cap
- **THEN** State Registry rejects it without changing Goal or task projections

### Requirement: Goal content remains scoped and content-safe

State Registry SHALL omit Goal objectives and raw judge output from discovery,
admin task summaries, logs, audit, errors, and aggregates. Same-team task detail
MAY expose the objective and bounded normalized verdict summary. Registry SHALL
NOT store or expose hidden reasoning, judge credentials, full judge prompts, or
raw OpenHands conversation events.

#### Scenario: Same-team operator reads Goal status

- **WHEN** a same-team operator opens Goal task detail
- **THEN** State Registry returns objective-safe Goal configuration and latest normalized progress without credentials, hidden reasoning, or raw conversation events

#### Scenario: Foreign operator probes Goal task

- **WHEN** an operator reads another team's Goal task or stream
- **THEN** State Registry returns the normal non-revealing result with no objective, verdict, count, cursor, timing, or existence signal
