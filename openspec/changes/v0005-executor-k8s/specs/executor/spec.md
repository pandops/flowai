## MODIFIED Requirements

### Requirement: K8s Executor participates in State Registry task approvals

The K8s Executor SHALL register with the State Registry exactly one
`team_id`, exactly one `authorized_tag`, observed `max_capacity`, observed
`running_count`, an optional display-only `team_name`, and runtime metadata,
all bound to the authenticated Executor service identity. It SHALL discover
`created` tasks only when the task's `team_id` equals its bound `team_id`
and the task's `required_tag` equals its single `authorized_tag`, applying
team filtering BEFORE result shaping so a tag whose matching `created`
tasks all belong to other teams yields the NORMAL empty result (`204 No
Content` or `200` with an empty task list and no count, cursor, total, or
pagination metadata), indistinguishable from "no tasks match this tag
anywhere", rather than `404`. It SHALL request explicit approval for a
chosen task, write task and self events whose envelope includes its bound
`team_id`, and decide locally when to discover, request approval, or
create a Pod based on its own capacity observation. It SHALL NOT create a
Pod before receiving `200 approved`. Point-resource lookups by `task_id`
(approval for a specific `task_id`, event write for a specific `task_id`,
control read for a specific `task_id`, environment open for a specific
`environment_id` bound to a specific `task_id`, task detail read for a
specific `task_id`) SHALL yield a non-revealing `404` indistinguishable
from "resource does not exist" when the referenced resource belongs to a
different team. The K8s Executor SHALL only ever issue discovery with its
single registered `authorized_tag`; a request that names any other
`authorized_tag` is a separate authentication failure (exact-tag
enforcement inherited from the v0002 baseline) and SHALL NOT be described
as a team-isolation `404`. No Pod SHALL be created and no event SHALL be
appended when an identifier resolves to a cross-team or unknown resource.
The State Registry SHALL NOT gate discovery or approval on `max_capacity`
or `running_count` and SHALL NEVER emit a capacity-based rejection.

#### Scenario: K8s Executor starts

- **WHEN** a K8s Executor process starts
- **THEN** it registers executor type `k8s`, exactly one `team_id`, exactly
  one `authorized_tag`, optional display-only `team_name`, observed
  `max_capacity`, observed `running_count`, and metadata with the State
  Registry, all bound to the authenticated Executor service identity

#### Scenario: K8s Executor discovers a same-team created task

- **WHEN** the K8s Executor issues discovery with its single registered tag
- **THEN** the State Registry returns only `created` tasks whose
  `required_tag` equals the registered tag AND whose `team_id` equals the
  Executor's bound `team_id`, regardless of `max_capacity` or `running_count`

### Requirement: K8s Executor runs only Kubernetes Pods

The K8s Executor SHALL control assigned task execution by emitting a
`running` event whose envelope carries `task_id`, `executor_id`,
`team_id` (matching the Executor's bound team), `event_id`,
`event_type = running`, `occurred_at`, and `payload`, then creating a fresh
Kubernetes Pod containing the agent runtime, mounting task-scoped inputs,
observing that Pod until completion, emitting exactly one of `finished` or
`failed` with the same envelope shape (carrying `task_id`, `executor_id`,
`team_id`, `event_id`, `event_type`, `occurred_at`, `payload`), and never
running agent code directly in the Executor process. The K8s Executor SHALL
NOT create a Pod and SHALL NOT append an event for any cross-team
identifier. The State Registry SHALL apply the following event-denial
taxonomy when it receives a task or self event post: an authenticated
Executor whose envelope `team_id` differs from its immutable service
binding SHALL be rejected with `403 team_mismatch`; a same-team Executor
that is not the recorded `tasks.executor_id` SHALL be rejected with `403
not_assigned`; a foreign task or Executor point identifier SHALL be
rejected with the same non-revealing `404` shape used for an unknown
identifier, without appending any event. No `403` response SHALL be
returned for a foreign-team probe.

#### Scenario: State Registry approves a task for a K8s Executor

- **WHEN** the K8s Executor discovers a same-team matching `created` task
  and receives `200 approved` from the State Registry
- **THEN** it appends a `running` event to
  `POST /v1/tasks/{task_id}/events` carrying `task_id`, `executor_id`,
  `team_id`, `event_id`, `event_type = running`, `occurred_at`, and
  `payload`, and creates a Kubernetes Pod containing the agent runtime for
  that task

#### Scenario: Another Executor already dispatched the task

- **WHEN** the K8s Executor receives `409 task_already_dispatched`
- **THEN** it creates no Pod for that task, appends no `running` event,
  and resumes discovery

#### Scenario: Foreign-team approval yields no Pod and no event

- **WHEN** the K8s Executor requests approval for a `task_id` whose
  `team_id` does not equal the Executor's bound `team_id`
- **THEN** the State Registry returns a non-revealing `404`, the Executor
  creates no Pod, and no event is appended

### Requirement: K8s Executor tracks child Pod capacity locally

The K8s Executor SHALL observe every child Pod it started and SHALL
decide locally to discover, request approval, or start additional tasks
based on its own capacity observation. The K8s Executor SHALL NOT request
approval or start another task when its locally observed `running_count`
is greater than or equal to `max_capacity`. Capacity enforcement SHALL be
purely local and SHALL NOT be communicated as a Registry-side rejection;
the State Registry SHALL still approve a same-team task for any eligible
Executor when the Executor's `running_count` equals `max_capacity`.
Self-event writes for those observations SHALL carry `team_id` matching
the Executor's bound team and SHALL be treated as informational only.

#### Scenario: K8s local capacity is reached

- **WHEN** the K8s Executor's locally observed `running_count` is greater
  than or equal to its `max_capacity`
- **THEN** it does not request approval or start another task until a
  later local observation shows capacity available

#### Scenario: Registry still approves at full local capacity

- **WHEN** a same-team matching `created` task exists and the K8s
  Executor's locally observed `running_count` equals `max_capacity`
- **THEN** the State Registry SHALL still approve the task for any
  eligible same-team Executor because capacity is purely local; the K8s
  Executor SHALL decide locally whether to start a Pod

### Requirement: K8s Executor remains non-authoritative

The K8s Executor SHALL NOT ingest tasks, assign work to other Executors,
own a database, persist secrets, call Web UI or API Gateway, run Docker
containers, manage team identity (no team CRUD, no team directory),
control canonical task state outside State Registry event and approval
APIs, or read or write tasks, environments, or controls belonging to a
team other than its bound `team_id`. The K8s Executor SHALL NOT mutate
its bound `team_id` after first registration.

#### Scenario: K8s Executor restarts

- **WHEN** the K8s Executor restarts
- **THEN** task, assignment, and history state remain in the State
  Registry, the K8s Executor's bound `team_id` is preserved on its
  Executor record, and the K8s Executor reconciles by re-reading
  assigned tasks from durable State Registry state without
  re-approving already-dispatched tasks and without creating duplicate
  Pods or duplicate `running` events

## ADDED Requirements

### Requirement: K8s Executor binds to a single authenticated team identity

The K8s Executor SHALL register exactly one `team_id` bound to the
authenticated Executor service identity and SHALL refuse to operate when
the supplied `team_id` differs from the team bound to that identity.
The State Registry SHALL accept a registration only when the supplied
`team_id` matches the team carried by the authenticated Executor
credential, SHALL reject a registration that omits `team_id`, SHALL
reject a registration that supplies more than one `team_id`, and SHALL
reject any later write that attempts to change the stored `team_id`.
`team_id` is authoritative for the Executor's matching and authorization
scope; `team_name` is a display-only label and SHALL NOT be used to look
up, match, or authorize any task, environment, event, or control.

#### Scenario: K8s Executor registers with one team_id

- **WHEN** a K8s Executor supplies one `team_id`, one `authorized_tag`,
  observed `max_capacity`, observed `running_count`, optional `team_name`,
  and metadata, all bound to the authenticated Executor service identity
- **THEN** the State Registry accepts the registration, stores the
  `team_id` on the canonical Executor record, and exposes the same
  `team_id` on subsequent reads

#### Scenario: K8s Executor registration without team_id is rejected

- **WHEN** a K8s Executor submits a registration payload that omits
  `team_id`
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

### Requirement: K8s Executor scopes discovery, approval, task events, environment opens, and control reads to its bound team

The K8s Executor SHALL issue collection discovery, approval,
task-event writes, environment opens, and control reads only within its
bound `team_id`, and SHALL refuse to forward any cross-team identifier
to the State Registry. The State Registry SHALL filter by `team_id`
BEFORE shaping results, with two distinct outcomes:

- Collection-level discovery (the read-side task list
  `GET /v1/executors/{executor_id}/tasks?tag=...`) SHALL be filtered by
  the Executor's bound `team_id` BEFORE the result is shaped. When the
  only matching `created` tasks belong to other teams, the response
  SHALL be the NORMAL empty result (`204 No Content` or `200` with an
  empty task list) and SHALL NOT include any count, cursor, total, or
  pagination metadata that would distinguish "no tasks match this tag
  anywhere" from "tasks exist but only in other teams".

- Point-resource lookups (approval for a specific `task_id`, event
  write for a specific `task_id`, control read for a specific `task_id`,
  environment open for a specific `environment_id` bound to a specific
  `task_id`, task detail read for a specific `task_id`) whose
  referenced resource belongs to a different team SHALL return a
  non-revealing `404` indistinguishable from "resource does not exist".

In both cases the K8s Executor SHALL create no Pod and SHALL append no
event. The K8s Executor SHALL surface the same `404` and the same
empty collection results to its own control plane without revealing the
existence of tasks, environments, or controls in other teams.

#### Scenario: Foreign-team discovery returns the normal empty result without count or cursor leakage

- **WHEN** the K8s Executor issues collection-level discovery (the read-side
  task list) for a tag whose matching `created` tasks all belong to a
  different `team_id`
- **THEN** the State Registry filters by the Executor's bound `team_id`
  BEFORE shaping results, returns the NORMAL empty result (`204 No Content`
  or `200` with an empty task list), and the response payload contains no
  count, cursor, total, or pagination metadata that would distinguish this
  outcome from "no tasks match this tag anywhere"; the K8s Executor creates
  no Pod and appends no event

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
- **THEN** the State Registry returns the same non-revealing `404` shape
  used for an unknown identifier and appends no event

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
only after the Executor is assigned to the task AND the Executor's bound
`team_id` equals the task's `team_id` AND the request is
`GET /v1/environments/{environment_id}/open?task_id={task_id}` carrying
the State Registry-issued compact three-part signed scope token
`<header>.<payload>.<signature>` in the `X-FlowAI-Scope-Token` request
header only (no scope-token fields appear in the body or other
headers). The protected header SHALL carry `alg` (allow-listed), `kid`,
and `typ` (`scope-token+json`); the payload SHALL carry `team_id`, `project_id` (a required
claim; nullable only when the canonical environment has no project
scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal
`state-registry.environment.open`), `issued_at`, `expiry` (`expiry >
issued_at` and `expiry - issued_at <= 5 minutes`), and `key_id`
selecting a State Registry-controlled active key. The K8s Executor SHALL
present the token over its authenticated mTLS identity bound to its
`team_id`. The State Registry SHALL return the authorized env-style
values only when it has first verified that the protected-header `kid`
equals the payload `key_id`, accepted only algorithms in the
allow-listed HMAC set `HS256`/`HS384`/`HS512`, recomputed the signature
under the declared allow-listed algorithm and compared it under
constant-time comparison, verified the `key_id` against the documented
active key window (retired keys rejected), verified
`issued_at <= server_now + 30 seconds`, verified the literal expected
`audience`, verified the canonical claim shape (including the
project-scope rule for `project_id`), verified the authenticated
Executor mTLS identity, the Executor's same-team ownership, the task
assignment, the task's non-terminal state, and the project/task
applicability. Every one of those checks SHALL happen BEFORE any
OpenBao operation or plaintext disclosure. The K8s Executor SHALL
inject returned values only into that task's Pod and SHALL NOT log or
durably persist plaintext. A retry within the token TTL by the same
assigned same-team Executor MAY be allowed only when every canonical
claim, transition, and assignment check still passes; it SHALL NOT
extend TTL, SHALL NOT bypass canonical checks, and SHALL NOT revive
an expired token. Any invalid or unavailable condition — including a
missing or empty token, a tampered MAC, an algorithm outside the
allow-listed HMAC set, a `kid` not equal to payload `key_id`, a
`key_id` outside the active window, a lifetime exceeding five minutes,
expired or premature issuance, audience mismatch, canonical-claim
mismatch (including a null `project_id` for a project-scoped
environment, or a missing `project_id`), `team_id` mismatch, terminal
task state, or not-assigned caller — SHALL return the same
non-revealing `404 environment_unknown_or_unavailable` shape with zero
OpenBao calls and no token plaintext, individual claim values beyond
identifier-level metadata, MAC bytes, key material, or derived key
bytes in logs, audit entries, or error responses.

#### Scenario: K8s Executor opens an assigned task environment with a valid HMAC-SHA scope token

- **WHEN** the K8s Executor is assigned to a task in its bound
  `team_id` and issues
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`
  carrying the compact three-part signed scope token in the
  `X-FlowAI-Scope-Token` request header, with a protected header
  whose `alg` is `HS256`, `HS384`, or `HS512`, `kid` equal to the
  payload `key_id`, and `typ` set to `scope-token+json`; and with a payload whose `team_id`
  matches the Executor's bound `team_id`, whose `project_id` is null
  only when the canonical environment has no project scope and
  otherwise matches the canonical project, whose `task_id` and
  `environment_id` match canonical records, whose `executor_id` matches
  the assigned Executor, whose `audience` equals the literal
  `state-registry.environment.open`, whose `key_id` selects an active
  key, whose `issued_at <= server_now + 30 seconds`, whose `expiry >
  issued_at` and `expiry - issued_at <= 5 minutes`, and whose task is
  in a non-terminal state
- **THEN** the State Registry verifies `kid == key_id` first, recomputes
  the MAC under the declared allow-listed algorithm and compares it
  under constant-time comparison, verifies the `key_id` active window,
  the lifetime window, the literal `audience`, every canonical claim,
  the authenticated Executor team, same-team assignment, project/task
  applicability, and non-terminal task state, all BEFORE any OpenBao
  operation, returns the authorized env-style values, the K8s Executor
  injects them only into that task's Pod, and the Registry records a
  plaintext-free open-environment audit entry containing `team_id`,
  `executor_id`, `environment_id`, `task_id`, `audience`,
  `key_id`, and the access time

#### Scenario: Cross-team or invalid-binding open-environment request is rejected before OpenBao

- **WHEN** the K8s Executor presents the compact three-part signed scope
  token in the `X-FlowAI-Scope-Token` request header with any of the
  following invalid conditions: missing or empty token, tampered MAC,
  protected-header `alg` outside the allow-listed `HS256`/`HS384`/`HS512`
  HMAC set, protected-header `kid` that does not equal the payload
  `key_id`, `key_id` outside the documented active key window
  (including a retired `key_id`), `expiry` not strictly later than
  `issued_at`, `expiry - issued_at > 5 minutes`, `issued_at` in the
  future beyond `server_now + 30 seconds`, `audience` different from the
  literal `state-registry.environment.open`, missing required claim
  (including a missing `project_id`), `project_id` null while the
  canonical environment has a project scope, `project_id` mutated to
  an unrelated project, `task_id`, `environment_id`, or `executor_id`
  mutated away from canonical records, `team_id` claim different from
  the Executor's bound `team_id`, referenced task in a terminal state,
  or calling Executor no longer the recorded `tasks.executor_id`
- **THEN** the State Registry returns the same non-revealing `404
  environment_unknown_or_unavailable` shape used for every other
  invalid or unavailable case, performs no OpenBao operation, appends
  no plaintext, and the K8s Executor injects no values into the Pod

### Requirement: K8s Executor reads pending controls only for assigned tasks in its bound team

The K8s Executor SHALL read pending operator controls only for tasks
that are assigned to the reading Executor AND whose `team_id` equals the
Executor's bound `team_id`. The K8s Executor SHALL NOT poll, list, or
apply controls for tasks assigned to a different Executor or to a
different team. The State Registry SHALL respond to a control read whose
task is in another team with a non-revealing `404`.

#### Scenario: Assigned task control is delivered to the K8s Executor

- **WHEN** a pending operator control exists for a task assigned to the
  K8s Executor in the Executor's bound `team_id`
- **THEN** the K8s Executor reads the control, applies it to that
  task's Pod, and records the resulting task events using the standard
  envelope (carrying `task_id`, `executor_id`, `team_id`, `event_id`,
  `event_type`, `occurred_at`, and `payload`)

#### Scenario: Foreign-team control read is rejected

- **WHEN** the K8s Executor requests a pending control for a task whose
  `team_id` does not equal the Executor's bound `team_id`
- **THEN** the State Registry returns a non-revealing `404` and the
  Executor reads and applies no control

### Requirement: K8s Executor reconciles to State Registry after restart without reassignment

The K8s Executor SHALL, after a restart, reconcile by re-reading its
already-assigned tasks from the durable State Registry state. The K8s
Executor SHALL NOT re-discover, SHALL NOT re-approve, SHALL NOT
reassign, and SHALL NOT duplicate Pods or `running` events for tasks
already dispatched to it. The K8s Executor SHALL continue observing its
existing Pods through the Kubernetes API and SHALL emit exactly one
terminal `finished` or `failed` event per Pod, each carrying `task_id`,
`executor_id`, `team_id`, `event_id`, `event_type`, `occurred_at`, and
`payload`. The K8s Executor's bound `team_id` and `authorized_tag` SHALL
match the values previously accepted by the State Registry, and any
divergence SHALL be rejected by the Registry without reassignment.

#### Scenario: K8s Executor restarts mid-run

- **WHEN** the K8s Executor restarts while one or more Pods are still
  running
- **THEN** the K8s Executor re-registers with the same bound `team_id`
  and `authorized_tag`, re-reads its assigned tasks from the State
  Registry, observes the existing Pods in Kubernetes, and emits exactly
  one terminal `finished` or `failed` event per task when the
  corresponding Pod terminates

#### Scenario: Restart does not duplicate a running event

- **WHEN** the K8s Executor restarts and re-reads a task whose latest
  task event is a registry-accepted `running` event for the same
  Executor
- **THEN** the K8s Executor creates no additional Pod and appends no
  additional `running` event for that task