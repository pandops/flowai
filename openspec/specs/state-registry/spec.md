# state-registry Specification

## Purpose

TBD - created by archiving change v0002-state-registry. Update Purpose after archive.

## Requirements

### Requirement: State Registry is the canonical team-scoped task source

The State Registry SHALL be the durable source of truth for every FlowAI task, its immutable owning `team_id`, projected state, assignment, immutable event history, audit history, and operator control requests. Every task SHALL belong to exactly one team for its lifetime. Event listeners such as Jira integrations and webhook receivers SHALL authenticate with a service identity bound to exactly one immutable authorized `team_id` and to exactly one immutable authorized `source_system_id` registered through `POST /admin/source-systems`, and SHALL supply that immutable `team_id`, the external `source_id` (the external task identifier within that source system), and a REQUIRED `task_type_id` referencing a task type registered through `POST /admin/task-types` whose immutable `team_id` equals the authenticated listener's team. The State Registry SHALL verify the submitted `team_id` against the authenticated listener authorization, SHALL verify that `source_system_id` belongs to the same team, and SHALL verify that `task_type_id` belongs to that team before persisting or acknowledging the task. The State Registry SHALL derive `tasks.required_tag` from the referenced task type's `execution_tag`. The Registry SHALL reject any ingestion body that supplies a listener-authored `required_tag` field (whether at the top level, nested under a body wrapper, or as a request header) with `400 listener_supplied_required_tag_forbidden`; the listener SHALL NOT supply an independently authoritative `required_tag` and the Registry SHALL NOT silently ignore or silently overwrite a listener-supplied `required_tag` value.

#### Scenario: Authorized listener stores a new team task

- **WHEN** a listener authenticated for `team-a` and a registered source system `source-system-a` submits a previously unseen `(team-a, source-system-a, source-id)` triple with a `task_type_id` that references a task type registered for `team-a`
- **THEN** the State Registry persists the complete canonical task record with immutable `team_id = team-a`, immutable `source_system_id = source-system-a`, the supplied `source_id`, the listener-supplied `task_type_id`, `required_tag` derived server-side from the task type's `execution_tag`, projected state `pending`, no `created` event, and empty event history, all in one transaction before acknowledging the source event

#### Scenario: Listener submits an unauthorized team

- **WHEN** a listener authenticated for `team-a` submits a task with `team_id = team-b`
- **THEN** the State Registry rejects the request without creating a task, appending an event, or acknowledging successful ingestion

#### Scenario: Listener submits a task_type_id that belongs to another team

- **WHEN** a listener authenticated for `team-a` submits a task whose `task_type_id` references a task type registered for `team-b`
- **THEN** the State Registry rejects the request without creating a task, appending an event, or acknowledging successful ingestion; `tasks.required_tag` is derived only from a same-team task type

#### Scenario: Task becomes discoverable only after commit

- **WHEN** an authorized listener is persisting a new task
- **THEN** no Executor can discover or claim that task until the persistence transaction commits

#### Scenario: Authorized listener names a same-team task type

- **WHEN** an authorized listener submits `task_type_id` referencing a task type registered for the same team
- **THEN** the State Registry persists the canonical task with non-null `task_type_id`, derives `tasks.required_tag` from the referenced `task_type.execution_tag`, and rejects any later listener-supplied `required_tag` value

#### Scenario: Authorized listener omits the required task_type_id

- **WHEN** an authorized listener submits an ingestion body without the required `task_type_id`
- **THEN** the State Registry rejects the request without creating or partially persisting any task; `task_type_id` is REQUIRED on every listener submission

#### Scenario: Authorized listener names a task_type_id from another team

- **WHEN** an authorized listener for `team-a` submits a `task_type_id` that references a task type registered for `team-b`
- **THEN** the State Registry rejects the request without creating or partially persisting any task; the database composite `(tasks.task_type_id, tasks.team_id) -> (task_types.task_type_id, task_types.team_id)` foreign-key constraint and the listener-time validation both reject the submission

#### Scenario: Authorized listener supplies a listener-authoritative required_tag

- **WHEN** an authorized listener submits a `required_tag` field as part of the ingestion body (whether at the top level, nested under a body wrapper, or as a header) alongside a REQUIRED `task_type_id` referencing a same-team task type
- **THEN** the State Registry rejects the request with `400 listener_supplied_required_tag_forbidden` without creating or partially persisting any task, without appending any event, and without acknowledging ingestion; the listener SHALL NOT submit an independently authoritative `required_tag` and the Registry derives `tasks.required_tag` solely from the referenced `task_type.execution_tag`

### Requirement: State Registry deduplicates listener tasks within a team

Each listener submission SHALL include immutable `team_id`, immutable `source_system_id`, the external `source_id` (the external task identifier within that source system), and the required `task_type_id`. The State Registry SHALL enforce uniqueness on `(team_id, source_system_id, source_id)` and SHALL return the existing canonical task when the same triple is received again without creating another task, appending any event, or resetting state or history. Each `(source_system_id, source_id)` is independent across teams because `team_id` is part of the dedupe key; independence is demonstrated only by distinct registered source systems or authorized team submissions, never by one source-system identity owning two teams. The Registry SHALL derive `required_tag` from the referenced task type at ingestion. The Registry SHALL reject any ingestion body that supplies a listener-authored `required_tag` field (whether at the top level, nested under a body wrapper, or as a request header) with `400 listener_supplied_required_tag_forbidden`; the Registry SHALL NOT silently trust, silently ignore, or silently overwrite a listener-supplied `required_tag` value.

#### Scenario: Listener repeats a source task in one team

- **WHEN** an authorized listener submits the same `(team_id, source_system_id, source_id)` triple more than once
- **THEN** the State Registry returns the existing canonical task unchanged, appends no new event, and stores only one task record in that team

#### Scenario: Different teams reuse the external source_id across distinct registered source systems

- **WHEN** `team-a` registers source system `source-system-a` (via `POST /admin/source-systems`), `team-b` registers a separate source system `source-system-b` (via `POST /admin/source-systems`), and authorized listeners for each team submit the same external `source_id` under their own team's registered `source_system_id`
- **THEN** the State Registry stores two independent canonical tasks because `team_id` is part of the deduplication key; independence comes from distinct team-owned source systems, never from one source-system identity spanning teams

#### Scenario: Cross-team authorization through a single shared source identity is impossible

- **WHEN** an authenticated listener bound to `team-a` and to source system `source-system-a` submits a task with `team_id = team-b`
- **THEN** the State Registry rejects the submission without persisting or acknowledging it; no source-system identity may be owned by more than one team; each team registers its own source system through `POST /admin/source-systems`

#### Scenario: Listener names a source system not registered for its team

- **WHEN** an authenticated listener bound to `team-a` submits a task with `source_system_id = source-system-b` where `source-system-b` is registered for `team-b`, not `team-a`
- **THEN** the State Registry rejects the submission without persisting or acknowledging it; the listener is authorized only for source systems registered under its own team

### Requirement: State Registry defines the task lifecycle as pending then created then running then terminal

The State Registry SHALL project each unclaimed task as `pending` immediately after ingestion commits; the `pending` projection reflects the durable unclaimed task row with no lifecycle event appended at ingestion. A successful claim SHALL atomically set `tasks.owner_command_id` and `tasks.executor_id`, resolve the image, append the FIRST task lifecycle event `created` with `executor_id` set to the claiming Executor and a payload meaning `task <task_id> loaded by <executor_id>`, project the task to `created`, and remove the task from the claimer's scope of discovery. The canonical lifecycle SHALL then progress `created -> running -> finished | failed`. The `dispatched` lifecycle state SHALL NOT exist; no event of that name SHALL be appended at any time. The `pending` state SHALL exist before claim with no lifecycle event appended.

#### Scenario: Task moves through the full lifecycle after claim

- **WHEN** a listener persists a `pending` task, an eligible same-team or system Executor claims that exact oldest pending task, and the claiming Executor reports success
- **THEN** the State Registry appends `created`, `running`, and `finished` events with `executor_id` non-null for the Executor-emitted events in that order, projects the task through `created` and `running` to `finished`, and returns the same ordered history to an authorized reader

#### Scenario: Task fails after running

- **WHEN** a `running` task fails in the claiming same-team Executor
- **THEN** the State Registry appends a `failed` event after the existing `running` event and projects the task to `failed`

### Requirement: State Registry appends the first `created` event atomically on successful claim

The State Registry SHALL append the first task lifecycle event `created` in the same transaction that atomically transitions a `pending` task to `created`. The `created` event SHALL carry `task_id`, `executor_id` set to the claiming Executor, `event_type = "created"`, `occurred_at`, `event_id`, and a payload whose meaning is `task <task_id> loaded by <executor_id>`. The `created` event SHALL be the FIRST task event; no `dispatched` event exists before or after it, and the Registry SHALL append no lifecycle event for the `pending` state. The `created` event is Registry-emitted (persisted by the State Registry inside the claim transaction); the Executor SHALL NOT separately emit a `created` event. The Executor SHALL emit `running`, `finished`, and `failed` only. The `created` event's `executor_id` SHALL be non-null and equal to the claiming Executor's authenticated identity because the Registry records the claimer at append time.

#### Scenario: Claim emits the first created event with non-null executor_id

- **WHEN** an authenticated Executor successfully claims a `pending` task in FIFO order
- **THEN** the State Registry, in one transaction, inserts the canonical team-owned task row + the FIRST lifecycle event `created` with `executor_id` equal to the claiming Executor and a payload meaning `task <task_id> loaded by <executor_id>`; `tasks.owner_command_id` and `tasks.executor_id` are set; no `dispatched` event is ever appended; no event was appended at ingestion when the task was `pending`; the Executor subsequently emits `running` itself

#### Scenario: Pending task has no event before claim

- **WHEN** an authorized listener persists a new `(team_id, source_system_id, source_id)` triple
- **THEN** the State Registry persists the task row with projected state `pending`, no event, an empty event history, and `tasks.owner_command_id = NULL`

### Requirement: State Registry rejects invalid or out-of-order lifecycle transitions

The State Registry SHALL reject any event submission or claim whose target transition is not permitted from the task's current state. After resolving an idempotent retry by `event_id`, every new lifecycle event's `(occurred_at, event_id)` tuple SHALL be strictly greater than the latest accepted tuple for that task. A new event with an older or equal ordering tuple SHALL be rejected without append or projection change. The contract SHALL NOT accept a caller-supplied `accepted_sequence` or other recovery override for an out-of-order event.

#### Scenario: Finished event after finished is rejected

- **WHEN** the claiming Executor posts a new `finished` event for a task already projected to `finished`
- **THEN** the State Registry rejects the request without appending the event or changing projected state

#### Scenario: Running event before created is rejected

- **WHEN** an Executor posts a `running` event for a task whose current state is `pending`
- **THEN** the State Registry rejects the request without appending the event or changing projected state

#### Scenario: Out-of-order lifecycle event is rejected

- **WHEN** the claiming Executor posts a new lifecycle event whose `(occurred_at, event_id)` is not strictly greater than the latest accepted tuple for the task
- **THEN** the State Registry rejects the request without appending the event or changing projected state, and no recovery field can override the rejection

### Requirement: State Registry orders accepted lifecycle events deterministically

The State Registry SHALL read accepted task events by `(occurred_at ASC, event_id ASC)` for projection verification and external read-back. `event_id` SHALL be a stable monotonic identifier that acts as the idempotency key and the tie-breaker for events sharing the same `occurred_at`. Equal timestamps are valid only when each newly accepted event has an `event_id` greater than the latest accepted event at that timestamp.

#### Scenario: Consecutive events share occurred_at

- **WHEN** two valid consecutive events for the same task share an `occurred_at` value and the later event has a greater monotonic `event_id`
- **THEN** the State Registry accepts them and exposes them in `event_id` ascending order

#### Scenario: Event retry preserves order

- **WHEN** an Executor retries an already accepted event with the same `event_id`
- **THEN** the State Registry returns the original result and the ordered history remains unchanged

### Requirement: State Registry owns team registration through `/admin/teams`

The State Registry SHALL expose `POST /admin/teams` to create a team, accepting only an authenticated system-administrator identity. The request body SHALL include a REQUIRED unique `team_name` (display-only, never authorization) and a REQUIRED `default_image` opaque container image reference. The State Registry SHALL persist a generated immutable `team_id`, the unique `team_name`, the required `default_image`, and an `ingested_at` timestamp. After successful creation, the State Registry SHALL return the new canonical team record including the generated `team_id`. The system-administrator identity is the only role authorized to create teams through this path; the State Registry SHALL reject calls without an authenticated system-administrator identity, and SHALL reject `team_name` values that are not unique across existing teams. The State Registry SHALL NOT provide a separate path to create or update teams through any non-admin interface.

#### Scenario: Admin creates a team

- **WHEN** an authenticated system-administrator submits a unique `team_name` and a required opaque `default_image` to `POST /admin/teams`
- **THEN** the State Registry persists a generated immutable `team_id`, the unique `team_name`, the required `default_image`, and returns the canonical team record

#### Scenario: Non-admin caller cannot create a team

- **WHEN** a caller without an authenticated system-administrator identity submits `POST /admin/teams`
- **THEN** the State Registry rejects the request without creating or partially persisting any team

#### Scenario: Team creation rejects duplicate or missing display name

- **WHEN** an authenticated system-administrator submits a `team_name` that already exists or omits `team_name` or omits the required `default_image`
- **THEN** the State Registry rejects the request without creating a team

#### Scenario: Team default image is required at registration

- **WHEN** any team exists
- **THEN** its `default_image` SHALL be set and SHALL never be cleared through any admin or operator path; required registration guarantees resolution always succeeds

### Requirement: State Registry owns source-system registration through `/admin/source-systems`

The State Registry SHALL expose `POST /admin/source-systems` to register a team-owned source system, accepting only an authenticated system-administrator identity. The request body SHALL include a REQUIRED reference to an existing immutable `team_id`, an immutable server-assigned `source_system_id`, an opaque listener identity reference bound to exactly one team, and an OPTIONAL `default_image` opaque container image reference. Each registered source system SHALL belong to exactly one immutable `team_id`. A source system identity belongs to one team and SHALL NEVER be reassigned to another team; cross-team reuse is rejected without persistence. The State Registry SHALL reject calls without an authenticated system-administrator identity and SHALL reject submissions whose `team_id` does not already exist.

#### Scenario: Admin registers a team-owned source system

- **WHEN** an authenticated system-administrator submits `team_id = team-a`, a listener identity reference, and an optional `default_image` to `POST /admin/source-systems`
- **THEN** the State Registry persists an immutable `source_system_id`, immutable `team_id = team-a`, the listener identity reference, the optional `default_image`, and rejects any later attempt to attach the same `source_system_id` to another team

#### Scenario: Source-system creation fails for unknown team

- **WHEN** an authenticated system-administrator submits a `team_id` that does not exist
- **THEN** the State Registry rejects the request without creating or partially persisting any source system

#### Scenario: Non-admin caller cannot create a source system

- **WHEN** a caller without an authenticated system-administrator identity submits `POST /admin/source-systems`
- **THEN** the State Registry rejects the request without persisting any source system

#### Scenario: Admin attempts to register a source system under an already-bound listener principal in the same team

- **WHEN** an authenticated system-administrator submits a new `POST /admin/source-systems` whose `listener_identity` value is already bound to an existing `(team_id, source_system_id)` row in `source_systems`, regardless of whether the existing row references the same team or a different team
- **THEN** the State Registry rejects the request without persisting or partially persisting any source system; the global uniqueness index over `source_systems.listener_identity` enforces that one listener principal maps to exactly one `(team_id, source_system_id)` pair and one principal cannot be reused across teams or re-registered under the same team

### Requirement: State Registry owns task-type registration through `/admin/task-types`

The State Registry SHALL expose `POST /admin/task-types` to register a team-owned task type, accepting only an authenticated system-administrator identity. The request body SHALL include a REQUIRED reference to an existing immutable `team_id`, an immutable server-assigned `task_type_id`, a REQUIRED `execution_tag` (the immutable registered tag that Executors must declare to be eligible for tasks of this type), and an OPTIONAL `default_image` opaque container image reference. Each registered task type SHALL belong to exactly one immutable `team_id`. The State Registry SHALL reject calls without an authenticated system-administrator identity and SHALL reject submissions whose `team_id` does not already exist.

#### Scenario: Admin registers a team-owned task type

- **WHEN** an authenticated system-administrator submits `team_id = team-a`, a required `execution_tag = openhands`, and an optional `default_image` to `POST /admin/task-types`
- **THEN** the State Registry persists an immutable `task_type_id`, immutable `team_id = team-a`, the required `execution_tag`, the optional `default_image`

#### Scenario: Task-type creation fails for unknown team

- **WHEN** an authenticated system-administrator submits a `team_id` that does not exist
- **THEN** the State Registry rejects the request without creating any task type

#### Scenario: Non-admin caller cannot create a task type

- **WHEN** a caller without an authenticated system-administrator identity submits `POST /admin/task-types`
- **THEN** the State Registry rejects the request without persisting any task type

### Requirement: State Registry exposes `GET /admin/tags` as a system-admin-only tag projection

The State Registry SHALL expose `GET /admin/tags` to return a global read-only projection of canonical task-configuration-derived tags. The endpoint SHALL accept only an authenticated system-administrator identity and SHALL reject every other identity (listener, Executor, API Gateway, anonymous, or any caller without the documented system-administrator authentication) before reading any row. The Registry SHALL emit each entry from the `task_types` table projection and the entry SHALL contain EXACTLY three fields: `team_id`, `task_type_id`, and `execution_tag`; the Registry SHALL NOT add, omit, rename, or re-order these fields, SHALL NOT include `default_image`, and SHALL NOT include any other column from any table. The Registry SHALL order entries deterministically by `(team_id ASC, execution_tag ASC, task_type_id ASC)`. The endpoint SHALL support cursor pagination with a default page size of 50 and a maximum page size of 200 entries per response; an out-of-range or malformed `limit` SHALL be rejected without reading or returning any row. The endpoint SHALL accept optional exact-match `team_id` and `tag` query filters; a filter that names a value not registered in any `task_types` row SHALL return an empty page. The endpoint SHALL NOT create, update, delete, or mutate any team, source system, task type, task, assignment, event, environment, secret, secret version, control request, or audit entry; SHALL NOT read or return task payload values, decrypted environment values, secret material, nonce bytes, ciphertext bytes, authentication tag bytes, key material, or event history; SHALL NOT grant, broaden, select, or weaken any tenant authorization from the projected tag values, and equal tag strings appearing under different `team_id`s SHALL grant no cross-team authority. There is no standalone `tags` table, no tag create, update, or delete endpoint, and no system-admin authentication-mechanism definition beyond the requirement that the identity be authenticated as a system administrator.

#### Scenario: System administrator lists configured tags across teams

- **WHEN** an authenticated system administrator calls `GET /admin/tags` while multiple teams have registered `task_types` rows with `execution_tag = openhands`
- **THEN** the Registry returns one entry per registered task type whose body contains EXACTLY `team_id`, `task_type_id`, and `execution_tag` (no `default_image`, no other columns), ordered deterministically by `(team_id ASC, execution_tag ASC, task_type_id ASC)`; pagination cursors advance through the global collection; no team, source system, task type, task, assignment, event, environment, secret, control, or audit row is mutated; no task payload, decrypted environment value, secret material, or event history is returned; equal `execution_tag` strings appearing under different `team_id`s grant no cross-team authority

#### Scenario: Non-admin identity is rejected before any row is read

- **WHEN** a caller authenticated as a listener, Executor, API Gateway operator, or any non-system-administrator identity submits `GET /admin/tags`
- **THEN** the Registry rejects the request without reading or returning any `task_types` row and without mutating any team, source system, task type, task, assignment, event, environment, secret, control, or audit entry

#### Scenario: Tag filter restricts results to one tag

- **WHEN** an authenticated system administrator calls `GET /admin/tags?tag=openhands`
- **THEN** the Registry returns only entries whose `execution_tag = openhands` with EXACTLY `team_id`, `task_type_id`, and `execution_tag` in each entry body, ordered by `(team_id ASC, task_type_id ASC)`, paginated; no mutation occurs and equal tag strings across teams grant no authority

#### Scenario: Team filter restricts results to one team

- **WHEN** an authenticated system administrator calls `GET /admin/tags?team_id=team-a`
- **THEN** the Registry returns only entries whose `team_id = team-a` with EXACTLY `team_id`, `task_type_id`, and `execution_tag` in each entry body, ordered by `(execution_tag ASC, task_type_id ASC)`, paginated; no mutation occurs

### Requirement: State Registry binds opaque cursors to endpoint, scope, filters, ordering, and direction

State Registry SHALL emit opaque cursors for every paginated Gateway read (`GET /v1/tasks`, and any successor collection returning `PageInfo.next_cursor`) and every paginated admin read (`GET /admin/tags`, `GET /admin/tasks`). Each cursor SHALL be integrity-protected under a server-controlled HMAC keyed by a State Registry-controlled `key_id`, SHALL be bound to the originating endpoint and the authenticated authorization scope at the time of issue (the trusted `team_id` plus verified `operator_id` from the trusted API Gateway for Gateway cursors; the authenticated system-administrator role for admin cursors), SHALL encode the complete active filter tuple (every filter and operator-supplied query parameter applied to the page), SHALL encode the deterministic resource-appropriate ordering rule for that endpoint (for example `(ingested_at DESC, task_id DESC)` for `GET /v1/tasks` and `GET /admin/tasks`), and SHALL encode the page direction (forward only). The Registry SHALL verify the cursor integrity (HMAC) and SHALL reject the request before any protected query runs when the cursor is missing, malformed, has an unknown `key_id`, fails the integrity check, names a different endpoint, names a different authorization scope, names different filter values, names different ordering, names a different direction, or has been revoked. State Registry SHALL also reject a request whose `limit` is below 1, above the documented maximum (200 for Gateway and admin collections), malformed, or otherwise outside the documented range before any protected query runs. Every rejection under this requirement SHALL return the same non-revealing `400 invalid_pagination` response shape without revealing pagination internals or row counts and SHALL NOT mutate or read any team, source system, task type, task, assignment, event, environment, secret, secret version, control, or audit row. Cursors are unguessable opaque tokens to callers; pagination metadata (`next_cursor`, `count`) is computed solely from rows that pass the team predicate for Gateway reads and from the authenticated system-administrator's accessible rows for admin reads.

#### Scenario: Forward page through a Gateway collection advances via next_cursor

- **WHEN** a trusted API Gateway identity for `team-a` pages through `GET /v1/tasks` with a documented filter and observes `next_cursor` populated in the response
- **THEN** returning that `next_cursor` to the same authenticated context returns the next page in `(ingested_at DESC, task_id DESC)` order with no row overlap, no foreign-row inclusion, and the same filter tuple

#### Scenario: Tampered cursor is rejected before any protected query runs

- **WHEN** a trusted API Gateway identity for `team-a` calls `GET /v1/tasks?cursor=<modified>` with any single bit changed in the cursor
- **THEN** State Registry detects the integrity failure, rejects the request with the same non-revealing `400 invalid_pagination` shape BEFORE any `tasks` row is read, leaves all canonical state unchanged, and never reveals the integrity check

#### Scenario: Cross-endpoint cursor reuse is rejected

- **WHEN** a trusted API Gateway identity for `team-a` calls `GET /admin/tasks?cursor=<cursor issued for /v1/tasks>` (or any other endpoint-binding mismatch)
- **THEN** State Registry detects the endpoint mismatch, rejects the request with the same non-revealing `400 invalid_pagination` shape BEFORE any protected query runs, and never returns rows from the wrong endpoint

#### Scenario: Cross-scope cursor reuse is rejected

- **WHEN** a cursor issued under trusted Gateway context for `team-a` is presented under trusted Gateway context for `team-b` or under an admin context
- **THEN** State Registry detects the scope mismatch, rejects the request with the same non-revealing `400 invalid_pagination` shape BEFORE any protected query runs, and never returns rows from the wrong team or role

#### Scenario: Changed-filter cursor reuse is rejected

- **WHEN** a cursor issued for filter `team_id = team-a, state = pending` is presented under trusted Gateway context for `team-a` with any query filter altered (added, removed, or value-changed) compared with the original filter tuple
- **THEN** State Registry detects the filter mismatch, rejects the request with the same non-revealing `400 invalid_pagination` shape BEFORE any protected query runs, and never returns rows computed against a different filter

#### Scenario: Malformed limit is rejected before any protected query runs

- **WHEN** a trusted API Gateway identity or an authenticated system administrator calls any paginated collection with `limit` set to 0, set to a value greater than the documented maximum (200), set to a non-integer value, or omitted when the documented default differs from 50
- **THEN** State Registry rejects the request with the same non-revealing `400 invalid_pagination` shape BEFORE any row is read or returned and never mutates any canonical row

### Requirement: State Registry exposes `GET /admin/tasks` as a system-admin-only global task projection

The State Registry SHALL expose `GET /admin/tasks` to return a global read-only projection of canonical task summaries across every team. The endpoint SHALL accept only an authenticated system-administrator identity and SHALL reject every other identity (listener, Executor, API Gateway, anonymous, or any caller without the documented system-administrator authentication) before reading any row. Each entry SHALL contain EXACTLY eleven fields: `task_id`, `team_id`, `task_type_id`, `source_system_id`, `source_id`, `required_tag`, `current_state`, `owner_command_id`, `executor_id`, `ingested_at`, and `claimed_at`; the Registry SHALL NOT add, omit, rename, or re-order these fields, SHALL NOT include `image`, `resolved_image`, `image_source`, `project_id`, `environment_id`, or any other column from any table, and SHALL NOT include task payload values. The Registry SHALL order entries deterministically by `(ingested_at DESC, task_id DESC)`. The endpoint SHALL support cursor pagination with a default page size of 50 and a maximum page size of 200 entries per response; an out-of-range or malformed `limit` SHALL be rejected without reading or returning any row. The endpoint SHALL accept optional exact-match `team_id`, `state`, `tag`, and `task_type_id` query filters; a filter that matches no canonical task SHALL return an empty page. The endpoint SHALL NOT create, update, delete, or mutate any team, source system, task type, task, assignment, event, environment, secret, secret version, control request, or audit entry; SHALL NOT return task payload values, decrypted environment values, secret material, nonce bytes, ciphertext bytes, authentication tag bytes, key material, or event history; SHALL NOT grant, broaden, select, or weaken any tenant authorization. "All" tasks means a traversable, paginated global collection; the response SHALL NOT be unbounded and SHALL always be expressed as a paginated page with a cursor. There is no system-admin authentication-mechanism definition beyond the requirement that the identity be authenticated as a system administrator. This admin read-only projection does NOT change the canonical `Task` resource returned to a successful claim, to a same-team Gateway read, to a same-team Executor discovery, or to any other non-admin endpoint.

#### Scenario: System administrator lists tasks across every team

- **WHEN** an authenticated system administrator calls `GET /admin/tasks` while teams `team-a` and `team-b` each have multiple tasks in mixed states
- **THEN** the Registry returns one entry per canonical task whose body contains EXACTLY `task_id`, `team_id`, `task_type_id`, `source_system_id`, `source_id`, `required_tag`, `current_state`, `owner_command_id`, `executor_id`, `ingested_at`, and `claimed_at` (no `image`, no `resolved_image`, no `image_source`, no `project_id`, no `environment_id`, no task payload), ordered deterministically by `(ingested_at DESC, task_id DESC)`; pagination cursors advance through the full global collection; no payload, secret, decrypted environment value, or event history is returned; no team, source system, task type, task, assignment, event, environment, secret, control, or audit row is mutated; equal tag strings across teams grant no cross-team authority

#### Scenario: Non-admin identity is rejected before any row is read

- **WHEN** a caller authenticated as a listener, Executor, API Gateway operator, or any non-system-administrator identity submits `GET /admin/tasks`
- **THEN** the Registry rejects the request without reading or returning any `tasks` row, without reading events, environments, secrets, controls, or audit entries, and without mutating any row

#### Scenario: Optional filters narrow the global collection

- **WHEN** an authenticated system administrator calls `GET /admin/tasks?team_id=team-a&state=pending&tag=openhands&task_type_id=tt-a1`
- **THEN** the Registry returns only entries matching all four exact-match filters, paginated by `(ingested_at DESC, task_id DESC)`; no mutation occurs and no payload, secret, decrypted environment value, or event history is returned

#### Scenario: Admin reads perform no mutation

- **WHEN** an authenticated system administrator calls `GET /admin/tags` or `GET /admin/tasks` repeatedly against the same canonical state
- **THEN** the Registry never appends to or modifies any team, source system, task type, task, assignment, event, environment, secret, control, or audit row, never persists or transmits a new event, never creates a task, and never extends or shortens any history

### Requirement: State Registry persists team ownership in normalized 3NF PostgreSQL

The State Registry SHALL persist its durable surface in a third normal form PostgreSQL schema. The Registry SHALL own the tables `teams`, `executors`, `tasks`, `task_events`, `executor_events`, `environment_definitions`, `secrets`, `secret_versions`, `audit_entries`, `task_control_requests`, `source_systems`, and `task_types`. `teams` SHALL own the required opaque `default_image` plus the immutable `team_id` and display metadata including `team_name`; `team_name` SHALL NOT be used for authorization. Every non-key attribute SHALL depend on the whole primary key of its row and SHALL NOT transitively depend on a non-key column. Tenant-owned parent rows SHALL expose database-enforced team ownership, and child rows SHALL inherit and enforce that ownership through matching composite foreign keys or an equivalent database constraint when `team_id` is non-null. The Registry SHALL NOT publish an ER diagram as part of the contract.

#### Scenario: Team-owned parents reference teams

- **WHEN** an Executor, task, environment definition, logical secret, source system, or task type is persisted
- **THEN** the row references exactly one `teams.team_id` and its owning `team_id` cannot be changed (source systems and task types never change team_id; system Executors persist `team_id = NULL` only)

#### Scenario: Display metadata is read-only and never authorizes

- **WHEN** an authenticated system administrator reads or audits any team-owned record
- **THEN** display metadata such as `team_name` is included only as audit or display context; every authorization and ownership decision continues to use the unchanged `team_id`, and `team_name` SHALL NOT be selected, granted, broadened, weakened, or replaced by any caller-supplied value because no team update endpoint exists

#### Scenario: Child references a foreign-team parent

- **WHEN** a write attempts to associate a task event, Executor event, control, environment, secret, source-system child, task-type child, or secret version with a parent owned by another team
- **THEN** the State Registry and its database constraints reject the write without creating or reparenting the child row

### Requirement: State Registry normalizes team-owned entity relationships

The State Registry SHALL enforce `teams 1:N executors` (team-owned only), `teams 1:N tasks`, `teams 1:N environment_definitions`, `teams 1:N secrets`, `teams 1:N source_systems`, `teams 1:N task_types`, `executors 1:N executor_events`, `tasks 1:N task_events`, `tasks 1:N task_control_requests`, `source_systems 1:N tasks`, `task_types 1:N tasks`, `environment_definitions 1:N secrets`, and `secrets 1:N secret_versions`. `tasks.executor_id` SHALL be nullable before claim and immutable from claim onward; `tasks.owner_command_id` SHALL be nullable before claim and immutable from claim onward. `tasks.team_id` SHALL remain immutable for the lifetime of the row regardless of Executor scope. `task_events.executor_id` SHALL be non-nullable for every Executor-emitted event including the lifecycle `created` event emitted on claim; `task_events.team_id` SHALL be non-nullable for every row and SHALL equal the parent task's immutable `team_id`. `executor_events.executor_id` SHALL be `NOT NULL`; `executor_events.team_id` SHALL be `NOT NULL` when `scope = team` and SHALL be `NULL` when `scope = system`. `secret_versions` SHALL reference `secrets` rather than directly owning logical secret identity.

#### Scenario: Task executor_id and owner_command_id are null before claim

- **WHEN** the Registry has only persisted a `pending` task with no claim
- **THEN** `tasks.executor_id` and `tasks.owner_command_id` SHALL both be `NULL` and no lifecycle event SHALL exist

#### Scenario: Task owner_command_id and executor_id become immutable after claim

- **WHEN** the Registry appends the first lifecycle event `created`
- **THEN** `tasks.executor_id` is set to the claiming Executor, `tasks.owner_command_id` is set to the immutable `command_id`, and SHALL NOT change for any later write

#### Scenario: Executor-emitted event carries executor_id

- **WHEN** the assigned Executor appends a `running`, `finished`, or `failed` event
- **THEN** the event's `executor_id` is not null, identifies the authenticated Executor, and `task_events.team_id` equals the parent task's immutable `team_id`

#### Scenario: Secret version references its logical secret

- **WHEN** State Registry persists a replacement secret value
- **THEN** it appends an immutable `secret_versions` row that references the existing same-team `secrets` row

#### Scenario: Source system and task type reference their immutable team

- **WHEN** an admin registers a source system or task type
- **THEN** the row references the immutable `teams.team_id`, never changes team, and rejects cross-team attempts through the unique constraint

### Requirement: State Registry lists pending tasks by exact tag, scoped to the Executor's eligibility

Each task SHALL declare exactly one immutable `team_id` and exactly one required execution tag derived server-side from the referenced `task_types.execution_tag` at ingestion. Discovery SHALL be filtered by the authenticated Executor's single registered tag, `current_state = pending`, and an eligibility predicate derived from the Executor's scope. When `Executors.scope = team`, eligibility SHALL require `tasks.team_id = executors.team_id` AND `tasks.required_tag = executors.authorized_tag`. When `Executors.scope = system`, eligibility SHALL require `tasks.required_tag = executors.authorized_tag` and SHALL match tasks across every team; the parent task's immutable `team_id` SHALL be carried in the discovery response. The eligibility predicate precedes FIFO ordering: the discovery ordering rule SHALL be `(ingested_at ASC, task_id ASC)` for the tasks that pass the eligibility predicate, the eligibility predicate SHALL be applied before pagination and counts, and discovery SHALL be read-only, SHALL NOT reserve or assign a task, and SHALL NOT read or enforce capacity. The discovery response SHALL include `task_id`, `team_id`, `required_tag`, `current_state = pending`, `ingested_at`, `source_system_id`, `source_id`, `task_type_id`, `project_id`, `environment_id`, `image`, `owner_command_id` (null), and `executor_id` (null). The `ingested_at` column is immutable for the task lifetime and `task_id` SHALL be the deterministic tie-breaker for FIFO ordering. Equal `required_tag` strings across teams SHALL NOT grant cross-team authority; team-owned discovery remains filtered by `tasks.team_id = executors.team_id`.

#### Scenario: Team-owned Executor discovers a same-team pending task

- **WHEN** an Executor registered with `scope = team` requests discovery and a `pending` task matches both its registered `team_id` and tag
- **THEN** the State Registry returns the task summary without changing state or assignment, in FIFO `(ingested_at ASC, task_id ASC)` order with the eligibility predicate applied first, regardless of `max_capacity` or `running_count`

#### Scenario: Team-owned Executor is hidden from foreign-team tasks

- **WHEN** a team-owned Executor registered for `team-a` requests discovery and only foreign-team `pending` tasks match its registered tag
- **THEN** the State Registry returns no task and no result count, cursor, pagination metadata, timing, or existence signal reveals the foreign tasks

#### Scenario: System-owned Executor discovers pending tasks across teams

- **WHEN** an Executor registered with `scope = system` requests discovery and `pending` tasks from `team-a` and `team-b` both match its single registered tag
- **THEN** the State Registry returns summaries for both teams' matching tasks in FIFO `(ingested_at ASC, task_id ASC)` order, eligibility predicate first, without changing state or assignment, and each summary carries its `team_id`, `ingested_at`, `required_tag`, and other task fields

#### Scenario: Executor requests an unregistered tag

- **WHEN** an Executor requests discovery for a tag other than its registered tag
- **THEN** the State Registry rejects the request without revealing tasks for that tag or any team

### Requirement: Executors register one tag and either team-owned or system-owned scope

Each Executor SHALL register at most one ownership `scope` chosen from `{team, system}`. First-start registration SHALL use `POST /v1/executors`; State Registry SHALL generate an immutable UUID `executor_id`, persist it on the new canonical row, and return it in `201 Created`. The request body SHALL carry neither `executor_id` nor `identity`; State Registry SHALL derive the canonical service identity exclusively from authenticated mTLS. Restart refresh SHALL use `PUT /v1/executors/{executor_id}` with the identifier previously returned and cached by the Executor; PUT SHALL update only the existing matching row and SHALL return `404` rather than create a missing Executor. When `scope = team`, the Executor service identity SHALL be bound to exactly one immutable authorized `team_id` and SHALL submit that `team_id` in the registration body; State Registry SHALL verify the submitted `team_id` exists and SHALL persist that `team_id` on the `executors` row. When `scope = system`, the Executor service identity SHALL NOT be bound to any team and the registration body SHALL omit the `team_id` property; State Registry SHALL persist `team_id` as NULL on the `executors` row and SHALL mark the Executor as system-owned. Both scopes SHALL register exactly one authorized tag, Executor type, and runtime metadata. The Registry SHALL reject any registration that omits the tag or supplies more than one tag, SHALL reject any registration that supplies `team_id` with `scope = system` including explicit null, SHALL reject any registration that omits `team_id` with `scope = team`, and SHALL reject a registration that supplies a `team_id` that does not reference an existing team. Executor registration and refresh SHALL NOT create or update any team.

#### Scenario: Team-owned Executor registers one team and one tag

- **WHEN** an Executor identity authorized for `team-a` posts first-start registration with `scope = team`, `team_id = team-a`, one tag, type, and metadata
- **THEN** State Registry generates and returns one immutable UUID `executor_id` and persists one immutable team and one tag

#### Scenario: Executor refreshes with its cached server identifier

- **WHEN** a restarted Executor sends `PUT /v1/executors/{executor_id}` using the identifier returned by its successful first registration
- **THEN** State Registry refreshes that existing record without generating another identifier or creating another Executor

#### Scenario: Team-owned Executor attempts another team

- **WHEN** an Executor identity authorized for `team-a` registers or re-registers with `scope = team` and `team_id = team-b`
- **THEN** the State Registry rejects the request without creating or changing an Executor record

#### Scenario: System-owned Executor registers without a team

- **WHEN** an Executor identity with no team binding registers with `scope = system`, omits the `team_id` property, and supplies one tag, type, and metadata
- **THEN** the State Registry accepts the registration, persists `team_id = NULL` on the `executors` row, and records the Executor as system-owned

#### Scenario: System-owned Executor registration with team_id is rejected

- **WHEN** an Executor identity submits a registration with `scope = system` and includes the `team_id` property with either null or a value
- **THEN** the State Registry rejects the request without creating or changing an Executor record

#### Scenario: Team-owned Executor registration with unknown team_id is rejected

- **WHEN** an Executor identity submits a registration with `scope = team` and `team_id` that does not reference an existing team
- **THEN** the State Registry rejects the request without creating or changing an Executor record

#### Scenario: Team-owned Executor registration without team_id is rejected

- **WHEN** an Executor identity submits a registration with `scope = team` and omits `team_id` or supplies null
- **THEN** the State Registry rejects the request without creating or changing an Executor record

#### Scenario: Executor registration has an invalid tag count

- **WHEN** an Executor registration omits the tag or supplies more than one tag
- **THEN** the State Registry rejects the registration without creating an Executor record

#### Scenario: Executor registration never creates or updates a team

- **WHEN** any Executor registration is submitted
- **THEN** the State Registry SHALL NOT create a new `teams` row, SHALL NOT update an existing `teams` row, SHALL NOT alter `default_image`, and SHALL NOT touch any team-owned column

### Requirement: State Registry observes Executor capacity without gating discovery or claim

The State Registry SHALL store the latest self-reported Executor observations on the `executors` row, refreshed by the latest Executor self event. The Registry SHALL NOT read, compare, evaluate, or enforce capacity during discovery, claim, or lifecycle-event handling. Claim SHALL succeed for an otherwise eligible Executor regardless of observations, including when `running_count >= max_capacity`. The Executor alone SHALL decide when local capacity permits discovery, claim, or runtime start.

#### Scenario: Claim succeeds at full Executor capacity

- **WHEN** an eligible Executor claims the oldest eligible pending task while its self-reported `running_count` equals `max_capacity`
- **THEN** the State Registry returns `200 claimed`, appends the first `created` event, and the Executor decides locally whether to start a runtime

#### Scenario: Registry does not reject on capacity

- **WHEN** an eligible Executor submits a claim request with `max_capacity = 1` and `running_count = 1`
- **THEN** the State Registry SHALL NOT return `422 executor_at_capacity` or any other capacity-related rejection

### Requirement: State Registry claims the oldest eligible task atomically under FIFO

Before starting work, an Executor SHALL ask State Registry to claim a specific `task_id` using its authenticated Executor identity on `POST /v1/executors/{executor_id}/claim`. The claim body SHALL carry `task_id` and the immutable `command_id`. The State Registry SHALL approve the claim only when ALL of these hold: the task is `pending`; the task's required tag equals the Executor's single registered tag; the eligibility predicate from the Executor's scope holds (`tasks.team_id = executors.team_id` for team scope; tag-match only for system scope); AND the requested task is the OLDEST currently eligible unclaimed task for that authenticated Executor inside the claim transaction. Inside one transaction, claim SHALL append the FIRST `created` lifecycle event with `executor_id` set to the claiming Executor and a payload meaning `task <task_id> loaded by <executor_id>`; SHALL set `tasks.executor_id` (immutable thereafter) and `tasks.owner_command_id` (immutable thereafter) from the request; SHALL persist `tasks.resolved_image` from the documented image-resolution rule with `image_source` set to the matching source; SHALL set `tasks.claimed_at` to the transaction commit time; and SHALL remove the task from the requesting Executor's scope of discovery. The FIFO rule is scoped to the Executor's eligibility: `scope = team` = same team + tag in `(ingested_at ASC, task_id ASC)` order; `scope = system` = matching tag across teams in `(ingested_at ASC, task_id ASC)` order. Eligibility precedes ordering precedes pagination precedes counts. The complete claim-conflict taxonomy SHALL be: (a) `task_id` unknown or foreign to the authenticated Executor SHALL return non-revealing `404` with no mutation and no event; (b) `task_id` already claimed under a different `command_id` SHALL return `409 task_already_claimed` with no mutation and no event appended; (c) the same `(task_id, command_id)` by the original claiming Executor SHALL return the original `200 claimed` body with no new event and no field update; (d) an eligible but non-oldest `pending` `task_id` SHALL return `409 older_task_must_be_claimed_first` with no mutation and no event appended; (e) a `task_id` whose current state is not `pending` (and is not the same `(task_id, command_id)` of an existing claim) SHALL return non-revealing `404` with no mutation and no event appended. The Registry SHALL NOT use the foreign-team probe as a way to distinguish a 404 from a 409; the conflict-code distinction between cases (a), (b), (d), and (e) is observable only to a caller authorized to see the canonical task. Concurrent claims SHALL be atomic; exactly one eligible requester wins; competitors SHALL receive `409 task_already_claimed` after the winner commits. An Executor SHALL NOT start a runtime before a successful claim.

#### Scenario: Team-owned Executor claims the oldest pending task

- **WHEN** an authenticated team-owned Executor registers a `command_id` and the `task_id` of the oldest eligible pending same-team same-tag task
- **THEN** the State Registry atomically appends the first `created` event with `executor_id` equal to the claiming Executor, sets `tasks.executor_id` and `tasks.owner_command_id` (both immutable from claim onward), persists `tasks.resolved_image` and `tasks.image_source`, removes the task from this Executor's discovery scope, projects the task to `created`, and returns `200 claimed`

#### Scenario: Executor requests a non-oldest eligible pending task

- **WHEN** an authenticated Executor submits a `task_id` that is eligible but is NOT the oldest currently eligible `pending` task for that authenticated Executor (FIFO violation)
- **THEN** the State Registry returns `409 older_task_must_be_claimed_first`, appends no event, sets no fields, and performs no mutation

#### Scenario: Same Executor retries the same task with the same command

- **WHEN** an authenticated Executor repeats a `claim` with the same `(task_id, command_id)` after a successful claim
- **THEN** the State Registry returns the original `200 claimed` response with the same `task`, `resolved_image`, `image_source`, scope token, and `claimed_at`; no new event is appended and no field is updated

#### Scenario: Different command claims an already-claimed task

- **WHEN** an authenticated Executor submits a `claim` whose `(task_id)` is already claimed but with a different `command_id`
- **THEN** the State Registry returns `409 task_already_claimed` without mutation and without appending any event

#### Scenario: One command may own multiple tasks

- **WHEN** an authenticated Executor submits successive successful claims using the SAME `command_id` for different `task_id`s
- **THEN** the State Registry accepts every claim, sets `tasks.owner_command_id` to the same `command_id` on every claimed task, and never appends additional events for the same task; `tasks.owner_command_id` is unique per task (each task is limited to one command), but a single command MAY own many tasks over time

#### Scenario: Two Executors race to claim the same task

- **WHEN** two eligible Executors concurrently request claim for the same `task_id` with different `command_id`s
- **THEN** exactly one receives `200 claimed`, the other receives `409 task_already_claimed`, one `created` event exists on the task, and the task disappears from the claimer's scope of discovery

#### Scenario: Team-owned Executor cannot claim a foreign-team task

- **WHEN** a team-owned Executor requests claim for a `pending` task whose `team_id` differs from the Executor's registered team
- **THEN** the State Registry returns non-revealing `404` without appending an event or changing assignment, regardless of tag match

#### Scenario: Executor claims an unknown identifier

- **WHEN** an authenticated Executor requests claim for a `task_id` that does not exist
- **THEN** the State Registry returns non-revealing `404` without appending an event and leaves state and assignment unchanged

#### Scenario: Executor claims an already-claimed task under a different command_id

- **WHEN** an authenticated Executor requests claim for a `task_id` already claimed under a different `command_id`
- **THEN** the State Registry returns `409 task_already_claimed` without mutation and without appending any event; the existing assignment, `owner_command_id`, `executor_id`, `resolved_image`, `image_source`, and `claimed_at` remain immutable

#### Scenario: Executor claims a non-pending task that is not an already-claimed conflict

- **WHEN** an authenticated Executor requests claim for a `task_id` whose current state is `created`, `running`, `finished`, or `failed` and is not the same `(task_id, command_id)` of an existing claim
- **THEN** the State Registry returns non-revealing `404` without appending an event and leaves state and assignment unchanged

### Requirement: State Registry accepts Executor-emitted lifecycle events under the Executor's ownership scope

The State Registry SHALL append task-scoped events received from the claiming Executor to the canonical task event log. Task events SHALL include stable `event_id`, `task_id`, non-null `executor_id`, non-null `team_id` equal to the parent task's immutable `team_id`, `event_type`, `occurred_at`, and `payload`; no `accepted_sequence` field is accepted. Repeating an event identifier SHALL NOT append a duplicate. Every accepted event append SHALL update `tasks.current_state` in the same transaction. Envelope `team_id` verification is conditional on the assigned Executor's ownership scope: when the Executor's `scope = team`, State Registry SHALL verify that the envelope `team_id` equals the authenticated Executor's immutable `team_id` AND equals the parent task's immutable `team_id`; when the Executor's `scope = system`, State Registry SHALL verify that the envelope `team_id` equals the parent task's immutable `team_id` (the Executor's own `team_id` is NULL by construction, so the envelope must carry the task's team). Event-denial taxonomy: a same-team Executor that is not the recorded `tasks.executor_id` SHALL be rejected with `403 not_assigned`; a system-owned Executor that is not the recorded `tasks.executor_id` SHALL also be rejected with `403 not_assigned`; an authenticated team-owned Executor whose envelope `team_id` differs from its immutable service binding SHALL be rejected with `403 team_mismatch`; a foreign task or Executor point identifier SHALL be rejected with the same non-revealing `404` shape used for an unknown identifier. No `403` response is returned for a foreign team probe.

#### Scenario: Team-owned assigned Executor writes a task event

- **WHEN** the assigned team-owned Executor posts a valid event with envelope `team_id` matching its authenticated team and the parent task
- **THEN** the State Registry appends the event and projects the task in the same transaction

#### Scenario: System-owned assigned Executor writes a task event

- **WHEN** the assigned system-owned Executor posts a valid event with envelope `team_id` equal to the parent task's `team_id` (the Executor's own `team_id` is NULL)
- **THEN** the State Registry appends the event and projects the task in the same transaction

#### Scenario: System-owned Executor posts an envelope team_id that does not match the task

- **WHEN** the assigned system-owned Executor posts an event whose envelope `team_id` differs from the parent task's immutable `team_id`
- **THEN** the State Registry rejects the request with `403 team_mismatch` without appending the event and without changing projected task state

#### Scenario: Executor retries a task event

- **WHEN** an Executor repeats a task event with the same `event_id`
- **THEN** the State Registry returns the original acceptance without appending a duplicate

#### Scenario: Unassigned team-owned Executor reports running

- **WHEN** a team-owned Executor other than `tasks.executor_id` posts a `running` event
- **THEN** the State Registry rejects the request with `403 not_assigned` without appending any event or changing projected state

#### Scenario: Unassigned system-owned Executor reports running

- **WHEN** a system-owned Executor other than `tasks.executor_id` posts a `running` event
- **THEN** the State Registry rejects the request with `403 not_assigned` without appending any event or changing projected state

#### Scenario: Foreign-team Executor names a task

- **WHEN** an authenticated Executor names a task owned by another team in an event request
- **THEN** the State Registry returns the same non-revealing `404` used for an unknown task and appends no event

### Requirement: State Registry records Executor self events under the Executor's ownership scope

The State Registry SHALL maintain canonical Executor records and append Executor-scoped lifecycle, health, capacity, and running-child-count events separately from task event logs. Every Executor self-event envelope SHALL carry `event_id`, `executor_id`, optional `team_id`, `event_type`, `occurred_at`, and `payload`. When the Executor's `scope = team`, `executor_events.team_id` SHALL be non-null, SHALL be persisted on append, SHALL inherit the authenticated Executor's immutable `team_id`, and State Registry SHALL verify that the envelope `team_id` equals the authenticated Executor's immutable `team_id` before accepting. When the Executor's `scope = system`, `executor_events.team_id` SHALL be NULL on append (matching the system-owned Executor's NULL `team_id`); the envelope `team_id` SHALL be NULL and State Registry SHALL verify that the envelope `team_id` is absent or null before accepting. Every `executor_events` row SHALL have `executor_id NOT NULL`. The Registry SHALL mirror the latest observation onto `executors` in the same transaction and SHALL NOT use observations to gate discovery or claim. Event-denial taxonomy: a team-owned Executor whose envelope `team_id` differs from its immutable service binding SHALL be rejected with `403 team_mismatch`; a system-owned Executor whose envelope `team_id` is non-null SHALL be rejected with `403 team_mismatch`; a foreign-team Executor point identifier SHALL be rejected with the same non-revealing `404` shape used for an unknown identifier.

#### Scenario: Team-owned Executor writes a self event

- **WHEN** an authenticated team-owned Executor posts a lifecycle or capacity event to `POST /v1/executors/{executor_id}/events` with matching envelope `team_id`
- **THEN** State Registry appends it with non-null `executor_id` and matching `team_id` to that same-team Executor's history and transactionally updates observed capacity values

#### Scenario: System-owned Executor writes a self event

- **WHEN** an authenticated system-owned Executor posts a lifecycle or capacity event to `POST /v1/executors/{executor_id}/events` with envelope `team_id` null or absent
- **THEN** State Registry appends it with non-null `executor_id` and persisted `team_id = NULL` and transactionally updates observed capacity values

#### Scenario: Team-owned self event envelope team_id does not match Executor

- **WHEN** an authenticated team-owned Executor posts a self event whose envelope `team_id` differs from the authenticated Executor's immutable `team_id`
- **THEN** the State Registry rejects the request with `403 team_mismatch` without appending the event and without changing projected Executor state

#### Scenario: System-owned self event envelope team_id is non-null

- **WHEN** an authenticated system-owned Executor posts a self event whose envelope `team_id` is non-null
- **THEN** the State Registry rejects the request with `403 team_mismatch` without appending the event and without changing projected Executor state

#### Scenario: Executor names a foreign Executor

- **WHEN** an authenticated Executor posts to an `executor_id` owned by another team
- **THEN** the State Registry returns non-revealing `404` and changes no Executor row or event history

### Requirement: State Registry requires trusted team-scoped API Gateway context

State Registry SHALL accept operator reads, subscriptions, controls, environment writes, and secret writes only from an authenticated trusted API Gateway service identity. Every such request SHALL carry Gateway-verified `operator_id`, immutable `team_id`, and `request_id`; the optional display-only `team_name` is forwarded only when the verified operator team carries one. State Registry SHALL authorize solely with `team_id`; `team_name`, caller-supplied tenant headers, and unauthenticated payload fields SHALL NOT grant or broaden access, and the absence of `team_name` SHALL NOT weaken authorization anchored to `team_id`. Each operator authentication context SHALL represent exactly one team. In addition, the State Registry SHALL expose `/admin/*` endpoints (`POST /admin/teams`, `POST /admin/source-systems`, `POST /admin/task-types`) under an authenticated system-administrator identity only; the Registry SHALL NOT describe the authentication mechanism for that role beyond requiring an authenticated system-administrator identity, and SHALL NOT create or update any team through Executor or listener paths.

#### Scenario: Gateway forwards verified operator context

- **WHEN** trusted API Gateway forwards an operator request with verified `operator_id`, `team_id`, and `request_id`, with or without an optional `team_name`
- **THEN** State Registry evaluates authorization using only `team_id` and retains `team_name` only as display or audit context when present

#### Scenario: Direct client supplies team headers

- **WHEN** a client without the trusted API Gateway service identity supplies `operator_id`, `team_id`, `team_name`, or similar headers directly
- **THEN** State Registry rejects the request without reading or changing protected tenant data

#### Scenario: Admin endpoint requires system-administrator identity

- **WHEN** a caller without an authenticated system-administrator identity submits a `POST /admin/*` request
- **THEN** State Registry rejects the request without persisting or partially creating any team, source system, or task type

### Requirement: State Registry returns non-revealing not-found for foreign resources

For an authenticated listener, Executor, or Gateway request scoped to one team, every task, Executor, event, control, environment, secret, secret version, audit, source system, or task type identifier owned by another team SHALL be treated as absent. State Registry SHALL return the same `404` shape used for an unknown identifier and SHALL NOT reveal existence, ownership, state, metadata, counts, or timing-dependent details. Rejection SHALL append no domain event, mutate no canonical resource, create no control, and disclose no audit entry.

#### Scenario: Same-team operator reads a foreign task identifier

- **WHEN** trusted API Gateway for `team-a` requests a task identifier owned by `team-b`
- **THEN** State Registry returns the same non-revealing `404` as an unknown task and performs no mutation or event append

#### Scenario: Same-team operator writes a foreign environment identifier

- **WHEN** trusted API Gateway for `team-a` attempts to update an environment owned by `team-b`
- **THEN** State Registry returns non-revealing `404`, leaves the environment and secrets unchanged, and performs no decrypt operation

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

State Registry SHALL store operator intervention and cancellation requests as audit-backed control records only when trusted API Gateway context and the target task share the same `team_id`. Each write SHALL use an idempotency key scoped to `(team_id, task_id, operator_id, action)`. Operators SHALL interact only with Web UI and API Gateway. Assigned Executors SHALL read pending controls only for tasks assigned to that Executor (whose `tasks.team_id` is the same as the Executor's persisted team when `scope = team`, or any team when `scope = system` and the task's `team_id` matches the envelope).

#### Scenario: Same-team operator requests cancellation

- **WHEN** an operator requests cancellation through trusted API Gateway for a task in the operator's team
- **THEN** State Registry appends the control and a team-scoped audit entry containing request outcome without secret plaintext

#### Scenario: Operator targets another team's task

- **WHEN** trusted API Gateway context names a task owned by another team
- **THEN** State Registry returns non-revealing `404` and appends no control or task event

### Requirement: State Registry stores team-owned environment definitions

State Registry SHALL accept environment-definition writes only from trusted API Gateway context for the same `team_id`, store immutable team ownership with non-secret `KEY=value` entries and optional project/task applicability, append a team-scoped audit entry, and return a stable environment identifier. Same-team operators manage environments for the team. An environment definition is either team-wide (no task parent) or task-owned (a single `task_id` parent in the same team); in both cases the row's `team_id` is immutable and ownership is enforced through a database-enforced relationship. Project and task scopes SHALL limit applicability inside that team and SHALL NOT grant cross-team access.

#### Scenario: Same-team operator stores an environment definition

- **WHEN** an operator submits valid non-secret environment values through trusted API Gateway context without naming a parent `task_id`
- **THEN** State Registry stores the team-wide definition under that context's immutable `team_id`, appends an audit entry, and returns its environment identifier

#### Scenario: Same-team operator stores a task-owned environment definition

- **WHEN** an operator submits valid non-secret environment values through trusted API Gateway context and names a parent `task_id` that belongs to the same `team_id`
- **THEN** State Registry stores the task-owned definition with the parent `task_id`, appends an audit entry, and returns its environment identifier; only the parent task in the same team may use the definition

#### Scenario: Task parent belongs to a foreign team

- **WHEN** an operator submits an environment definition whose parent `task_id` belongs to another team
- **THEN** State Registry returns the same non-revealing `404 environment_unknown_or_unavailable` shape, performs no decrypt operation, appends no audit entry, and stores no row

#### Scenario: Project scope narrows applicability

- **WHEN** a team-wide environment is scoped to one project
- **THEN** tasks from the same team but another project cannot use it, and no project scope can make it available to another team

### Requirement: State Registry stores team-owned secrets with immutable versions

State Registry SHALL store each logical secret in a team-owned `secrets` row associated with an environment in the same team. It SHALL encrypt every submitted secret value with a local AES-256-GCM authenticated encryption (256-bit data key, unique random 96-bit nonce per encryption, 128-bit authentication tag) using a key provided through configuration, with authenticated associated data that binds at minimum the team identifier, the logical secret identifier, and the secret version. The opaque provider-neutral `key_id` and `key_version` are recorded on the immutable `secret_versions` row; plaintext is never persisted or logged. Same-team operators manage secrets through trusted API Gateway context; optional project/task scope limits applicability within the team. A `secrets` row inherits its scope from its parent environment: when the parent environment is team-wide the secret is team-scoped, and when the parent environment is task-owned the secret is also task-owned and only the parent task in the same team may open it.

#### Scenario: Same-team operator stores a secret

- **WHEN** a valid secret write arrives through trusted API Gateway for a same-team team-wide environment
- **THEN** State Registry encrypts the value with local AES-256-GCM, stores a logical `secrets` row and immutable referenced `secret_versions` row with non-sensitive `key_id` and `key_version`, appends a plaintext-free audit entry, and returns the secret identifier and version

#### Scenario: Same-team operator stores a secret under a task-owned environment

- **WHEN** a valid secret write arrives through trusted API Gateway for a same-team task-owned environment whose parent `task_id` belongs to the same team
- **THEN** State Registry stores the secret under that task-owned environment, encrypts the value with local AES-256-GCM, and only the parent task in the same team may open the resulting `secret_versions`

#### Scenario: Secret value changes

- **WHEN** a same-team operator replaces a secret value
- **THEN** State Registry appends a new encrypted `secret_versions` row referencing the same logical secret with the new `key_id` / `key_version` without modifying or exposing prior plaintext

#### Scenario: Secret targets a foreign environment

- **WHEN** trusted Gateway context attempts to create or replace a secret under another team's environment
- **THEN** State Registry returns non-revealing `404`, performs no encrypt operation, and stores no secret or version

#### Scenario: Startup fails closed when AES-GCM key is missing

- **WHEN** the State Registry starts without a configured 256-bit AES-GCM key
- **THEN** startup fails closed and refuses all secret writes and open-environment reads

#### Scenario: Decryption fails closed when authenticated associated data does not match

- **WHEN** State Registry attempts to decrypt a stored ciphertext using the wrong team, logical secret, or version binding in the associated data
- **THEN** State Registry returns the non-revealing `404 environment_unknown_or_unavailable` shape, performs no logical-secret disclosure, and logs no key material, nonce, ciphertext, plaintext, or individual claim values

### Requirement: State Registry uses local AES-256-GCM authenticated encryption for secret values

State Registry SHALL encrypt every stored secret value with local AES-256-GCM authenticated encryption. The data key SHALL be a configuration-provided 256-bit secret loaded from a secure configuration source, never logged, never persisted alongside ciphertext, and rotated through the opaque `key_id`/`key_version` envelope on the `secret_versions` row. Each encryption call SHALL generate a fresh random 96-bit nonce and SHALL compute a 128-bit authentication tag over the plaintext bound to authenticated associated data that includes the team identifier, the logical secret identifier, and the version. The State Registry SHALL refuse decryption when the loaded key is missing, the key identifier is outside the active window, the associated data binding does not match canonical records, the nonce or tag is invalid, or the decrypted plaintext fails the canonical authorization checks. The Registry SHALL fail closed on startup or runtime error; secret writes and open-environment reads SHALL NOT proceed in any fail-closed condition. Application logs, audit records, and error responses SHALL NOT contain plaintext secret values, key material, key bytes, nonce bytes, ciphertext bytes, authentication tag bytes, individual associated-data values beyond identifier-level metadata, or any derived key bytes. The state-registry service SHALL NOT depend on an external cryptography service to encrypt or decrypt secret values; the local provider accepts the documented `key_id` / `key_version` envelope so a future change can replace it without disturbing the contract. A future change MAY replace the local AES-256-GCM provider with an external provider behind the same `key_id` / `key_version` envelope; that change is not part of this contract.

#### Scenario: Encryption binds team, secret, and version in associated data

- **WHEN** the State Registry encrypts a secret value
- **THEN** the associated data SHALL bind the team identifier, the logical secret identifier, and the secret version, and the resulting ciphertext SHALL be rejected on decryption if any of these do not match the canonical records

#### Scenario: Decryption is denied before any plaintext leaves the Registry

- **WHEN** an authorized open-environment request is in progress
- **THEN** the State Registry SHALL decrypt using the locally loaded 256-bit AES-GCM key under constant-time MAC verification, perform every canonical authorization check before returning plaintext, and never log plaintext, key material, nonce, ciphertext, authentication tag, or associated-data values beyond identifier-level metadata

#### Scenario: Logs and audit never contain secret material

- **WHEN** State Registry encrypts, decrypts, accepts, rejects, or retries any secret write or open-environment read
- **THEN** application logs, audit entries, and error responses SHALL contain no plaintext, no key material, no nonce bytes, no ciphertext bytes, no authentication tag bytes, no individual claim values, and no derived key bytes

### Requirement: State Registry opens environments only for assigned same-team Executors

State Registry SHALL expose `GET /v1/environments/{environment_id}/open?task_id={task_id}` only to the assigned Executor for the referenced task. The request SHALL carry the canonical `task_id` query parameter and the compact three-part signed token in the `X-FlowAI-Scope-Token` request header (the body SHALL NOT carry any scope-token fields). State Registry SHALL reject the request without any decrypt operation or value disclosure when the `task_id` query parameter is absent, malformed, or, after successful MAC verification and canonical claim parsing, does not equal both the payload `task_id` claim and the canonical assigned task, returning the same non-revealing `404 environment_unknown_or_unavailable` shape. The Executor SHALL present its authenticated identity and a signed, unexpired scope token satisfying the "State Registry signs open-environment scope tokens with an allow-listed HMAC and a server-controlled rotating key" requirement. Before decrypting, State Registry SHALL verify that the protected-header `kid` equals the payload `key_id`, the token MAC under the declared allow-listed HMAC algorithm (`HS256`/`HS384`/`HS512`) and the documented active key window for `key_id`, the expected literal `audience` (`state-registry.environment.open`), the `issued_at <= server_now + 30 seconds` and `expiry > issued_at` and `expiry - issued_at <= 5 minutes` window, every token claim (including the project-scope rule for `project_id`: required, nullable only when the canonical environment has no project scope) against canonical records, same-team ownership across Executor, task, environment, and secrets, task assignment, the non-terminal task state, and project/task applicability. On success it SHALL return authorized env-style values and append a plaintext-free team-scoped audit entry. Plaintext SHALL remain in memory only. State Registry SHALL NOT accept a token whose `key_id` is outside the documented active window, SHALL NOT accept a token after terminal task state or Executor unassignment, and SHALL perform the decrypt operation only after every check passes. A same `(task_id, command_id)` retry by the original claiming Executor MAY be allowed within the token's TTL when every check still passes; it SHALL NOT bypass canonical claim, transition, or assignment checks, SHALL NOT extend TTL, and SHALL NOT revive an expired token. Any invalid or unavailable condition — including a missing token, tampered MAC, algorithm outside the allow-listed set, `kid` mismatch with payload `key_id`, `key_id` outside the active window, lifetime exceeding five minutes, expired or premature tokens, audience or canonical-claim mismatch, mismatched `team_id`, terminal task, or not-assigned caller — returns the same non-revealing `404 environment_unknown_or_unavailable` shape with zero decrypt operations and no token plaintext, individual claim values beyond identifier-level metadata, MAC bytes, key material, or derived key bytes in logs, audit entries, or error responses.

#### Scenario: Assigned Executor opens an environment

- **WHEN** the assigned Executor sends `GET /v1/environments/{environment_id}/open?task_id={task_id}` with the compact three-part scope token in the `X-FlowAI-Scope-Token` request header, the protected header carrying `alg` (allow-listed `HS256`/`HS384`/`HS512`), `kid`, and `typ` (`scope-token+json`) with `kid == payload key_id`, and the payload's `team_id`, `project_id` (required, null only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `key_id`, `issued_at`, and `expiry` (`expiry > issued_at`, `expiry - issued_at <= 5 minutes`, `issued_at <= server_now + 30 seconds`) all matching canonical records and the MAC verifying under constant-time comparison
- **THEN** State Registry returns authorized values after local AES-256-GCM decryption and records `team_id`, actor, action, resource, request, and outcome without plaintext or any secret material

#### Scenario: Scope token has a foreign or mismatched claim

- **WHEN** any token claim, signature, audience, `key_id`, expiry, assignment, team, project, task, environment, or Executor binding is invalid
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value

### Requirement: State Registry signs open-environment scope tokens with an allow-listed HMAC and a server-controlled rotating key

State Registry SHALL be the sole issuer and verifier of open-environment scope tokens. The compact three-part wire format SHALL be `<header>.<payload>.<signature>` carried only in the `X-FlowAI-Scope-Token` request header for `GET /v1/environments/{environment_id}/open?task_id={task_id}`. Each token SHALL be signed using an allow-listed HMAC algorithm from the set `HS256`/`HS384`/`HS512` (the "HMAC-SHA-256 or a stronger HMAC" family), keyed with a State Registry-controlled key selected by a `key_id` claim included in the payload. The protected header SHALL carry `alg` (allow-listed), `kid`, and `typ`; the payload SHALL carry `team_id`, `project_id` (a required claim whose value is nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry`, and `key_id`. State Registry SHALL require `expiry > issued_at`, SHALL bound `expiry - issued_at <= 5 minutes`, SHALL require `issued_at <= server_now + 30 seconds` (premature-beyond-skew rejection), SHALL require `audience` to match the documented literal identifier, SHALL verify that the protected-header `kid` equals the payload `key_id` before any MAC computation, SHALL recompute the signature under the declared allow-listed algorithm and compare it under constant-time comparison before any other check, SHALL accept only `key_id` values inside the documented active key window for rotation, SHALL perform canonical claim verification (presence, types, encoding, allowed values, and the project-scope rule for `project_id`) before any team, assignment, or applicability check, and SHALL never log token plaintext, individual claims, MAC bytes, key material, or derived key bytes. A token SHALL be valid only when the calling authenticated Executor identity is the currently assigned Executor for the referenced task (`scope = team` matching the Executor's team and the parent task's `team_id`; `scope = system` matching the parent task's `team_id`) and the referenced task is in a non-terminal state; any other identity, a terminal task, or an unassigned Executor fails before the local AES-256-GCM decrypt operation.

#### Scenario: Token envelope carries the documented claims

- **WHEN** State Registry issues an open-environment scope token
- **THEN** the envelope includes non-null `team_id`, `task_id`, `environment_id`, `executor_id`, `audience`, `key_id`, `issued_at`, and `expiry`, and the required `project_id` claim whose value is null only when the canonical environment has no project scope

#### Scenario: Protected-header kid must equal payload key_id

- **WHEN** the protected header `kid` differs from the payload `key_id`
- **THEN** State Registry rejects the request with the same non-revealing `404 environment_unknown_or_unavailable` response used for every other invalid or unavailable case, performs no MAC comparison, and performs no decrypt operation

#### Scenario: Algorithm outside the allow-listed HMAC set is rejected

- **WHEN** an Executor presents a token whose protected header `alg` is anything other than `HS256`, `HS384`, or `HS512`
- **THEN** State Registry rejects the request with the same non-revealing `404 environment_unknown_or_unavailable` response used for every other invalid or unavailable case and performs no decrypt operation

#### Scenario: MAC is verified under constant-time comparison

- **WHEN** an Executor presents an open-environment scope token
- **THEN** State Registry compares the MAC using a constant-time comparison and rejects the request when the comparison fails before any further check or any decrypt operation

#### Scenario: Expiry is bounded by five minutes after issued_at

- **WHEN** a token's `expiry` is more than five minutes after `issued_at`
- **THEN** State Registry rejects the token as malformed and never uses it for verification

#### Scenario: Expiry must be strictly later than issued_at

- **WHEN** a token's `expiry` is not strictly later than `issued_at`
- **THEN** State Registry rejects the token with the same non-revealing `404 environment_unknown_or_unavailable` response and performs no decrypt operation

#### Scenario: Issued_at must not be premature beyond the clock-skew window

- **WHEN** a token's `issued_at` is in the future beyond `server_now + 30 seconds`
- **THEN** State Registry rejects the token with the same non-revealing `404 environment_unknown_or_unavailable` response and performs no decrypt operation

#### Scenario: Audience must match the literal expected identifier

- **WHEN** a token's `audience` differs from the documented literal identifier `state-registry.environment.open`
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value

#### Scenario: key_id outside the documented active window is rejected

- **WHEN** a token's `key_id` does not fall inside the documented active key window for HMAC rotation
- **THEN** State Registry rejects the token and performs no decrypt operation

#### Scenario: Canonical claim verification fails

- **WHEN** a token has unexpected, missing, or malformed claim fields, types, or allowed values, including a null `project_id` for a project-scoped environment or any non-null mismatch with canonical records
- **THEN** State Registry rejects the request before any team, assignment, applicability, or decrypt operation

#### Scenario: Token replay by a different identity is denied

- **WHEN** an authenticated Executor different from the original assigned Executor presents a token whose claims still pass canonical checks
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value and records an audit entry without token plaintext

#### Scenario: Token after terminal task state is denied

- **WHEN** the referenced task is in a terminal state and the assigned Executor presents a still-valid token
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value and performs no decrypt operation

#### Scenario: Token after Executor unassignment is denied

- **WHEN** the Executor is no longer the recorded `tasks.executor_id` and that Executor presents a still-valid token
- **THEN** State Registry rejects the request without decrypting or returning any environment or secret value and performs no decrypt operation

#### Scenario: Retry within TTL by the same assigned identity MAY be allowed

- **WHEN** the same assigned Executor presents the same token again before `expiry`
- **THEN** State Registry MAY return the same authorized values, but SHALL still verify the MAC, `key_id`, audience, every canonical claim, task non-terminal state, and current assignment on each retry and SHALL NOT extend TTL or revive an expired token

#### Scenario: Operational logs and audit never contain token plaintext or key material

- **WHEN** State Registry processes, accepts, rejects, retries, or logs any scope-token request
- **THEN** application logs, audit entries, and error responses contain no token plaintext, individual claim values beyond permitted identifier-level metadata, MAC bytes, key material, or derived key bytes

### Requirement: State Registry records plaintext-free team audit entries

For each auditable authorized operator action, control, environment or secret mutation, and open-environment access, State Registry SHALL append an immutable audit entry containing `team_id`, actor identity and type, action, resource type and identifier, `request_id`, outcome, and timestamp. Audit entries, application logs, event payloads, and error responses SHALL NOT contain secret plaintext, decrypted environment values, key material, nonce bytes, ciphertext bytes, authentication tag bytes, or individual claim values beyond identifier-level metadata. Audit reads SHALL be filtered by trusted `team_id` before pagination or aggregation.

#### Scenario: Authorized secret write is audited

- **WHEN** a same-team operator creates a secret through trusted API Gateway context
- **THEN** the audit entry identifies the team, operator, action, secret resource, Gateway request, and outcome without containing the plaintext value, ciphertext, nonce, key, or authentication tag

#### Scenario: Assigned Executor opens an environment

- **WHEN** an assigned Executor successfully opens an environment
- **THEN** the audit entry identifies the team, Executor actor, environment resource, request, and success outcome without containing returned values, secret material, key, nonce, ciphertext, or authentication tag

### Requirement: State Registry enforces team-bound service identities with conditional Executor binding

State Registry SHALL authenticate listener, Executor, and API Gateway service identities. Each listener identity SHALL be bound to exactly one immutable authorized `team_id` and to exactly one immutable authorized `source_system_id`; listener source identifiers and submitted `team_id` SHALL be bound to the authenticated listener. Each Executor identity SHALL be bound to either exactly one immutable authorized `team_id` (`scope = team`) or to no `team_id` (`scope = system`). Listener and team-owned Executor registration, team and tag authorization, discovery, claim, task-event writes, Executor-event writes, control reads, and environment opens SHALL be bound to the authenticated service identity. System-owned Executor registration, discovery, claim, task-event writes, and Executor-event writes SHALL be bound to the authenticated Executor identity and SHALL use `tasks.team_id` as the team predicate for any per-task ownership check; the parent task's `team_id` is immutable for the row's lifetime. API Gateway-only reads, subscriptions, environment/secret writes, and controls SHALL require the trusted Gateway identity and verified operator context. `/admin/*` endpoints SHALL require an authenticated system-administrator identity. Untrusted client headers SHALL NOT establish a team.

#### Scenario: Caller impersonates another service or team

- **WHEN** a caller uses another listener, Executor, Gateway, or `team_id` without the corresponding trusted service credential and authorization
- **THEN** State Registry rejects the request without revealing or changing protected state

### Requirement: State Registry remains independent from execution and identity management

State Registry SHALL NOT execute tasks, control Docker containers or Kubernetes Pods, issue bearer tokens, manage team membership, implement team CRUD UI, implement project-level RBAC, expose plaintext secrets to operators, listeners, API Gateway, or unassigned Executors, or centrally decide Executor scheduling or capacity. Executors SHALL retain runtime lifecycle and local capacity enforcement. Every task and operator authentication context SHALL represent exactly one team; an Executor authentication context SHALL represent either exactly one team (`scope = team`) or no team (`scope = system`). The State Registry SHALL accept team creation, source-system creation, and task-type creation only through `/admin/*` under an authenticated system-administrator identity; it SHALL NOT accept those creations through any listener, Executor, Gateway, or operator path. Image strings are not team-owned resources; two tasks from different teams MAY reference the same image string without any cross-team authority, and equality or reuse of image strings SHALL NOT grant, broaden, or weaken any tenant authorization.

#### Scenario: Claimed task references an environment

- **WHEN** an Executor successfully claims a task that references an applicable environment
- **THEN** the claim response includes only the environment identifier, team-bound scope token, and resolved image; values remain unavailable until the assigned Executor calls the open-environment API

#### Scenario: Project scope is evaluated

- **WHEN** a team-owned environment has project or task applicability metadata
- **THEN** State Registry uses it only to narrow use within that team and does not treat it as project RBAC or cross-team authorization

#### Scenario: Image equality across teams grants no authority

- **WHEN** `team-a` and `team-b` both store or reference the same opaque image string on their tasks, teams, source systems, or task types
- **THEN** neither team gains, broadens, or weakens any tenant authorization because of the shared image reference; image strings are not team-owned resources

### Requirement: State Registry uses mutually authenticated encrypted transport

All listener, Executor, and API Gateway connections to State Registry SHALL use encrypted transport. Service HTTP and WebSocket callers SHALL use mutually authenticated TLS identities bound to service role and team authorization rules; the database client SHALL validate server identity and certificate chains.

#### Scenario: Caller uses an untrusted transport identity

- **WHEN** a caller presents no trusted client identity, an expired identity, or an identity for another service role
- **THEN** State Registry rejects the connection before reading or changing protected data

### Requirement: State Registry requires a default image at team registration

State Registry SHALL persist a REQUIRED opaque `default_image` on each `teams` row set through `POST /admin/teams`. Operators SHALL NOT edit or clear `default_image` through any operator or admin path; the only way to change the team's `default_image` is to register a new team. `default_image` SHALL be an opaque container image reference consisting of a registry repository path plus an immutable digest; State Registry SHALL NOT resolve, pull, mutate, or verify the digest and SHALL NOT use it for any tenant authorization decision. Because every team has a required `default_image`, image resolution at claim SHALL always succeed and the Registry SHALL always return a resolved image in the claim response; the Executor SHALL NOT need a local fallback image.

#### Scenario: Required team default image is always present

- **WHEN** the State Registry processes any claim
- **THEN** the parent team's `default_image` SHALL be non-null and SHALL always participate in image resolution

### Requirement: State Registry persists per-task, per-task-type, and per-source-system image overrides

State Registry SHALL persist an OPTIONAL `image` override on each `tasks` row (per-task override), an OPTIONAL `default_image` on each `task_types` row (per-task-type override), and an OPTIONAL `default_image` on each `source_systems` row (per-source-system override). A listener submission MAY include `image`; if present it SHALL be an opaque container image reference consisting of a registry repository path plus an immutable digest. State Registry SHALL verify the submission's immutable `team_id` against the authenticated listener authorization, SHALL store the `image` together with the canonical task in the same ingestion transaction, and SHALL NOT use any of these image values for tenant authorization. None of these image values SHALL be unique to a team: image strings are not team-owned resources, two teams MAY reference the same image, and image equality or reuse SHALL NOT grant, broaden, or weaken any tenant authorization.

#### Scenario: Listener submits a task image override

- **WHEN** an authorized listener submits a new task with a digest-bearing `image` reference under its immutable authorized `team_id`
- **THEN** State Registry persists the task row with the override `image` before transitioning the task out of `pending`; a later listener retry of the same `(team_id, source_system_id, source_id)` SHALL NOT replace or clear `image`

#### Scenario: Image override is immutable after ingestion

- **WHEN** an authorized listener repeats a previously seen `(team_id, source_system_id, source_id)` and includes an `image` value that differs from the canonical task
- **THEN** State Registry returns the existing canonical task unchanged, leaves the original `image` in place, appends no event, and creates no new task

#### Scenario: Image equality across teams grants no authority

- **WHEN** `team-a` and `team-b` both reference the same opaque image string on a task, task type, or source system
- **THEN** neither team gains or broadens access; image strings carry no team authority

### Requirement: State Registry resolves the effective image at claim by four-level precedence

On atomic claim, State Registry SHALL resolve an effective image reference for the claimed task using this exact precedence:

1. The canonical task's stored `image` override if non-NULL.
2. Otherwise, the `default_image` of the registered `task_type` referenced by the task if non-NULL.
3. Otherwise, the `default_image` of the registered `source_system` referenced by the task if non-NULL.
4. Otherwise, the parent team's stored `default_image` (always required at team registration and therefore always present).

Because the team default is required, resolution SHALL always succeed; the Registry SHALL always persist `tasks.resolved_image` and `image_source` in the same transaction that appends the `created` event on claim. `image_source` SHALL be one of `task_override`, `task_type_default`, `source_system_default`, `team_default`. The Registry SHALL include the resolved image in the claim response and SHALL return it to the claiming Executor; the Executor SHALL use `resolved_image` verbatim and SHALL NOT substitute any local image, perform any fallback, or consult any other source. Image strings are not team-owned resources: the same resolved image MAY be reused across teams without granting any tenant authority.

#### Scenario: Task image override wins over task-type, source-system, and team default

- **WHEN** an eligible Executor claims a `pending` task whose stored `image` is non-NULL while the task-type, source-system, and team `default_image` values are also non-NULL
- **THEN** State Registry returns `200 claimed`, persists `resolved_image` equal to the task's stored `image`, sets `image_source = task_override`, and includes `resolved_image` and `image_source` in the claim response

#### Scenario: Task-type default applies when no task override

- **WHEN** an eligible Executor claims a `pending` task whose stored `image` is NULL but the referenced `task_type` has a non-NULL `default_image`
- **THEN** State Registry returns `200 claimed`, persists `resolved_image` equal to the task-type `default_image`, sets `image_source = task_type_default`, and includes `resolved_image` and `image_source` in the claim response

#### Scenario: Source-system default applies when task and task-type are absent

- **WHEN** an eligible Executor claims a `pending` task whose stored `image` is NULL, the referenced `task_type` has NULL `default_image`, and the referenced `source_system` has a non-NULL `default_image`
- **THEN** State Registry returns `200 claimed`, persists `resolved_image` equal to the source-system `default_image`, sets `image_source = source_system_default`, and includes `resolved_image` and `image_source` in the claim response

#### Scenario: Team default applies as the final source

- **WHEN** an eligible Executor claims a `pending` task whose task, task-type, and source-system image values are all NULL while the parent team `default_image` is non-NULL (always present by registration)
- **THEN** State Registry returns `200 claimed`, persists `resolved_image` equal to the team's `default_image`, sets `image_source = team_default`, and includes `resolved_image` and `image_source` in the claim response

#### Scenario: Resolved image is immutable from claim onward

- **WHEN** a task has been claimed and `tasks.resolved_image` and `tasks.image_source` have been persisted in the same transaction as the first `created` event
- **THEN** any later action — restart, re-read through trusted Gateway context, creation of unrelated task types or source systems, or creation of a new team registration — does NOT change the claimed task's `resolved_image` or `image_source`, does NOT alter the projected task state or events, and does NOT mutate the FIRST `created` event; the contract defines no path to mutate an already-claimed task's image record, and any future migration of an existing `resolved_image` would require an explicit change to this contract

#### Scenario: Resolved image survives a Registry restart

- **WHEN** the State Registry is restarted and the claimed task is read back through trusted Gateway context
- **THEN** the read response returns the same `resolved_image` value that was persisted at claim time

#### Scenario: Resolved image is reused across teams without granting authority

- **WHEN** `team-a` and `team-b` both claim tasks whose `resolved_image` evaluates to the same opaque image string
- **THEN** no team gains or broadens authority over the other; image equality is not team authority

### Requirement: State Registry enforces Executor scope as a registration-time decision

State Registry SHALL treat every Executor's `scope` as immutable from registration onward and SHALL derive every team predicate for that Executor from the recorded `scope`. The Registry SHALL persist `executors.scope` on the row and SHALL refuse a re-registration that names a different `scope` than the original. The Registry SHALL NOT infer scope from runtime data, SHALL NOT auto-promote a team-owned Executor to system-owned, and SHALL NOT allow a system-owned Executor to claim a `team_id`. When `scope = system`, every team predicate in discovery, claim, task events, and self events SHALL evaluate against the parent task's `team_id` rather than the Executor's `team_id`; when `scope = team`, the team predicate SHALL continue to evaluate against the Executor's immutable `team_id` AND the parent task's `team_id` (which must match). Audit entries that record an Executor's actions SHALL include `executor_scope` so operators can attribute cross-team actions to the system-owned Executor that performed them.

#### Scenario: Re-registration cannot change scope

- **WHEN** an existing Executor submits a re-registration with a different `scope` value than the originally recorded one
- **THEN** the State Registry rejects the re-registration without changing the existing record

#### Scenario: Audit records Executor scope on task events

- **WHEN** a system-owned Executor appends a task event
- **THEN** the audit entry identifies the team (the task's team), the executor, the action, the resource, the request, the outcome, and the executor's `scope = system`, without containing secret plaintext or task payload values

#### Scenario: Audit records Executor scope on self events

- **WHEN** either a team-owned or a system-owned Executor appends a self event
- **THEN** the audit entry identifies the executor, the action, the resource, the request, the outcome, and the executor's `scope` without containing secret plaintext or payload values
