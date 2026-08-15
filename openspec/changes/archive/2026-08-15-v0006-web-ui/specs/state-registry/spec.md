## ADDED Requirements

### Requirement: State Registry exposes the v0006 UI API surface

State Registry SHALL implement the operations declared by
`specs/state-registry/openapi/ui-operator.openapi.yaml`. Every team-scoped
operation SHALL carry the stable selected `team_id` in the path
`/ui/v1/teams/{team_id}/...`; no operation SHALL fall back to the first team,
a cached team, or a deployment default. The surface SHALL include dashboard
statistics; task list/detail/lifecycle/control/log reads; cancellation;
Executor list/detail/event/active-task reads; launch-parameter CRUD and
revision history; logical-secret create/replace/delete and version history;
team audit; and the team live stream. Collection operations SHALL support the
documented filters, opaque cursor, and `limit` values `10`, `25`, `50`, or
`100`, defaulting to `10`.

Task history SHALL accept an optional strict `date=YYYY-MM-DD` filter. State
Registry SHALL interpret it as the UTC half-open range from that day's
midnight through the following midnight and SHALL apply both bounds together
with team ownership before ordering, pagination, counts, or serialization.

#### Scenario: UI requests one task-history day

- **WHEN** task history requests `date=2026-08-15`
- **THEN** State Registry returns only same-team tasks with `ingested_at >= 2026-08-15T00:00:00Z` and `ingested_at < 2026-08-16T00:00:00Z`

This API contract accepts `team_id` as request data for the unauthenticated
v0006 implementation stage. It SHALL NOT claim that the identifier is
authenticated, derived from Keycloak, or authorized by membership. Those
security properties are outside this change and are replaced by the auth
change. State Registry SHALL still apply the supplied `team_id` consistently
before resource lookup, filtering, pagination, mutation, or streaming.

#### Scenario: UI changes selected team

- **WHEN** a later request uses a different valid `team_id` path value
- **THEN** State Registry evaluates the complete operation in that new team context and uses no state from the previously selected team

#### Scenario: UI requests an unsupported page size

- **WHEN** a collection request supplies a `limit` other than `10`, `25`, `50`, or `100`
- **THEN** State Registry returns the documented validation error without reading or returning collection rows

#### Scenario: UI names a resource outside the selected team

- **WHEN** a team-scoped operation names a task, Executor, environment, secret, revision, control, log, or audit resource not owned by the path `team_id`
- **THEN** State Registry returns the documented non-revealing not-found response

### Requirement: State Registry returns complete selected dashboard calendars

State Registry SHALL accept dashboard selections only as an ISO week
`week=YYYY-Www` with `period=week` or a calendar month `month=YYYY-MM` with
`period=month`. An omitted selection SHALL resolve to the current UTC week or
month. Future and malformed selections SHALL be rejected. A selected week
SHALL use Monday 00:00 UTC through the following Monday as a half-open range
and return exactly seven ordered daily buckets. A selected month SHALL use its
first day through the first day of the following month as a half-open range
and return exactly 28, 29, 30, or 31 ordered daily buckets. Every bucket SHALL
contain zero-initialized counts for `pending`, `created`, `running`,
`finished`, and `failed`; days without tasks, including future days inside the
current selected range, SHALL remain present. Team ownership and both range
bounds SHALL be applied before aggregation.

#### Scenario: Selected week contains sparse activity

- **WHEN** a selected ISO week contains tasks on only one day
- **THEN** State Registry returns all seven Monday-through-Sunday buckets in order and the other six buckets have zero totals

#### Scenario: Selected month has no tasks

- **WHEN** a selected calendar month has no same-team tasks
- **THEN** State Registry returns every day of that month in order with all five lifecycle counts initialized to zero

#### Scenario: Selection crosses a calendar boundary

- **WHEN** the selected ISO week crosses an ISO year boundary or the selected month is February in a leap year
- **THEN** State Registry derives the exact UTC half-open range and returns the corresponding seven or twenty-nine daily buckets

### Requirement: State Registry resolves scoped launch parameters

State Registry SHALL store environment definitions as launch-parameter
definitions with exactly one scope from `global`, `team`, or `task_type`.
A global definition has no team or task-type binding and is
managed only through an authenticated system-administrator surface outside
this change. A team definition has one immutable `team_id`; a task-type
definition has one immutable `team_id` and one `task_type_id` owned by that
team. UI API writes in v0006 SHALL create or replace only team- or
task-type-scoped definitions. State Registry SHALL reject any environment
definition, ordinary env value, or logical secret binding addressed by
`task_id`.

For a claimed task, State Registry SHALL merge applicable launch parameters
from broadest to narrowest in this order: global, team, task type. A
narrower definition SHALL replace a same-named ordinary env value, logical
secret reference, or optional `image` from a broader definition. State
Registry SHALL reject ambiguous duplicate active definitions for the same
scope key. Scope matching and team authorization SHALL occur before merge,
image resolution, counts, cursors, or serialization.

#### Scenario: Task-type parameters apply inside one team

- **WHEN** a team-owned task references a `task_type_id` with an active same-team task-type launch-parameter definition
- **THEN** State Registry applies that definition after global and team definitions

#### Scenario: Task identifier is not a launch-parameter scope

- **WHEN** a caller attempts to bind launch parameters, an ordinary env value, or a logical secret to a `task_id`
- **THEN** State Registry rejects the request without storing a definition, revision, secret, or audit mutation

#### Scenario: Task type belongs to another team

- **WHEN** an operator attempts to bind launch parameters to a `task_type_id` owned by another team
- **THEN** State Registry returns the same non-revealing `404 environment_unknown_or_unavailable` shape as unknown and stores no definition, revision, secret, or audit entry

### Requirement: State Registry freezes and opens task launch-parameter snapshots

State Registry SHALL atomically freeze the merged `global → team → task_type`
ordinary values and the exact selected logical-secret version references when
an Executor successfully claims a task. The claim response SHALL expose
`launch_parameters = true` and a task-bound `scope_token` when that immutable
snapshot contains at least one ordinary value or secret reference; it SHALL
not expose an `environment_id`, secret plaintext, ciphertext, nonce,
authentication tag, key identifier, or provider metadata.

The assigned Executor SHALL open the snapshot through
`GET /v1/tasks/{task_id}/launch-parameters/open` with the claim-issued scope
token. State Registry SHALL verify the token, current task assignment,
Executor identity, team ownership, and non-terminal execution state before
decrypting only the snapshotted secret versions. Later definition edits,
secret replacements, definition deletion, or secret revocation SHALL affect
later claims only and SHALL NOT mutate an already committed task snapshot.

#### Scenario: Definition changes after claim

- **WHEN** an assigned Executor opens a task snapshot after the operator changes an applicable env value or replaces a referenced secret
- **THEN** State Registry returns the ordinary values and exact secret versions frozen by the successful claim

#### Scenario: Unassigned Executor attempts snapshot open

- **WHEN** a different Executor presents the task identifier or scope token
- **THEN** State Registry returns the same non-revealing not-found response and performs no decryption

#### Scenario: Operator attempts global launch-parameter mutation

- **WHEN** a v0006 UI API request creates, replaces, or deletes a global definition
- **THEN** State Registry rejects it without mutation because global administration is outside the operator surface

### Requirement: State Registry resolves launch-parameter image overrides

State Registry SHALL support one optional opaque digest-bearing `image`
override on each launch-parameter definition and SHALL resolve the claimed
task image using this exact precedence:

1. The canonical task's immutable `image` override.
2. The merged applicable launch-parameter `image`, selected by task type,
   team, then global specificity.
3. The referenced task type's `default_image`.
4. The referenced source system's `default_image`.
5. The owning team's required `default_image`.

State Registry SHALL persist the resolved image and source atomically at
claim. The source SHALL identify the winning task override,
launch-parameter scope, task-type default, source-system default, or team
default. An image string SHALL NOT grant or broaden tenant authority.

#### Scenario: Task-type launch parameters override catalog defaults

- **WHEN** a task has no direct image, its applicable task-type launch parameters contain an image, and task-type, source-system, and team defaults are present
- **THEN** State Registry persists the launch-parameter image and records the task-type launch-parameter scope as its source

#### Scenario: Team default remains the final fallback

- **WHEN** no task, applicable launch-parameter, task-type, or source-system image is present
- **THEN** State Registry resolves the owning team's required default image

### Requirement: State Registry stores immutable launch-parameter revisions

State Registry SHALL append an immutable revision for every successful create,
replace, or delete of a launch-parameter definition. Revision numbers SHALL
start at `1` and increase by exactly one per definition. Each revision SHALL
contain the definition identifier, scope and immutable bindings, revision
number, name, complete non-secret env snapshot, optional image, deletion
marker, trusted actor and request identifiers, audit identifier, and creation
time. It SHALL contain no secret plaintext, ciphertext, nonce, authentication
tag, or decryption material.

State Registry SHALL expose selected-team UI reads at
`GET /v1/environments/{environment_id}/revisions` and
`GET /v1/environments/{environment_id}/revisions/{revision}`. The collection
SHALL use opaque cursor pagination and deterministic `revision DESC`
ordering. Global revision reads SHALL NOT be exposed through the v0006
operator UI surface.

#### Scenario: Operator changes an ordinary env value

- **WHEN** a same-team UI API request replaces launch parameters with one ordinary env value changed
- **THEN** State Registry atomically appends the next revision and audit entry, updates the current projection, and returns history from which the key's changes can be derived

#### Scenario: Operator deletes launch parameters

- **WHEN** a same-team UI API request deletes a definition
- **THEN** State Registry appends a tombstone revision before marking the current projection deleted and preserves all earlier revisions

#### Scenario: Deleted ordinary env value is absent from later resolutions

- **WHEN** a same-team UI API request replaces a definition without an existing ordinary env key or deletes the definition containing it
- **THEN** State Registry appends the next immutable revision, removes the key from the current projection, and excludes it from every launch-parameter resolution performed after the committed mutation

#### Scenario: Operator reads foreign history

- **WHEN** a UI API path names a definition or revision owned by another team
- **THEN** State Registry returns the same non-revealing `404` as unknown and reveals no revision, count, cursor, timing, or secret material

### Requirement: State Registry records and streams task-control events

State Registry SHALL append an immutable first control event with
`event_type = task.control.requested` and `status = pending` in the same
transaction that accepts a task control and its audit entry. State Registry
SHALL expose an Executor-only append operation at
`POST /v1/tasks/{task_id}/controls/{control_id}/events`. Only the
authenticated Executor assigned to the task may append `acknowledged`,
`completed`, or `failed` results. Appends SHALL be idempotent by
`control_event_id`, strictly ordered by
`(occurred_at, control_event_id)`, and atomically update the control
projection and audit.

State Registry SHALL expose the ordered selected-team UI read
`GET /v1/tasks/{task_id}/controls/{control_id}/events` with opaque cursor
pagination. It SHALL publish every committed control event to
`/v1/events/stream` as a team-bound `control_event` frame. Control events
SHALL NOT append task lifecycle events or introduce a cancellation lifecycle
state.

#### Scenario: Cancellation request enters the task feed

- **WHEN** State Registry accepts a same-team operator cancellation control
- **THEN** it atomically stores the control, audit entry, and pending requested event and publishes that event to the team's live stream after commit

#### Scenario: Assigned Executor reports the result

- **WHEN** the assigned Executor appends ordered acknowledged and completed or failed control events
- **THEN** State Registry stores and publishes each result, updates the control projection, and leaves the canonical task lifecycle unchanged

#### Scenario: Unauthorized caller reports a result

- **WHEN** a browser, unassigned Executor, or foreign-team Executor calls the control-event append operation
- **THEN** State Registry rejects it without changing the control, appending an event, or publishing a frame

### Requirement: State Registry stores and streams ordered task logs

State Registry SHALL accept task-log chunks only from the authenticated
Executor currently assigned to the task. Each append SHALL carry an
idempotent `log_chunk_id`; State Registry SHALL assign a strictly increasing
task-local `log_offset` and a `stream` from `work` or `reasoning`, persist
committed chunks, and publish them as
team-bound `task_log` frames without converting them into lifecycle or
control events. It SHALL expose same-team ordered replay by opaque cursor and
live continuation through the operator read surface. Ownership SHALL
be checked before replay, cursor handling, counts, subscription, or content
serialization.

The selected-team aggregate stream SHALL also publish one stable
`task_committed` frame for each successfully ingested task, including a
pending task with no lifecycle event. A client SHALL establish its initial
`after` boundary before opening the WebSocket and SHALL reconnect with the
last committed `(occurred_at, frame_id)` pair so the connection handshake
cannot lose a commit and replay cannot change derived dashboard counts.

The `reasoning` stream SHALL contain only reasoning text explicitly emitted
by the running agent as execution output. State Registry SHALL NOT infer,
request, or expose a model provider's hidden chain of thought or other
provider-internal reasoning state.

#### Scenario: Assigned Executor appends output

- **WHEN** the assigned Executor appends a new log chunk for its running task
- **THEN** State Registry commits it once with its declared `work` or `reasoning` stream, assigns the next task-local offset, and publishes one team-bound `task_log` frame

#### Scenario: Operator resumes a task log

- **WHEN** a same-team UI read supplies the last committed opaque cursor
- **THEN** State Registry returns later chunks in ascending offset order without gaps or duplicates before continuing live delivery

#### Scenario: Unauthorized log access

- **WHEN** an unassigned Executor appends or a foreign-team operator reads a task log
- **THEN** State Registry returns a non-revealing denial and exposes no content, cursor, count, timing, or existence signal

## MODIFIED Requirements

### Requirement: State Registry stores team-owned environment definitions

State Registry SHALL store environment definitions as launch-parameter
definitions. Every definition contains ordinary non-secret `KEY=value`
entries, logical secret references, and an optional image override, with
exactly one scope from `global`, `team`, or `task_type`.
Global definitions are administrator-owned and have no team binding; their
write API is outside v0006. Team definitions carry one immutable `team_id`
and apply to every task type and task in that team. Task-type definitions
carry one immutable `team_id` and same-team `task_type_id`.

The v0006 UI API SHALL create, read, replace, and delete only
team- and task-type-scoped definitions in their configured team.
State Registry SHALL authorize every referenced team and task type,
environment, and secret before mutation, merge, history shaping, image
resolution, or decryption. Every successful mutation SHALL append a
team-scoped plaintext-free audit entry and an immutable definition revision.
For ordinary env values and logical secrets, State Registry SHALL accept the
caller-supplied env-style key as explicit data, validate it, persist it
unchanged, and use that same key during merge and runtime resolution. It SHALL
NOT derive a key from a display name or generate or normalize one silently.

#### Scenario: Same-team operator stores team-wide launch parameters

- **WHEN** an operator submits ordinary env values, logical secrets, or an image without a task-type binding
- **THEN** State Registry stores one team-scoped definition that applies to every task type and task owned by the trusted team

#### Scenario: Operator-supplied key is preserved

- **WHEN** a same-team operator creates an ordinary env value or logical secret with a valid explicit key
- **THEN** State Registry stores and resolves the exact supplied key without deriving, renaming, or normalizing it

#### Scenario: Same-team operator stores task-type launch parameters

- **WHEN** an operator binds a definition to a `task_type_id` owned by the trusted team
- **THEN** State Registry stores the definition with immutable team and task-type ownership and applies it only to tasks of that type inside the same team

#### Scenario: Operator attempts task-scoped launch parameters

- **WHEN** an operator supplies a `task_id` binding for a definition, ordinary env value, or logical secret
- **THEN** State Registry rejects the request without mutation because `task_id` is not a supported launch-parameter scope

#### Scenario: Referenced task type belongs to another team

- **WHEN** an operator submits a task-type binding owned by another team
- **THEN** State Registry returns the same non-revealing `404 environment_unknown_or_unavailable` as unknown and stores no definition, revision, secret, or audit entry

#### Scenario: Operator attempts global mutation

- **WHEN** a v0006 UI API request attempts to create, replace, or delete a global definition
- **THEN** State Registry rejects the request without mutation

### Requirement: State Registry stores team-owned secrets with immutable versions

State Registry SHALL store each logical secret under one launch-parameter
definition and SHALL inherit that definition's global, team, or task-type
scope. Each submitted secret value SHALL create a new immutable encrypted
version using the configured cryptography provider and associated data that
binds at minimum the owning scope, logical secret identifier, and version.
Plaintext SHALL never be persisted, returned, or logged.

During launch-parameter resolution, State Registry SHALL merge logical secret
references from global to team to task type. A narrower same-named
logical secret SHALL replace the broader reference only after all ownership
and scope checks succeed. State Registry SHALL decrypt only the final resolved
references for an assigned Executor presenting a valid task-bound scope token.
Operator responses and revision history SHALL contain only logical references
and non-sensitive version metadata.

#### Scenario: Operator stores a team-wide secret

- **WHEN** a same-team operator creates a secret under team-scoped launch parameters
- **THEN** State Registry creates the logical secret and first encrypted version, audits the write without plaintext, and makes the reference applicable to every launch in that team unless a narrower scope overrides it

#### Scenario: Task-type secret overrides a team secret

- **WHEN** same-named logical secrets exist in applicable team and task-type definitions
- **THEN** State Registry resolves the task-type reference for tasks of that type without exposing either value to the operator

#### Scenario: Secret value changes

- **WHEN** a same-team operator replaces a logical secret value
- **THEN** State Registry appends a new immutable encrypted version without modifying or exposing prior plaintext

#### Scenario: Revoked secret is absent from later resolutions

- **WHEN** a same-team operator deletes a logical secret reference
- **THEN** State Registry appends plaintext-free secret and definition history, removes the reference from the current projection, and neither resolves nor decrypts that logical secret for any task claimed after the committed revocation

#### Scenario: Secret targets a foreign definition

- **WHEN** a UI API path attempts to create, replace, or delete a secret under another team's definition
- **THEN** State Registry returns non-revealing `404`, performs no encrypt or decrypt operation, and stores no secret, version, revision, or audit entry

#### Scenario: Global secret administration uses an operator route

- **WHEN** a v0006 UI API request attempts to mutate a secret under global launch parameters
- **THEN** State Registry rejects it because global administration is outside v0006

### Requirement: State Registry opens environments only for assigned same-team Executors

State Registry SHALL replace definition-addressed environment opening with
task-addressed opening at
`GET /v1/tasks/{task_id}/launch-parameters/open`. The assigned Executor SHALL
present the task-bound `scope_token` returned by claim. State Registry SHALL
open only the immutable merged snapshot captured for that task and SHALL NOT
accept an `environment_id`, project scope, task-owned definition, or sibling
task as authority.

#### Scenario: Assigned Executor opens the immutable task snapshot

- **WHEN** the assigned Executor calls the task launch-parameter open endpoint with the claim-issued token
- **THEN** State Registry returns the merged ordinary env values and decrypted logical-secret values for that task without returning definition identifiers or secret metadata

#### Scenario: Definition-addressed and task-scoped opening are unavailable

- **WHEN** a caller supplies an environment definition identifier, a removed task-owned definition, a foreign task, a sibling task, an unassigned Executor, or an invalid token
- **THEN** State Registry returns the same non-revealing `404 launch_parameters_unknown_or_unavailable` response and performs no unauthorized decrypt

### Requirement: State Registry signs open-environment scope tokens with an allow-listed HMAC and a server-controlled rotating key

State Registry SHALL sign a compact task launch-parameter scope token at
successful claim only when an immutable merged snapshot exists. The token
SHALL bind `team_id`, `task_id`, `executor_id`, audience, key id, issue time,
and expiry; it SHALL NOT bind or expose an `environment_id`, project id,
secret plaintext, ciphertext, nonce, or authentication tag.

#### Scenario: Claim returns a task-bound launch-parameter token

- **WHEN** a successful claim captures a non-empty launch-parameter snapshot
- **THEN** the claim response sets `launch_parameters=true`, returns a signed task-bound `scope_token`, and omits `environment_id`

#### Scenario: Invalid token fails closed

- **WHEN** token verification fails because the signature, key id, audience, expiry, team, task, or Executor binding is invalid
- **THEN** State Registry returns the canonical non-revealing 404 response and records no decrypt operation

## REMOVED Requirements

### Requirement: State Registry resolves the effective image at claim by four-level precedence
