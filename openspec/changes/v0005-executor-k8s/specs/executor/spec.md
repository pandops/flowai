## MODIFIED Requirements

### Requirement: Docker OpenHands concrete service uses the correctly spelled identifier

The existing Docker OpenHands Executor SHALL use
`executor_docker_openhands` consistently as its top-level directory,
command, binary, config YAML basename, Go import-path segment, wire
`executor_type`, slog `service` value, probe service label, autotest package,
and current documentation identifier. Its Go constant SHALL remain
`ExecutorTypeDockerOpenHands` and SHALL have the wire value
`executor_docker_openhands`. Non-archived runtime code SHALL provide no alias
or compatibility registration for `executor_docker_opehands`; archived
OpenSpec artifacts MAY retain that spelling as historical evidence.

#### Scenario: Docker OpenHands registers under the corrected wire value

- **WHEN** the renamed Docker OpenHands Executor starts and registers with
  State Registry
- **THEN** it runs from the `executor_docker_openhands` service surface and
  registers `executor_type = "executor_docker_openhands"`, while the old
  spelling is absent from non-archived runtime and current-state files

### Requirement: K8s Executor participates in State Registry FIFO task claims

The K8s Executor SHALL register with the State Registry using the concrete
service and wire identifier `executor_k8s_openhands`, with OpenHands as its
agent tool, and exactly one immutable ownership
`scope` from `{team, system}`. It SHALL submit exactly one
`authorized_tag`, observed `max_capacity`, observed `running_count`, and
runtime metadata. Team scope SHALL submit exactly one immutable `team_id`
that references an existing team and MAY submit a display-only `team_name`;
system scope SHALL submit no `team_id` or team binding. The K8s Executor
SHALL refuse any local change to its accepted scope or team binding. It
SHALL discover eligible `pending` tasks only when the task's `required_tag`
equals its single `authorized_tag`; team scope SHALL additionally match the
task's immutable `team_id`, while system scope SHALL match across teams and
receive metadata-only summaries until successful claim. Ordering SHALL be
`(ingested_at ASC, task_id ASC)`
with the eligibility predicate applied BEFORE ordering, BEFORE
pagination, and BEFORE counts. Discovery is read-only, SHALL NOT
reserve or assign a task, and SHALL NOT read, compare, or enforce
`max_capacity` or `running_count`. The K8s Executor SHALL choose the
oldest eligible `pending` task and SHALL ask
`POST /v1/executors/{executor_id}/claim` for that `task_id` while
supplying a stable `command_id`. The K8s Executor SHALL start one Pod
only after receiving `200 claimed`. On `409
older_task_must_be_claimed_first` the K8s Executor SHALL discard the
candidate and return to discovery without starting a runtime. On `409
task_already_claimed` (an authorized same-team pending task already
claimed under a different `command_id`) the K8s Executor SHALL discard
the candidate and return to discovery without starting a runtime. On
a same `(task_id, command_id)` retry the State Registry SHALL return
the original `200 claimed` body with no new event and no field change;
the K8s Executor SHALL NOT start an additional Pod or append an
additional `running` event for that task. Point-resource lookups by
`task_id` (claim for a specific `task_id`, event write for a specific
`task_id`, control read for a specific `task_id`, environment open for
a specific `environment_id` bound to a specific `task_id`, task detail
read for a specific `task_id`) SHALL yield a non-revealing `404`
indistinguishable from "resource does not exist" when the referenced
resource belongs to a different team. The K8s Executor SHALL only ever
issue discovery with its single registered `authorized_tag`; a request
that names any other `authorized_tag` is a separate authentication
failure (exact-tag enforcement inherited from the v0002 baseline) and
SHALL NOT be described as a team-isolation `404`. No Pod SHALL be
created and no event SHALL be appended when an identifier resolves to a
cross-team or unknown resource. The State Registry SHALL NOT gate
discovery or claim on `max_capacity` or `running_count` and SHALL NEVER
emit a capacity-based rejection.

#### Scenario: K8s Executor starts

- **WHEN** a K8s Executor process starts
- **THEN** it registers `executor_type = "executor_k8s_openhands"`, exactly
  one immutable scope from `{team, system}`, exactly one `authorized_tag`,
  observed `max_capacity`, observed `running_count`, and metadata; team scope
  includes exactly one identity-bound `team_id` and optional display-only
  `team_name`, while system scope includes no team binding

#### Scenario: K8s Executor discovers a same-team pending task

- **WHEN** the K8s Executor issues discovery with its single registered tag
- **THEN** the State Registry returns only `pending` tasks whose
  `required_tag` equals the registered tag AND whose `team_id` equals
  the Executor's bound `team_id`, ordered `(ingested_at ASC, task_id ASC)`
  with the eligibility predicate applied first, regardless of
  `max_capacity` or `running_count`

### Requirement: K8s Executor runs only Kubernetes Pods

The K8s Executor SHALL control assigned task execution by creating a
fresh Kubernetes Pod containing the agent runtime after a successful
`200 claimed` from State Registry. The successful claim transaction in
the State Registry atomically sets immutable
`tasks.owner_command_id` (the request `command_id`) and
`tasks.executor_id` (the claiming Executor), persists
`tasks.resolved_image` and `tasks.image_source` from the four-level
precedence (`tasks.image` -> `task_types.default_image` ->
`source_systems.default_image` -> required `teams.default_image`), and
State Registry appends the FIRST lifecycle event `created` with
`executor_id` non-null and equal to the claiming Executor and a
payload meaning `task <task_id> loaded by <executor_id>`, and projects
the task to `created`. The K8s Executor SHALL NOT append the FIRST
`created` event itself; the State Registry appends it transactionally
on successful claim and the K8s Executor SHALL only emit `running`,
`finished`, and `failed` lifecycle events. The K8s Executor SHALL use
`tasks.resolved_image` verbatim
and SHALL refuse the task if `resolved_image` cannot be pulled or
started; the K8s Executor SHALL NOT maintain a local fallback image and
SHALL NOT substitute its own image for the Registry-resolved one. After
a successful claim the K8s Executor SHALL emit `running` and SHALL
create exactly one Kubernetes Pod for the task. The Pod SHALL carry the
immutable labels `flowai.executor_id`, `flowai.team_id`, `flowai.task_id`,
`flowai.command_id`, `flowai.executor_scope`, `flowai.resolved_image_source`,
and the runtime identity label `flowai.runtime=k8s`. The Pod SHALL mount
task-scoped inputs and be supervised until terminal task state or
Executor exit. For an OpenHands agent-server runtime, the authoritative
successful-completion signal SHALL be the terminal conversation event
whose normalized `execution_status` is `finished`; the long-running
server process and Pod exit code SHALL NOT be used as the successful
task-completion signal. A terminal OpenHands status of `failed`,
`error`, `stuck`, or `paused`, an agent-server failure, or a Pod failure
SHALL produce `failed`. Other agent tools SHALL define an equally
explicit tool-level terminal-success signal before implementation. The
Executor SHALL produce exactly one of `finished` or `failed` for the
task.

After State Registry accepts the terminal event with `202`, the K8s
Executor SHALL select the cleanup delay by the accepted event type:
non-negative `finished_cleanup_delay` after `finished` and non-negative
`failed_cleanup_delay` after `failed` (each default `0s`). It SHALL
retain the terminal task Pod and continue counting it against local
capacity for the selected delay, then delete it idempotently. The timer
SHALL begin only after terminal-event acceptance, not when the tool
first emits its terminal signal. A zero delay preserves immediate
cleanup. Shutdown, drain, explicit
cancellation, and failed-Pod handling MAY clean up sooner. Failure to
delete SHALL NOT append a second terminal task event and SHALL be
retried or surfaced as an Executor self event. The Docker OpenHands
Executor SHALL implement the same configuration and ordering for its
task container: accepted terminal conversation event, configurable
delay, then idempotent stop/removal. Every Executor-emitted task or
self event SHALL carry
`task_id` (when task-scoped), `executor_id`, `team_id` (equal to the
team-owned Executor's bound team or the system-owned Executor's assigned
task team),
`event_id`, `event_type`, `occurred_at`, and `payload`. The State Registry SHALL apply the
following event-denial taxonomy when it receives a task or self event
post: an authenticated Executor whose envelope `team_id` differs from
its immutable service binding SHALL be rejected with `403
team_mismatch`; a same-team Executor that is not the recorded
`tasks.executor_id` SHALL be rejected with `403 not_assigned`; a foreign
task or Executor point identifier SHALL be rejected with the same
non-revealing `404` shape used for an unknown identifier, without
appending any event. No `403` response SHALL be returned for a
foreign-team probe.

#### Scenario: OpenHands completion precedes delayed Pod cleanup

- **WHEN** OpenHands emits terminal `execution_status = finished`, the
  K8s Executor posts the task's single `finished` event, and State
  Registry returns `202 accepted`
- **THEN** the Executor starts `finished_cleanup_delay`, keeps the
  still-running agent-server Pod counted against local capacity during
  that delay, and idempotently deletes the Pod when the delay expires;
  Pod exit code is not the task-completion signal and cleanup appends no
  additional terminal task event

#### Scenario: Docker OpenHands uses the same terminal cleanup delay

- **WHEN** the Docker OpenHands Executor receives an accepted terminal
  OpenHands conversation event
- **THEN** it selects `finished_cleanup_delay` for `finished` or
  `failed_cleanup_delay` for `failed`, retains the task container and
  continues counting it against local capacity for the selected delay,
  and idempotently stops and removes it afterward without appending
  another terminal task event

#### Scenario: State Registry claims a task for a K8s Executor

- **WHEN** the K8s Executor discovers the oldest eligible same-team
  `pending` task, requests
  `POST /v1/executors/{executor_id}/claim` with that `task_id` and a
  stable `command_id`, and receives `200 claimed`
- **THEN** the State Registry atomically appends the first `created`
  event with `executor_id` equal to the claiming Executor, sets
  `tasks.owner_command_id` and `tasks.executor_id` (immutable from claim
  onward), persists `tasks.resolved_image` and `tasks.image_source`,
  removes the task from this Executor's scope of discovery, and projects
  the task to `created`; the K8s Executor uses `resolved_image` verbatim,
  emits `running` to `POST /v1/tasks/{task_id}/events`, and creates a
  Kubernetes Pod containing the agent runtime for that task

#### Scenario: Another Executor already claimed the task

- **WHEN** the K8s Executor requests claim for a `pending` task whose
  `team_id` equals the Executor's bound `team_id` and whose required tag
  matches the registered `authorized_tag`, but the task is already
  claimed under a different `command_id`
- **THEN** the State Registry returns `409 task_already_claimed`, the
  K8s Executor creates no Pod, appends no `running` event, and resumes
  discovery

#### Scenario: Non-oldest eligible task returns FIFO conflict

- **WHEN** the K8s Executor requests claim for a `pending` task whose
  `(ingested_at, task_id)` is NOT the oldest currently eligible tuple
  for that authenticated Executor
- **THEN** the State Registry returns
  `409 older_task_must_be_claimed_first`, the K8s Executor creates no
  Pod, appends no event, and returns to discovery

#### Scenario: Same command retries a successful claim

- **WHEN** the K8s Executor repeats a successful claim with the same
  `(task_id, command_id)`
- **THEN** the State Registry returns the original `200 claimed` body
  with no new event and no field change, and the K8s Executor SHALL NOT
  start an additional Pod or append an additional `running` event for
  that task

#### Scenario: Foreign-team claim yields no Pod and no event

- **WHEN** the K8s Executor requests claim for a `task_id` whose
  `team_id` does not equal the Executor's bound `team_id`
- **THEN** the State Registry returns a non-revealing `404`, the
  Executor creates no Pod, and no event is appended

### Requirement: K8s Executor is scope-aware and provider-neutral

The K8s Executor SHALL register with exactly one immutable `scope` from
`{team, system}`. Team scope requires exactly one identity-bound `team_id`;
system scope requires no `team_id` and derives the event and access envelope
from the assigned task's immutable `team_id`. Re-registration SHALL NOT
change scope. The K8s Executor SHALL treat the State Registry's secret-cryptography
provider as opaque. The State Registry controls the active provider
behind the same `key_id` / `key_version` envelope; the v0005 K8s
Executor SHALL NOT call, name, or require any specific provider and
SHALL NOT log or persist any provider-specific identifier beyond the
State Registry-controlled `key_id` already carried in the scope
token. Open-environment scope-token verification SHALL be expressed in
provider-neutral terms: every check (allow-listed HMAC algorithm,
`kid == key_id`, active key window, lifetime window, canonical claims,
audience, same-team assignment, non-terminal task state,
applicability) SHALL run BEFORE any decrypt operation by the active
provider, and any invalid or unavailable condition SHALL return
the same non-revealing `404 environment_unknown_or_unavailable` shape
with zero provider decrypt operations and no token plaintext,
individual claim values beyond identifier-level metadata, MAC bytes,
key material, or derived key bytes in logs, audit entries, or error
responses.

#### Scenario: K8s Executor registers with system scope

- **WHEN** an authenticated system-owned K8s Executor registers with
  `scope = system`, one `authorized_tag`, and no `team_id`
- **THEN** State Registry accepts the registration, discovery matches the
  registered tag across teams, and every successful claim returns the
  claimed task's immutable `team_id` for Pod labels and later envelopes

#### Scenario: K8s Executor treats the cryptography provider as opaque

- **WHEN** the State Registry returns a `200 claimed` body whose
  open-environment scope token is signed under the active provider's
  allow-listed HMAC algorithm with a State Registry-controlled
  `key_id`
- **THEN** the K8s Executor carries the scope token verbatim in the
  `X-FlowAI-Scope-Token` request header of
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`
  without inspecting or naming the provider; the State Registry applies
  its own provider internally and returns the authorized env-style
  values; the K8s Executor injects them only into the assigned task's
  Pod

### Requirement: K8s Executor tracks child Pod capacity locally

The K8s Executor SHALL observe every child Pod it started and SHALL
decide locally to discover, claim, or start additional tasks based on
its own capacity observation. The K8s Executor SHALL NOT request claim
or start another task when its locally observed `running_count` is
greater than or equal to `max_capacity`. Capacity enforcement SHALL be
purely local and SHALL NOT be communicated as a Registry-side
rejection; the State Registry SHALL still claim a same-team task for
any eligible Executor when the Executor's `running_count` equals
`max_capacity`. Self-event writes for those observations SHALL carry the
scope-appropriate envelope defined by State Registry and SHALL be treated as
informational only.

#### Scenario: K8s local capacity is reached

- **WHEN** the K8s Executor's locally observed `running_count` is
  greater than or equal to its `max_capacity`
- **THEN** it does not request claim or start another task until a
  later local observation shows capacity available

#### Scenario: Registry still claims at full local capacity

- **WHEN** a same-team matching `pending` task exists and the K8s
  Executor's locally observed `running_count` equals `max_capacity`
- **THEN** the State Registry SHALL still claim the task for any
  eligible same-team Executor because capacity is purely local; the K8s
  Executor SHALL decide locally whether to start a Pod

### Requirement: K8s Executor remains non-authoritative

The K8s Executor SHALL NOT ingest tasks, assign work to other
Executors, own a database, persist secrets, call Web UI or API Gateway,
run Docker containers, manage team identity (no team CRUD, no team
directory), control canonical task state outside State Registry event
and claim APIs, or read or write tasks, environments, or controls outside
its registered ownership scope. The K8s Executor SHALL NOT mutate its
accepted `scope` or team binding after first registration.

#### Scenario: K8s Executor restarts

- **WHEN** the K8s Executor restarts
- **THEN** task, assignment, and history state remain in the State
  Registry, the K8s Executor's bound `team_id` is preserved on its
  Executor record, and the K8s Executor reconciles by re-reading
  assigned tasks from durable State Registry state by canonical
  `tasks.executor_id` and `tasks.owner_command_id` without re-claiming
  already-claimed tasks and without creating duplicate Pods or
  duplicate `running` events

## ADDED Requirements

### Requirement: K8s Executor binds to a single authenticated team identity

The K8s Executor SHALL register exactly one `team_id` bound to the
authenticated Executor service identity when `scope = team` and SHALL
refuse to operate when the supplied `team_id` differs from the team
bound to that identity. The State Registry SHALL accept a registration
only when the supplied `team_id` matches the team carried by the
authenticated Executor credential, SHALL reject a registration that
omits `team_id` when `scope = team`, SHALL reject a registration that
supplies more than one `team_id`, and SHALL reject any later write that
attempts to change the stored `team_id`. `team_id` is authoritative for
the Executor's matching and authorization scope; `team_name` is a
display-only label and SHALL NOT be used to look up, match, or
authorize any task, environment, event, or control.

#### Scenario: K8s Executor registers with one team_id

- **WHEN** a K8s Executor with `scope = team` supplies one `team_id`,
  one `authorized_tag`, observed `max_capacity`, observed
  `running_count`, optional `team_name`, and metadata, all bound to the
  authenticated Executor service identity
- **THEN** the State Registry accepts the registration, stores the
  `team_id` on the canonical Executor record, and exposes the same
  `team_id` on subsequent reads

#### Scenario: K8s Executor registration without team_id is rejected

- **WHEN** a K8s Executor with `scope = team` submits a registration
  payload that omits `team_id`
- **THEN** the State Registry rejects the registration without creating
  an Executor record

#### Scenario: K8s Executor registration with mismatched team_id is rejected

- **WHEN** a K8s Executor submits a registration whose `team_id` does
  not match the team bound to the authenticated Executor service
  identity
- **THEN** the State Registry rejects the registration without creating
  or updating an Executor record

#### Scenario: K8s Executor cannot change team_id after registration

- **WHEN** an already-registered K8s Executor submits a re-registration
  whose `team_id` differs from the stored `team_id`
- **THEN** the State Registry rejects the re-registration and SHALL NOT
  change the stored `team_id`

### Requirement: K8s Executor scopes discovery, claim, task events, environment opens, and control reads to its bound team

The K8s Executor SHALL issue collection discovery, claim, task-event
writes, environment opens, and control reads only within its bound
`team_id` (when `scope = team`), and SHALL refuse to forward any
cross-team identifier to the State Registry. The State Registry SHALL
filter by `team_id` BEFORE shaping results, with two distinct outcomes:

- Collection-level discovery (the read-side task list
  `GET /v1/executors/{executor_id}/tasks?tag=...`) SHALL be filtered by
  the Executor's bound `team_id` (when `scope = team`) BEFORE the
  result is shaped. When the only matching `pending` tasks belong to
  other teams, the response SHALL be the NORMAL empty result
  (`204 No Content` or `200` with an empty task list) and SHALL NOT
  include any count, cursor, total, or pagination metadata that would
  distinguish "no tasks match this tag anywhere" from "tasks exist but
  only in other teams".

- Point-resource lookups (claim for a specific `task_id`, event write
  for a specific `task_id`, control read for a specific `task_id`,
  environment open for a specific `environment_id` bound to a specific
  `task_id`, task detail read for a specific `task_id`) whose
  referenced resource belongs to a different team SHALL return a
  non-revealing `404` indistinguishable from "resource does not exist".

In both cases the K8s Executor SHALL create no Pod and SHALL append no
event. The K8s Executor SHALL surface the same `404` and the same empty
collection results to its own control plane without revealing the
existence of tasks, environments, or controls in other teams.

#### Scenario: Foreign-team discovery returns the normal empty result without count or cursor leakage

- **WHEN** the K8s Executor issues collection-level discovery (the
  read-side task list) for a tag whose matching `pending` tasks all
  belong to a different `team_id`
- **THEN** the State Registry filters by the Executor's bound `team_id`
  BEFORE shaping results, returns the NORMAL empty result (`204 No
Content` or `200` with an empty task list), and the response payload
  contains no count, cursor, total, or pagination metadata that would
  distinguish this outcome from "no tasks match this tag anywhere"; the
  K8s Executor creates no Pod and appends no event

#### Scenario: Foreign-team task detail read returns a non-revealing 404

- **WHEN** the K8s Executor reads a specific `task_id` (for example via
  `GET /v1/tasks/{task_id}` or any task-detail endpoint) whose task
  `team_id` does not equal the Executor's bound `team_id`
- **THEN** the State Registry returns a non-revealing `404` whose body
  shape is indistinguishable from "resource does not exist", the K8s
  Executor creates no Pod, and no event is appended

#### Scenario: Foreign-team event write is rejected without append

- **WHEN** an event post arrives from a K8s Executor whose bound
  `team_id` does not match the referenced task's `team_id` (foreign
  point probe)
- **THEN** the State Registry returns the same non-revealing `404`
  shape used for an unknown identifier and appends no event

#### Scenario: Unassigned same-team event write is rejected with 403 not_assigned

- **WHEN** an event post arrives from a same-team K8s Executor whose
  bound `team_id` matches the task's `team_id` but whose `executor_id`
  is not the recorded `tasks.executor_id`
- **THEN** the State Registry returns `403 not_assigned` and appends no
  event

#### Scenario: Event envelope team_id does not match Executor

- **WHEN** an authenticated K8s Executor posts a task or self event
  whose envelope `team_id` differs from the Executor's immutable
  service binding
- **THEN** the State Registry returns `403 team_mismatch` and appends
  no event

### Requirement: K8s Executor opens assigned task environments using a team-bound scope token

The K8s Executor SHALL open a task environment from the State Registry
only after the Executor is assigned to the task AND the Executor's
bound `team_id` equals the task's `team_id` AND the request is
`GET /v1/environments/{environment_id}/open?task_id={task_id}` carrying
the State Registry-issued compact three-part signed scope token
`<header>.<payload>.<signature>` in the `X-FlowAI-Scope-Token` request
header only (no scope-token fields appear in the body or other
headers). The protected header SHALL carry `alg` (allow-listed), `kid`,
and `typ` (`scope-token+json`); the payload SHALL carry `team_id`,
`project_id` (a required claim; nullable only when the canonical
environment has no project scope), `task_id`, `environment_id`,
`executor_id`, `audience` (literal
`state-registry.environment.open`), `issued_at`, `expiry`
(`expiry > issued_at` and `expiry - issued_at <= 5 minutes`), and
`key_id` selecting a State Registry-controlled active key. The K8s
Executor SHALL present the token over its authenticated mTLS identity
bound to its `team_id`. The State Registry SHALL return the authorized
env-style values only when it has first verified that the
protected-header `kid` equals the payload `key_id`, accepted only
algorithms in the allow-listed HMAC set `HS256`/`HS384`/`HS512`,
recomputed the signature under the declared allow-listed algorithm and
compared it under constant-time comparison, verified the `key_id`
against the documented active key window (retired keys rejected),
verified `issued_at <= server_now + 30 seconds`, verified the literal
expected `audience`, verified the canonical claim shape (including the
project-scope rule for `project_id`), verified the authenticated
Executor mTLS identity, the Executor's same-team ownership, the task
assignment, the task's non-terminal state, and the project/task
applicability. Every one of those checks SHALL happen BEFORE any
decrypt operation by the active provider or plaintext disclosure. The
K8s Executor SHALL inject returned values only into that task's Pod
and SHALL NOT log or durably persist plaintext. A retry within the
token TTL by the same assigned same-team Executor MAY be allowed only
when every canonical claim, transition, and assignment check still
passes; it SHALL NOT extend TTL, SHALL NOT bypass canonical checks,
and SHALL NOT revive an expired token. Any invalid or unavailable
condition — including a missing or empty token, a tampered MAC, an
algorithm outside the allow-listed HMAC set, a `kid` not equal to
payload `key_id`, a `key_id` outside the active window, a lifetime
exceeding five minutes, expired or premature issuance, audience
mismatch, canonical-claim mismatch (including a null `project_id` for
a project-scoped environment, or a missing `project_id`), `team_id`
mismatch, terminal task state, or not-assigned caller — SHALL return
the same non-revealing `404 environment_unknown_or_unavailable` shape
with zero provider decrypt operations and no token plaintext,
individual claim values beyond identifier-level metadata, MAC bytes,
key material, or derived key bytes in logs, audit entries, or error
responses.

#### Scenario: K8s Executor opens an assigned task environment with a valid HMAC-SHA scope token

- **WHEN** the K8s Executor is assigned to a task in its bound
  `team_id` and issues
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`
  carrying the compact three-part signed scope token in the
  `X-FlowAI-Scope-Token` request header, with a protected header whose
  `alg` is `HS256`, `HS384`, or `HS512`, `kid` equal to the payload
  `key_id`, and `typ` set to `scope-token+json`; and with a payload
  whose `team_id` matches the Executor's bound `team_id`, whose
  `project_id` is null only when the canonical environment has no
  project scope and otherwise matches the canonical project, whose
  `task_id` and `environment_id` match canonical records, whose
  `executor_id` matches the assigned Executor, whose `audience` equals
  the literal `state-registry.environment.open`, whose `key_id`
  selects an active key, whose `issued_at <= server_now + 30 seconds`,
  whose `expiry > issued_at` and `expiry - issued_at <= 5 minutes`,
  and whose task is in a non-terminal state
- **THEN** the State Registry verifies `kid == key_id` first,
  recomputes the MAC under the declared allow-listed algorithm and
  compares it under constant-time comparison, verifies the `key_id`
  active window, the lifetime window, the literal `audience`, every
  canonical claim, the authenticated Executor team, same-team
  assignment, project/task applicability, and non-terminal task state,
  all BEFORE any decrypt operation by the active provider, returns the
  authorized env-style values, the K8s Executor injects them only into
  that task's Pod, and the Registry records a plaintext-free
  open-environment audit entry containing `team_id`, `executor_id`,
  `environment_id`, `task_id`, `audience`, `key_id`, and the access
  time

#### Scenario: Cross-team or invalid-binding open-environment request is rejected before any decrypt operation

- **WHEN** the K8s Executor presents the compact three-part signed
  scope token in the `X-FlowAI-Scope-Token` request header with any
  of the following invalid conditions: missing or empty token, tampered
  MAC, protected-header `alg` outside the allow-listed
  `HS256`/`HS384`/`HS512` HMAC set, protected-header `kid` that does
  not equal the payload `key_id`, `key_id` outside the documented
  active key window (including a retired `key_id`), `expiry` not
  strictly later than `issued_at`, `expiry - issued_at > 5 minutes`,
  `issued_at` in the future beyond `server_now + 30 seconds`,
  `audience` different from the literal
  `state-registry.environment.open`, missing required claim (including
  a missing `project_id`), `project_id` null while the canonical
  environment has a project scope, `project_id` mutated to an
  unrelated project, `task_id`, `environment_id`, or `executor_id`
  mutated away from canonical records, `team_id` claim different from
  the Executor's bound `team_id`, referenced task in a terminal state,
  or calling Executor no longer the recorded `tasks.executor_id`
- **THEN** the State Registry returns the same non-revealing
  `404 environment_unknown_or_unavailable` shape used for every other
  invalid or unavailable case, performs zero provider decrypt
  operations, appends no plaintext, and the K8s Executor injects no
  values into the Pod

### Requirement: K8s Executor reads pending controls only for assigned tasks in its bound team

The K8s Executor SHALL read pending operator controls only for tasks
that are assigned to the reading Executor AND whose `team_id` equals
the Executor's bound `team_id`. The K8s Executor SHALL NOT poll, list,
or apply controls for tasks assigned to a different Executor or to a
different team. The State Registry SHALL respond to a control read
whose task is in another team with a non-revealing `404`.

#### Scenario: Assigned task control is delivered to the K8s Executor

- **WHEN** a pending operator control exists for a task assigned to
  the K8s Executor in the Executor's bound `team_id`
- **THEN** the K8s Executor reads the control, applies it to that
  task's Pod, and records the resulting task events using the standard
  envelope (carrying `task_id`, `executor_id`, `team_id`, `event_id`,
  `event_type`, `occurred_at`, and `payload`)

#### Scenario: Foreign-team control read is rejected

- **WHEN** the K8s Executor requests a pending control for a task
  whose `team_id` does not equal the Executor's bound `team_id`
- **THEN** the State Registry returns a non-revealing `404` and the
  Executor reads and applies no control

### Requirement: K8s Executor reconciles to State Registry after restart without reassignment

The K8s Executor SHALL, after a restart, reconcile by re-reading its
already-claimed, non-terminal tasks from the durable State Registry
state using the canonical assignment identity
`tasks.executor_id = authenticated_executor_id`. The K8s Executor
SHALL NOT filter by `tasks.owner_command_id` during reconciliation
because `command_id` is a claim-time identifier that the Executor
uses to identify which Pod owns a given task, not a query filter for
re-reading assignment. Each reconciled task row carries its immutable
`tasks.owner_command_id`; the K8s Executor SHALL match that
`owner_command_id` to the immutable Pod label `flowai.command_id` to
identify which in-flight Pod to continue observing and SHALL treat the
canonical `(tasks.executor_id, tasks.owner_command_id)` pair as the
authoritative owner record. The K8s Executor SHALL NOT re-discover,
SHALL NOT re-claim, SHALL NOT reassign, and SHALL NOT duplicate Pods
or `running` events for tasks already claimed by it. The K8s Executor
SHALL continue observing its existing Pods through the Kubernetes API
and SHALL emit exactly one terminal `finished` or `failed` event per
Pod, each carrying `task_id`, `executor_id`, `team_id`, `event_id`,
`event_type`, `occurred_at`, and `payload`. The K8s Executor's bound
`team_id` and `authorized_tag` SHALL match the values previously
accepted by the State Registry, and any divergence SHALL be rejected
by the Registry without reassignment.

#### Scenario: K8s Executor restarts mid-run

- **WHEN** the K8s Executor restarts while one or more Pods are still
  running
- **THEN** the K8s Executor re-registers with the same bound `team_id`
  and `authorized_tag`, re-reads its claimed non-terminal tasks from
  the State Registry by canonical `tasks.executor_id =
authenticated_executor_id`, matches each returned task's immutable
  `tasks.owner_command_id` to the existing Pod's `flowai.command_id`
  label to identify which in-flight Pod to continue observing,
  re-attaches to the existing Pods in Kubernetes without creating new
  Pods, and emits exactly one terminal `finished` or `failed` event
  per task when the corresponding Pod terminates

#### Scenario: Restart does not duplicate a running event

- **WHEN** the K8s Executor restarts and re-reads a task whose latest
  task event is a registry-accepted `running` event for the same
  Executor
- **THEN** the K8s Executor creates no additional Pod and appends no
  additional `running` event for that task
