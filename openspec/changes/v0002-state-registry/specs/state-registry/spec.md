## ADDED Requirements

### Requirement: State Registry is the canonical team-scoped task source

The State Registry SHALL be the durable source of truth for every FlowAI task, its immutable owning `team_id`, current state, assignment, event history, audit history, and control requests. Every task SHALL belong to exactly one team for its lifetime. Event listeners such as Jira integrations and webhook receivers SHALL authenticate with a service identity bound to one authorized team and SHALL supply that team's immutable `team_id` with every task submission. The State Registry SHALL verify the submitted `team_id` against the authenticated listener authorization before persisting or acknowledging the task.

#### Scenario: Authorized listener stores a new team task

- **WHEN** a listener authenticated for team `team-a` submits a previously unseen external task with `team_id = team-a`
- **THEN** the State Registry persists the complete canonical task record with immutable `team_id = team-a` and appends the immutable `created` event in the same transaction before acknowledging the source event

#### Scenario: Listener submits an unauthorized team

- **WHEN** a listener authenticated for `team-a` submits a task with `team_id = team-b`
- **THEN** the State Registry rejects the request without creating a task, appending an event, or acknowledging successful ingestion

#### Scenario: Task becomes discoverable only after commit

- **WHEN** an authorized listener is persisting a new task
- **THEN** no Executor can discover or request approval for that task until the persistence transaction commits

### Requirement: State Registry deduplicates listener tasks within a team

Each listener submission SHALL include immutable `team_id`, a stable source identifier, and a stable source task identifier. The State Registry SHALL enforce uniqueness on `(team_id, source, source_task_id)` and SHALL return the existing canonical task when the same team-scoped external task is received again without creating another task or resetting its state or event log.

#### Scenario: Listener repeats a source task in one team

- **WHEN** an authorized listener submits the same `team_id`, source identifier, and source task identifier more than once
- **THEN** the State Registry returns the existing canonical task, appends no new `created` event, and stores only one task record in that team

#### Scenario: Different teams reuse a source task identity

- **WHEN** authorized listeners for `team-a` and `team-b` submit the same source identifier and source task identifier for their respective teams
- **THEN** the State Registry stores two independent canonical tasks because `team_id` is part of the deduplication key

### Requirement: State Registry defines the event-sourced task lifecycle

The State Registry SHALL define the canonical task lifecycle as `created -> dispatched -> running -> finished | failed`. Each transition SHALL be represented by an immutable task event row that the State Registry appends to the canonical task event log in the same transaction that updates the projected task state. The State Registry SHALL emit the `created` event on successful ingestion and the `dispatched` event on atomic approval. The assigned same-team Executor SHALL emit `running`, `finished`, and `failed`. The Registry SHALL append no other event types for the canonical lifecycle. The Registry SHALL expose the projected current state of each task alongside its ordered event history only within the caller's authorized team.

#### Scenario: Task moves through the full lifecycle

- **WHEN** a listener persists a task, an eligible same-team Executor receives approval, and the assigned Executor reports success
- **THEN** the State Registry appends `created`, `dispatched`, `running`, and `finished` events in that order, projects the task to `finished`, and returns the same ordered history to an authorized same-team reader

#### Scenario: Task fails after running

- **WHEN** an approved and running task fails in the assigned same-team Executor
- **THEN** the State Registry appends a `failed` event after the existing `running` event and projects the task to `failed`

### Requirement: State Registry emits registry-owned lifecycle events

The State Registry SHALL append the `created` event in the same transaction that inserts the canonical team-owned task row and SHALL append the `dispatched` event in the same transaction that sets `tasks.executor_id`, records `approved_at`, and removes the task from same-team discovery lists. Both events SHALL carry `task_id`, `event_type`, `occurred_at`, `event_id`, and the parent task's immutable `team_id`; they inherit the task's team ownership without requiring an envelope verification step because no Executor is the source. For both events `task_events.executor_id` SHALL be `NULL` because the Registry emits them.

#### Scenario: Ingestion emits a created event

- **WHEN** an authorized listener persists a new task
- **THEN** the State Registry inserts the canonical team-owned task row and appends a `created` event with `team_id` equal to the task and `executor_id = NULL` in the same transaction

#### Scenario: Approval emits a dispatched event

- **WHEN** approval succeeds for an eligible same-team, same-tag Executor
- **THEN** the State Registry appends a `dispatched` event with `team_id` equal to the task and `executor_id = NULL`, sets `tasks.executor_id` to the approved Executor, and records `approved_at` in the same transaction

### Requirement: State Registry accepts Executor-emitted lifecycle events

The State Registry SHALL accept `running`, `finished`, and `failed` events only from the authenticated Executor currently recorded as `tasks.executor_id`, and only when that Executor and task have the same immutable `team_id`. Each task event envelope SHALL carry `event_id`, `task_id`, `executor_id`, non-null `team_id`, `event_type`, `occurred_at`, and `payload`. The Registry SHALL verify that the envelope `team_id` equals the authenticated Executor's immutable `team_id` and equals the parent task's immutable `team_id` before accepting, SHALL append the event exactly once keyed by `(task_id, event_id)`, and SHALL project the new current state in the same transaction. Retries that repeat the same `event_id` SHALL return the original acceptance without appending a duplicate. Event-denial taxonomy: a same-team Executor that is not the recorded `tasks.executor_id` SHALL be rejected with `403 not_assigned`; an authenticated Executor whose envelope `team_id` differs from its immutable service binding SHALL be rejected with `403 team_mismatch`; a foreign task or Executor point identifier SHALL be rejected with the same non-revealing `404` shape used for an unknown identifier. No `403` response is returned for a foreign team probe.

#### Scenario: Assigned same-team Executor reports running

- **WHEN** the assigned same-team Executor posts a valid `running` event with a fresh `event_id`
- **THEN** the State Registry appends the event, projects the task to `running`, and returns `202 accepted`

#### Scenario: Executor retries a running event

- **WHEN** the assigned Executor repeats the same `running` event with the same `event_id`
- **THEN** the State Registry returns the original acceptance result without appending a duplicate event and without changing projected state

#### Scenario: Unassigned Executor reports running

- **WHEN** a same-team Executor other than `tasks.executor_id` posts a `running` event
- **THEN** the State Registry rejects the request with `403 not_assigned` without appending any event or changing projected state

#### Scenario: Foreign-team Executor names a task

- **WHEN** an authenticated Executor names a task owned by another team in an event request
- **THEN** the State Registry returns the same non-revealing `404` used for an unknown task and appends no event

#### Scenario: Task event envelope team_id does not match Executor

- **WHEN** an assigned same-team Executor posts a task event whose envelope `team_id` differs from the authenticated Executor's immutable `team_id`
- **THEN** the State Registry rejects the request with `403 team_mismatch` without appending the event and without changing projected state

### Requirement: State Registry rejects invalid or out-of-order lifecycle transitions

The State Registry SHALL reject any event submission or approval whose target transition is not permitted from the task's current state. After resolving an idempotent retry by `event_id`, every new lifecycle event's `(occurred_at, event_id)` tuple SHALL be strictly greater than the latest accepted tuple for that task. A new event with an older or equal ordering tuple SHALL be rejected without append or projection change. The contract SHALL NOT accept a caller-supplied `accepted_sequence` or other recovery override for an out-of-order event.

#### Scenario: Finished event after finished is rejected

- **WHEN** the assigned Executor posts a new `finished` event for a task already projected to `finished`
- **THEN** the State Registry rejects the request without appending the event or changing projected state

#### Scenario: Running event before dispatch is rejected

- **WHEN** an Executor posts a `running` event for a task whose current state is `created`
- **THEN** the State Registry rejects the request without appending the event or changing projected state

#### Scenario: Re-approval of a dispatched task is rejected

- **WHEN** a same-team Executor requests approval for a task whose current state is not `created`
- **THEN** the State Registry returns `409 task_already_dispatched` and appends no event

#### Scenario: Out-of-order lifecycle event is rejected

- **WHEN** the assigned Executor posts a new lifecycle event whose `(occurred_at, event_id)` is not strictly greater than the latest accepted tuple for the task
- **THEN** the State Registry rejects the request without appending the event or changing projected state, and no recovery field can override the rejection

### Requirement: State Registry orders accepted lifecycle events deterministically

The State Registry SHALL read accepted task events by `(occurred_at ASC, event_id ASC)` for projection verification and external read-back. `event_id` SHALL be a stable monotonic identifier that acts as the idempotency key and the tie-breaker for events sharing the same `occurred_at`. Equal timestamps are valid only when each newly accepted event has an `event_id` greater than the latest accepted event at that timestamp.

#### Scenario: Consecutive events share occurred_at

- **WHEN** two valid consecutive events for the same task share an `occurred_at` value and the later event has a greater monotonic `event_id`
- **THEN** the State Registry accepts them and exposes them in `event_id` ascending order

#### Scenario: Event retry preserves order

- **WHEN** an Executor retries an already accepted event with the same `event_id`
- **THEN** the State Registry returns the original result and the ordered history remains unchanged

### Requirement: State Registry persists team ownership in normalized 3NF PostgreSQL

The State Registry SHALL persist its durable surface in a third normal form PostgreSQL schema. The Registry SHALL own the tables `teams`, `executors`, `tasks`, `task_events`, `executor_events`, `environment_definitions`, `secrets`, `secret_versions`, `audit_entries`, and `task_control_requests`. `teams` SHALL own immutable `team_id` and display metadata including `team_name`; `team_name` SHALL NOT be used for authorization. Every non-key attribute SHALL depend on the whole primary key of its row and SHALL NOT transitively depend on a non-key column. Tenant-owned parent rows SHALL expose database-enforced team ownership, and child rows SHALL inherit and enforce that ownership through matching composite foreign keys or an equivalent database constraint. The Registry SHALL NOT publish an ER diagram as part of the contract.

#### Scenario: Team-owned parents reference teams

- **WHEN** an Executor, task, environment definition, or logical secret is persisted
- **THEN** the row references exactly one `teams.team_id` and its owning `team_id` cannot be changed

#### Scenario: Team name changes without changing authority

- **WHEN** display metadata changes `team_name` for an existing `team_id`
- **THEN** every authorization and ownership decision continues to use the unchanged `team_id`

#### Scenario: Child references a foreign-team parent

- **WHEN** a write attempts to associate a task event, Executor event, control, environment, secret, or secret version with a parent owned by another team
- **THEN** the State Registry and its database constraints reject the write without creating or reparenting the child row

### Requirement: State Registry normalizes team-owned entity relationships

The State Registry SHALL enforce `teams 1:N executors`, `teams 1:N tasks`, `teams 1:N environment_definitions`, `teams 1:N secrets`, `executors 1:N executor_events`, `executors 1:N tasks`, `tasks 1:N task_events`, `tasks 1:N task_control_requests`, `environment_definitions 1:N secrets`, and `secrets 1:N secret_versions`. `tasks.executor_id` SHALL be nullable before dispatch and immutable after approval and SHALL reference an Executor with the same `team_id`. `task_events.executor_id` SHALL be non-nullable for every Executor-emitted event and `NULL` only for registry-emitted `created` and `dispatched`; `task_events.team_id` SHALL be non-nullable for every row and SHALL equal the parent task's immutable `team_id`. `executor_events.executor_id` SHALL be `NOT NULL` and `executor_events.team_id` SHALL be `NOT NULL` for every Executor self event; both columns SHALL equal the authenticated Executor's immutable `team_id` and identity. `secret_versions` SHALL reference `secrets` rather than directly owning logical secret identity.

#### Scenario: Task executor_id is null before dispatch

- **WHEN** the Registry has appended only a `created` event for a task
- **THEN** `tasks.executor_id` is `NULL`

#### Scenario: Task executor_id becomes immutable after dispatch

- **WHEN** the Registry appends a `dispatched` event
- **THEN** `tasks.executor_id` is set to an approved same-team Executor and SHALL NOT change for any later write

#### Scenario: Executor-emitted event carries executor_id

- **WHEN** the assigned Executor appends a `running` event or an Executor appends a self event
- **THEN** the corresponding event's `executor_id` is not null, identifies the authenticated Executor, and enforces the same owning team

#### Scenario: Secret version references its logical secret

- **WHEN** State Registry persists a replacement secret value
- **THEN** it appends an immutable `secret_versions` row that references the existing same-team `secrets` row

### Requirement: State Registry lists created tasks by team and required tag

Each task SHALL declare exactly one immutable `team_id` and exactly one required tag. An Executor SHALL discover only `created` tasks whose `team_id` equals the authenticated Executor's single registered `team_id` and whose required tag equals its single registered tag. Discovery SHALL apply both equality predicates before pagination or result counts, SHALL be read-only, SHALL NOT reserve or assign a task, and SHALL NOT read or enforce capacity. Discovery MAY return the same task to multiple eligible Executors in the same team.

#### Scenario: Executor discovers a same-team, same-tag task

- **WHEN** an Executor requests discovery and a `created` task matches both its registered `team_id` and tag
- **THEN** the State Registry returns the task summary without changing state or assignment, regardless of `max_capacity` or `running_count`

#### Scenario: Same tag exists in another team

- **WHEN** only a foreign-team `created` task matches the Executor's registered tag
- **THEN** the State Registry returns no task and no result count, cursor, or pagination metadata reveals the foreign task

#### Scenario: Executor requests an unregistered tag

- **WHEN** an Executor requests discovery for a tag other than its registered tag
- **THEN** the State Registry rejects the request without revealing tasks for that tag or any team

### Requirement: Executors register exactly one team and one tag

Each Executor service identity SHALL be bound to exactly one immutable authorized `team_id`. Each Executor SHALL register its identity, that same `team_id`, Executor type, exactly one authorized tag, runtime metadata, observed `max_capacity`, and observed `running_count` with the State Registry. The Registry SHALL verify the submitted `team_id` against the authenticated Executor service identity, accept a single tag, reject registration that omits or changes the team, and reject registration that omits a tag or supplies more than one tag.

#### Scenario: Executor registers one team and one tag

- **WHEN** an Executor identity authorized for `team-a` registers with `team_id = team-a`, one tag, type, capacity observations, and metadata
- **THEN** the State Registry accepts the registration and persists one immutable team and one tag

#### Scenario: Executor attempts another team

- **WHEN** an Executor identity authorized for `team-a` registers or re-registers with `team_id = team-b`
- **THEN** the State Registry rejects the request without creating or changing an Executor record

#### Scenario: Executor registration has an invalid tag count

- **WHEN** an Executor registration omits the tag or supplies more than one tag
- **THEN** the State Registry rejects the registration without creating an Executor record

### Requirement: State Registry observes Executor capacity without gating discovery or approval

The State Registry SHALL store `max_capacity` and `running_count` as observations on the team-owned `executors` row, refreshed by the latest Executor self event. The Registry SHALL NOT read, compare, evaluate, or enforce capacity during discovery, approval, or lifecycle-event handling. Approval SHALL succeed for an otherwise eligible same-team, same-tag Executor regardless of observations. The Executor alone SHALL decide when local capacity permits discovery, approval, or runtime start.

#### Scenario: Approval succeeds at full Executor capacity

- **WHEN** an eligible same-team, same-tag Executor requests approval while its `running_count` equals `max_capacity`
- **THEN** the State Registry returns `200 approved`, appends a `dispatched` event, and the Executor decides locally whether to start a runtime

#### Scenario: Registry does not reject on capacity

- **WHEN** an eligible Executor submits an approval request with `max_capacity = 1` and `running_count = 1`
- **THEN** the State Registry SHALL NOT return `422 executor_at_capacity` or any other capacity-related rejection

### Requirement: State Registry approves task start atomically within a team

Before starting work, an Executor SHALL ask State Registry to approve a specific `task_id` using its authenticated Executor identity. State Registry SHALL approve only when the task is `created`, the task and Executor have the same immutable `team_id`, and the task's required tag equals the Executor's single registered tag. In one transaction, approval SHALL append `dispatched`, set `tasks.executor_id`, record `approved_at`, and remove the task from same-team discovery. Exactly one concurrent eligible requester SHALL receive `200 approved`; later same-team competitors SHALL receive `409 task_already_dispatched` without an event or assignment change. A foreign-team `task_id` SHALL return non-revealing `404` and SHALL cause no mutation or event. An Executor SHALL NOT start a runtime before approval.

#### Scenario: Same-team Executors race to approve one task

- **WHEN** two same-team, same-tag Executors concurrently request approval for one `created` task
- **THEN** exactly one receives `200 approved`, the other receives `409 task_already_dispatched`, one assignment and one `dispatched` event exist, and the task disappears from same-team discovery

#### Scenario: Foreign-team Executor requests approval

- **WHEN** an authenticated Executor requests approval for a task owned by another team
- **THEN** the State Registry returns the same `404` used for an unknown task, appends no event, and leaves state and assignment unchanged

#### Scenario: Dispatched Executor fails

- **WHEN** an Executor fails after approval
- **THEN** the task remains assigned; the assigned Executor reports `failed` when possible, and the task does not silently return to `created`

### Requirement: State Registry records team-scoped task events

The State Registry SHALL append task-scoped events received from the assigned same-team Executor to the canonical task event log. Task events SHALL include stable `event_id`, `task_id`, non-null `executor_id`, non-null `team_id` equal to the parent task's `team_id`, `event_type`, `occurred_at`, and `payload`; no `accepted_sequence` field is accepted. Repeating an event identifier SHALL NOT append a duplicate. Every accepted event append SHALL update `tasks.current_state` in the same transaction.

#### Scenario: Assigned Executor writes a task event

- **WHEN** the assigned same-team Executor posts a valid event to `POST /v1/tasks/{task_id}/events`
- **THEN** the State Registry appends the event once and updates projected current state transactionally

#### Scenario: Executor retries a task event

- **WHEN** an Executor repeats a task event with the same `event_id`
- **THEN** the State Registry returns the original acceptance without appending a duplicate

### Requirement: State Registry records team-scoped Executor self events

The State Registry SHALL maintain canonical team-owned Executor records and append Executor-scoped lifecycle, health, capacity, and running-child-count events separately from task event logs. Every Executor self-event envelope SHALL carry `event_id`, `executor_id`, non-null `team_id`, `event_type`, `occurred_at`, and `payload`; `executor_events.team_id` SHALL be persisted on append and SHALL inherit the authenticated Executor's team. The Registry SHALL verify that the envelope `team_id` equals the authenticated Executor's immutable `team_id` before accepting. Every `executor_events` row SHALL have `executor_id NOT NULL`, SHALL identify the authenticated Executor, and SHALL inherit that Executor's immutable team ownership. The Registry SHALL mirror the latest observation onto `executors` in the same transaction and SHALL NOT use observations to gate discovery or approval. Event-denial taxonomy: an authenticated Executor whose envelope `team_id` differs from its immutable service binding SHALL be rejected with `403 team_mismatch`; a foreign-team Executor point identifier SHALL be rejected with the same non-revealing `404` shape used for an unknown identifier.

#### Scenario: Executor writes a self event

- **WHEN** an authenticated Executor posts a lifecycle or capacity event to `POST /v1/executors/{executor_id}/events` with matching envelope `team_id`
- **THEN** State Registry appends it with non-null `executor_id` and matching `team_id` to that same-team Executor's history and transactionally updates observed `max_capacity` and `running_count`

#### Scenario: Self event envelope team_id does not match Executor

- **WHEN** an authenticated Executor posts a self event whose envelope `team_id` differs from the authenticated Executor's immutable `team_id`
- **THEN** the State Registry rejects the request with `403 team_mismatch` without appending the event and without changing projected Executor state

#### Scenario: Executor names a foreign Executor

- **WHEN** an authenticated Executor posts to an `executor_id` owned by another team
- **THEN** the State Registry returns non-revealing `404` and changes no Executor row or event history

### Requirement: State Registry requires trusted team-scoped API Gateway context

State Registry SHALL accept operator reads, subscriptions, controls, environment writes, and secret writes only from an authenticated trusted API Gateway service identity. Every such request SHALL carry Gateway-verified `operator_id`, immutable `team_id`, and `request_id`; the optional display-only `team_name` is forwarded only when the verified operator team carries one. State Registry SHALL authorize solely with `team_id`; `team_name`, caller-supplied tenant headers, and unauthenticated payload fields SHALL NOT grant or broaden access, and the absence of `team_name` SHALL NOT weaken authorization anchored to `team_id`. Each operator authentication context SHALL represent exactly one team.

#### Scenario: Gateway forwards verified operator context

- **WHEN** trusted API Gateway forwards an operator request with verified `operator_id`, `team_id`, and `request_id`, with or without an optional `team_name`
- **THEN** State Registry evaluates authorization using only `team_id` and retains `team_name` only as display or audit context when present

#### Scenario: Direct client supplies team headers

- **WHEN** a client without the trusted API Gateway service identity supplies `operator_id`, `team_id`, `team_name`, or similar headers directly
- **THEN** State Registry rejects the request without reading or changing protected tenant data

### Requirement: State Registry returns non-revealing not-found for foreign resources

For an authenticated listener, Executor, or Gateway request scoped to one team, every task, Executor, event, control, environment, secret, secret version, or audit identifier owned by another team SHALL be treated as absent. State Registry SHALL return the same `404` shape used for an unknown identifier and SHALL NOT reveal existence, ownership, state, metadata, counts, or timing-dependent details. Rejection SHALL append no domain event, mutate no canonical resource, create no control, and disclose no audit entry.

#### Scenario: Same-team operator reads a foreign task identifier

- **WHEN** trusted API Gateway for `team-a` requests a task identifier owned by `team-b`
- **THEN** State Registry returns the same non-revealing `404` as an unknown task and performs no mutation or event append

#### Scenario: Same-team operator writes a foreign environment identifier

- **WHEN** trusted API Gateway for `team-a` attempts to update an environment owned by `team-b`
- **THEN** State Registry returns non-revealing `404`, leaves the environment and secrets unchanged, and performs no OpenBao operation

### Requirement: State Registry filters team-scoped reads before result shaping

State Registry SHALL apply the trusted `team_id` predicate before pagination, cursor creation, totals, counts, grouping, aggregation, and result serialization for tasks, Executors, events, controls, environments, secrets, secret versions, and audit entries. No page length, total, cursor, aggregate, or empty/non-empty distinction SHALL incorporate rows from another team.

#### Scenario: Teams share filter values

- **WHEN** `team-a` and `team-b` have records matching the same state, tag, source, project, or time-range filter and Gateway queries as `team-a`
- **THEN** rows, totals, counts, aggregates, and pagination metadata are computed only from `team-a` records

#### Scenario: Foreign rows exist beyond page boundary

- **WHEN** foreign-team rows would change a global cursor or page count if evaluated first
- **THEN** State Registry excludes those rows before pagination so the authorized response is identical to a database containing only the caller's team

### Requirement: State Registry isolates WebSocket subscriptions by team

State Registry SHALL accept operator WebSocket upgrades only from trusted API Gateway context containing one verified `team_id`. It SHALL bind the connection to that immutable team at upgrade and SHALL apply the team predicate before replay selection, cursor handling, subscription filters, live fan-out, and frame serialization. Every emitted frame SHALL belong to the bound team. A cross-team frame, count, cursor, or existence signal is a review-blocking contract violation, and the change SHALL NOT be accepted for implementation or release without automated evidence that concurrent subscriptions cannot receive another team's data.

#### Scenario: Concurrent teams subscribe to the same event kinds

- **WHEN** Gateways for `team-a` and `team-b` subscribe concurrently using identical event filters
- **THEN** each connection receives only frames whose resources belong to its bound `team_id`

#### Scenario: Subscription requests a foreign identifier

- **WHEN** a `team-a` subscription filter names a resource owned by `team-b`
- **THEN** State Registry treats it as unknown, emits no foreign frame or existence signal, and does not add the foreign resource to replay or live fan-out

### Requirement: State Registry records team-scoped gateway-mediated controls

State Registry SHALL store operator intervention and cancellation requests as audit-backed control records only when trusted API Gateway context and the target task share the same `team_id`. Each write SHALL use an idempotency key scoped to `(team_id, task_id, operator_id, action)`. Operators SHALL interact only with Web UI and API Gateway. Executors SHALL read pending controls only for tasks assigned to that same-team Executor.

#### Scenario: Same-team operator requests cancellation

- **WHEN** an operator requests cancellation through trusted API Gateway for a task in the operator's team
- **THEN** State Registry appends the control and a team-scoped audit entry containing request outcome without secret plaintext

#### Scenario: Operator targets another team's task

- **WHEN** trusted API Gateway context names a task owned by another team
- **THEN** State Registry returns non-revealing `404` and appends no control or task event

### Requirement: State Registry stores team-owned environment definitions

State Registry SHALL accept environment-definition writes only from trusted API Gateway context for the same `team_id`, store immutable team ownership with non-secret `KEY=value` entries and optional project/task applicability, append a team-scoped audit entry, and return a stable environment identifier. Same-team operators manage environments for the team. Project and task scopes SHALL limit applicability inside that team and SHALL NOT grant cross-team access.

#### Scenario: Same-team operator stores an environment definition

- **WHEN** an operator submits valid non-secret environment values through trusted API Gateway context
- **THEN** State Registry stores the definition under that context's immutable `team_id`, appends an audit entry, and returns its environment identifier

#### Scenario: Project scope narrows applicability

- **WHEN** a team-owned environment is scoped to one project
- **THEN** tasks from the same team but another project cannot use it, and no project scope can make it available to another team

### Requirement: State Registry stores team-owned secrets with immutable versions

State Registry SHALL store each logical secret in a team-owned `secrets` row associated with an environment in the same team. It SHALL encrypt every submitted secret value through OpenBao Transit before persistence, store ciphertext and metadata in a new immutable `secret_versions` row referencing `secrets`, and SHALL NOT persist or log plaintext. Same-team operators manage secrets through trusted API Gateway context; optional project/task scope limits applicability within the team.

#### Scenario: Same-team operator stores a secret

- **WHEN** a valid secret write arrives through trusted API Gateway for a same-team environment
- **THEN** State Registry encrypts the value before persistence, stores a logical `secrets` row and immutable referenced `secret_versions` row as applicable, appends a plaintext-free audit entry, and returns the secret identifier and version

#### Scenario: Secret value changes

- **WHEN** a same-team operator replaces a secret value
- **THEN** State Registry appends a new encrypted `secret_versions` row referencing the same logical secret without modifying or exposing prior plaintext

#### Scenario: Secret targets a foreign environment

- **WHEN** trusted Gateway context attempts to create or replace a secret under another team's environment
- **THEN** State Registry returns non-revealing `404`, performs no OpenBao operation, and stores no secret or version

### Requirement: State Registry opens environments only for assigned same-team Executors

State Registry SHALL expose `GET /v1/environments/{environment_id}/open?task_id={task_id}` only to the Executor assigned to the referenced task. The request SHALL carry the canonical `task_id` query parameter and the compact three-part signed token in the `X-FlowAI-Scope-Token` request header (the body SHALL NOT carry any scope-token fields). State Registry SHALL reject the request without any OpenBao operation or value disclosure when the `task_id` query parameter is absent, malformed, or, after successful MAC verification and canonical claim parsing, does not equal both the payload `task_id` claim and the canonical assigned task, returning the same non-revealing `404 environment_unknown_or_unavailable` shape. The Executor SHALL present its authenticated identity and a signed, unexpired scope token satisfying the "State Registry signs open-environment scope tokens with an allow-listed HMAC and a server-controlled rotating key" requirement. Before decrypting, State Registry SHALL verify that the protected-header `kid` equals the payload `key_id`, the token MAC under the declared allow-listed HMAC algorithm (`HS256`/`HS384`/`HS512`) and the documented active key window for `key_id`, the expected literal `audience` (`state-registry.environment.open`), the `issued_at <= server_now + 30 seconds` and `expiry > issued_at` and `expiry - issued_at <= 5 minutes` window, every token claim (including the project-scope rule for `project_id`: required, nullable only when the canonical environment has no project scope) against canonical records, same-team ownership across Executor, task, environment, and secrets, task assignment, the non-terminal task state, and project/task applicability. On success it SHALL return authorized env-style values and append a plaintext-free team-scoped audit entry. Plaintext SHALL remain in memory only. State Registry SHALL NOT accept a token whose `key_id` is outside the documented active window, SHALL NOT accept a token after terminal task state or Executor unassignment, SHALL NOT accept a token from a different authenticated Executor identity, and SHALL perform every check before any OpenBao operation.

#### Scenario: Assigned same-team Executor opens an environment

- **WHEN** the assigned Executor sends `GET /v1/environments/{environment_id}/open?task_id={task_id}` with the compact three-part scope token in the `X-FlowAI-Scope-Token` request header, the protected header carrying `alg` (allow-listed `HS256`/`HS384`/`HS512`), `kid`, and `typ` (`scope-token+json`) with `kid == payload key_id`, and the payload's `team_id`, `project_id` (required, null only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `key_id`, `issued_at`, and `expiry` (`expiry > issued_at`, `expiry - issued_at <= 5 minutes`, `issued_at <= server_now + 30 seconds`) all matching canonical records and the MAC verifying under constant-time comparison
- **THEN** State Registry returns authorized values and records `team_id`, actor, action, resource, request, and outcome without plaintext

#### Scenario: Scope token has a foreign or mismatched claim

- **WHEN** any token claim, signature, audience, `key_id`, expiry, assignment, team, project, task, environment, or Executor binding is invalid
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value

### Requirement: State Registry signs open-environment scope tokens with an allow-listed HMAC and a server-controlled rotating key

State Registry SHALL be the sole issuer and verifier of open-environment scope tokens. The compact three-part wire format SHALL be `<header>.<payload>.<signature>` carried only in the `X-FlowAI-Scope-Token` request header for `GET /v1/environments/{environment_id}/open?task_id={task_id}`. Each token SHALL be signed using an allow-listed HMAC algorithm from the set `HS256`/`HS384`/`HS512` (the "HMAC-SHA-256 or a stronger HMAC" family), keyed with a State Registry-controlled key selected by a `key_id` claim included in the payload. The protected header SHALL carry `alg` (allow-listed), `kid`, and `typ`; the payload SHALL carry `team_id`, `project_id` (a required claim whose value is nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry`, and `key_id`. State Registry SHALL require `expiry > issued_at`, SHALL bound `expiry - issued_at <= 5 minutes`, SHALL require `issued_at <= server_now + 30 seconds` (premature-beyond-skew rejection), SHALL require `audience` to match the documented literal identifier, SHALL verify that the protected-header `kid` equals the payload `key_id` before any MAC computation, SHALL recompute the signature under the declared allow-listed algorithm and compare it under constant-time comparison before any other check, SHALL accept only `key_id` values inside the documented active key window for rotation, SHALL perform canonical claim verification (presence, types, encoding, allowed values, and the project-scope rule for `project_id`) before any team, assignment, or applicability check, and SHALL never log token plaintext, individual claims, MAC bytes, key material, or derived key bytes. A token SHALL be valid only when the calling authenticated Executor identity is the currently assigned same-team Executor for the referenced task and the referenced task is in a non-terminal state; any other identity, a terminal task state, or unassignment SHALL cause rejection before any OpenBao operation. A retry of the same token within its TTL by the same assigned same-team Executor MAY be allowed; the retry SHALL NOT bypass canonical claim, transition, or assignment checks, SHALL NOT extend TTL, and SHALL NOT revive an expired token.

#### Scenario: Token envelope carries the documented claims

- **WHEN** State Registry issues an open-environment scope token
- **THEN** the envelope includes non-null `team_id`, `task_id`, `environment_id`, `executor_id`, `audience`, `key_id`, `issued_at`, and `expiry`, and the required `project_id` claim whose value is null only when the canonical environment has no project scope

#### Scenario: Protected-header kid must equal payload key_id

- **WHEN** the protected header `kid` differs from the payload `key_id`
- **THEN** State Registry rejects the request with the same non-revealing `404 environment_unknown_or_unavailable` response used for every other invalid or unavailable case, performs no MAC comparison, and performs no OpenBao operation

#### Scenario: Algorithm outside the allow-listed HMAC set is rejected

- **WHEN** an Executor presents a token whose protected header `alg` is anything other than `HS256`, `HS384`, or `HS512`
- **THEN** State Registry rejects the request with the same non-revealing `404 environment_unknown_or_unavailable` response used for every other invalid or unavailable case and performs no OpenBao operation

#### Scenario: MAC is verified under constant-time comparison

- **WHEN** an Executor presents an open-environment scope token
- **THEN** State Registry compares the MAC using a constant-time comparison and rejects the request when the comparison fails before any further check or any OpenBao operation

#### Scenario: Expiry is bounded by five minutes after issued_at

- **WHEN** a token's `expiry` is more than five minutes after `issued_at`
- **THEN** State Registry rejects the token as malformed and never uses it for verification

#### Scenario: Expiry must be strictly later than issued_at

- **WHEN** a token's `expiry` is not strictly later than `issued_at`
- **THEN** State Registry rejects the token with the same non-revealing `404 environment_unknown_or_unavailable` response and performs no OpenBao operation

#### Scenario: Issued_at must not be premature beyond the clock-skew window

- **WHEN** a token's `issued_at` is in the future beyond `server_now + 30 seconds`
- **THEN** State Registry rejects the token with the same non-revealing `404 environment_unknown_or_unavailable` response and performs no OpenBao operation

#### Scenario: Audience must match the literal expected identifier

- **WHEN** a token's `audience` differs from the documented literal identifier `state-registry.environment.open`
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value

#### Scenario: key_id outside the documented active window is rejected

- **WHEN** a token's `key_id` does not fall inside the documented active key window for HMAC rotation
- **THEN** State Registry rejects the token and performs no OpenBao operation

#### Scenario: Canonical claim verification fails

- **WHEN** a token has unexpected, missing, or malformed claim fields, types, or allowed values, including a null `project_id` for a project-scoped environment or any non-null mismatch with canonical records
- **THEN** State Registry rejects the request before any team, assignment, applicability, or OpenBao operation

#### Scenario: Token replay by a different identity is denied

- **WHEN** an authenticated Executor different from the original assigned same-team Executor presents a token whose claims still pass canonical checks
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value and records an audit entry without token plaintext

#### Scenario: Token after terminal task state is denied

- **WHEN** the referenced task is in a terminal state and the assigned Executor presents a still-valid token
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value and performs no OpenBao operation

#### Scenario: Token after Executor unassignment is denied

- **WHEN** the Executor is no longer the recorded `tasks.executor_id` and that Executor presents a still-valid token
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value and performs no OpenBao operation

#### Scenario: Retry within TTL by the same assigned identity MAY be allowed

- **WHEN** the same assigned same-team Executor presents the same token again before `expiry`
- **THEN** State Registry MAY return the same authorized values, but SHALL still verify the MAC, `key_id`, audience, every canonical claim, task non-terminal state, and current assignment on each retry and SHALL NOT extend TTL or revive an expired token

#### Scenario: Operational logs and audit never contain token plaintext or key material

- **WHEN** State Registry processes, accepts, rejects, retries, or logs any scope-token request
- **THEN** application logs, audit entries, and error responses contain no token plaintext, individual claim values beyond permitted identifier-level metadata, MAC bytes, key material, or derived key bytes

### Requirement: State Registry records plaintext-free team audit entries

For each auditable authorized operator action, control, environment or secret mutation, and open-environment access, State Registry SHALL append an immutable audit entry containing `team_id`, actor identity and type, action, resource type and identifier, `request_id`, outcome, and timestamp. Audit entries, application logs, event payloads, and error responses SHALL NOT contain secret plaintext or decrypted environment values. Audit reads SHALL be filtered by trusted `team_id` before pagination or aggregation.

#### Scenario: Authorized secret write is audited

- **WHEN** a same-team operator creates a secret through trusted API Gateway context
- **THEN** the audit entry identifies the team, operator, action, secret resource, Gateway request, and outcome without containing the plaintext value

#### Scenario: Assigned Executor opens an environment

- **WHEN** an assigned same-team Executor successfully opens an environment
- **THEN** the audit entry identifies the team, Executor actor, environment resource, request, and success outcome without containing returned values

### Requirement: State Registry enforces team-bound service identities

State Registry SHALL authenticate listener, Executor, and API Gateway service identities. Each listener and Executor identity SHALL be bound to exactly one immutable authorized `team_id`. Listener source identifiers and submitted `team_id` SHALL be bound to the authenticated listener. Executor registration, team and tag authorization, discovery, approval, task-event writes, Executor-event writes, control reads, and environment opens SHALL be bound to the authenticated Executor identity. API Gateway-only reads, subscriptions, environment/secret writes, and controls SHALL require the trusted Gateway identity and verified operator context. Untrusted client headers SHALL NOT establish a team.

#### Scenario: Caller impersonates another service or team

- **WHEN** a caller uses another listener, Executor, Gateway, or `team_id` without the corresponding trusted service credential and authorization
- **THEN** State Registry rejects the request without revealing or changing protected state

### Requirement: State Registry remains independent from execution and identity management

State Registry SHALL NOT execute tasks, control Docker containers or Kubernetes Pods, issue bearer tokens, manage team membership, implement team CRUD UI, implement project-level RBAC, expose plaintext secrets to operators, listeners, API Gateway, or unassigned Executors, or centrally decide Executor scheduling or capacity. Executors SHALL retain runtime lifecycle and local capacity enforcement. Every task, Executor, and operator authentication context SHALL represent exactly one team.

#### Scenario: Approved task references an environment

- **WHEN** an Executor receives same-team approval for a task that references an applicable environment
- **THEN** the response includes only the environment identifier and team-bound scope token, while values remain unavailable until the assigned Executor calls the open-environment API

#### Scenario: Project scope is evaluated

- **WHEN** a team-owned environment has project or task applicability metadata
- **THEN** State Registry uses it only to narrow use within that team and does not treat it as project RBAC or cross-team authorization

### Requirement: State Registry uses mutually authenticated encrypted transport

All listener, Executor, API Gateway, OpenBao, and PostgreSQL connections to or from State Registry SHALL use encrypted transport. Service HTTP and WebSocket callers SHALL use mutually authenticated TLS identities bound to service role and team authorization rules; database and OpenBao clients SHALL validate server identity and certificate chains.

#### Scenario: Caller uses an untrusted transport identity

- **WHEN** a caller presents no trusted client identity, an expired identity, or an identity for another service role
- **THEN** State Registry rejects the connection before reading or changing protected data
