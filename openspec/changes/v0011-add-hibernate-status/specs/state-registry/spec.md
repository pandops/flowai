## ADDED Requirements

### Requirement: State Registry authorizes hibernate and later-run actions

State Registry SHALL accept hibernate and later-run actions only from an authenticated same-team operator or an authenticated system administrator. Listener and Executor identities SHALL NOT originate either action. Authorization SHALL occur before task disclosure or mutation. Every accepted or rejected action SHALL produce a request-attributed audit record containing actor identity and kind, task and team identifiers when authorized, action, idempotency key, outcome, and timestamps without session contents, secrets, or storage locators.

#### Scenario: Same-team operator hibernates running work

- **WHEN** an authenticated operator requests hibernate for an eligible task owned by the operator's immutable team
- **THEN** State Registry accepts an idempotent hibernate control and audits the operator action

#### Scenario: Administrator hibernates work across teams

- **WHEN** an authenticated system administrator requests hibernate for an eligible task
- **THEN** State Registry accepts it through the admin-authorized path and audits the administrator and affected team

#### Scenario: Foreign operator is denied

- **WHEN** an operator requests hibernate or later run for a task owned by another team
- **THEN** State Registry returns the same non-revealing response used for an unknown task and creates no control, continuation task, or archive metadata

### Requirement: State Registry projects hibernate only after durable session storage

For an accepted hibernate control, State Registry SHALL keep the control `in_progress` while the assigned Executor stops execution and uploads the OpenHands session archive. Archive bytes SHALL use a private configured durable-storage adapter and SHALL NOT be stored in the Registry database. The Registry SHALL project terminal `hibernate` only after the complete archive plus its versioned manifest, byte count, content digest, conversation identifier, and locator commit successfully. It SHALL project terminal `failed` if the assigned Executor reports that snapshot or storage cannot complete. Partial uploads SHALL NOT be recoverable and SHALL be scheduled for deletion.

#### Scenario: Running session hibernates successfully

- **WHEN** the assigned Executor stops execution and uploads a complete valid archive for an accepted hibernate control
- **THEN** State Registry atomically commits archive metadata, projects the original task to `hibernate`, completes the control, and exposes no storage locator

#### Scenario: Hibernate storage fails

- **WHEN** snapshot creation, upload, configured limits, or digest verification fails after execution stops
- **THEN** State Registry projects the original task to `failed`, exposes no recoverable archive, and schedules partial data for deletion

#### Scenario: Hibernate retry is idempotent

- **WHEN** the same actor retries hibernate with the same idempotency key
- **THEN** State Registry returns the original control and outcome without another snapshot or state transition

### Requirement: State Registry creates continuation tasks from hibernated sessions

State Registry SHALL accept a later-run request only for a task in `hibernate`. The request SHALL contain a non-empty new user query and an idempotency key. The Registry SHALL create exactly one new `pending` continuation task with immutable `hibernated_from_task_id`, the same immutable `team_id`, and the source task's task type, source-system context, environment reference, required tag, and image-resolution inputs. The original task SHALL remain `hibernate`; its assignment and history SHALL remain unchanged.

#### Scenario: User starts a later run

- **WHEN** an authorized operator submits a new query and idempotency key for a hibernated task
- **THEN** State Registry creates one pending continuation task linked by `hibernated_from_task_id` and leaves the source task in `hibernate`

#### Scenario: Later-run request is retried

- **WHEN** the same actor repeats the later-run request with the same hibernated task and idempotency key
- **THEN** State Registry returns the original continuation task without creating another task

#### Scenario: Non-hibernated task cannot start a later run

- **WHEN** an actor requests a hibernated-session later run for a task in any state other than `hibernate`
- **THEN** State Registry rejects the request without creating a continuation task

### Requirement: State Registry serves hibernated archives only to assigned continuation Executors

After a continuation task is claimed, State Registry SHALL issue the assigned Executor a short-lived opaque archive handle bound to the continuation task, hibernated source task, team, Executor, and command identifier. The Registry SHALL stream the archive only after verifying every binding and the committed digest. Gateway, operator, listener, and other Executor paths SHALL NOT retrieve archive bytes or backing locators.

#### Scenario: Assigned Executor downloads a hibernated archive

- **WHEN** the continuation task's assigned Executor presents a valid bound handle
- **THEN** State Registry streams the verified archive and records a content-free audit event without exposing the storage locator

#### Scenario: Archive handle is replayed

- **WHEN** another identity presents the handle or a canonical binding differs
- **THEN** State Registry fails closed with a non-revealing response and returns no archive bytes
