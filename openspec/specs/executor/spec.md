# Executor

## Purpose

The Executor runs Router-provided FlowAI tasks in bounded agent-runtime containers and forwards all historical state to the State Registry. The v0001 implementation is the Docker Executor for OpenHands containers.

## Requirements

### Requirement: Docker Executor runs bounded OpenHands containers

The Docker Executor SHALL run OpenHands containers concurrently up to its locally enforced configured capacity. Each container SHALL run the runtime image returned by State Registry on successful claim: `tasks.resolved_image` from the claim response. The Executor SHALL use `resolved_image` verbatim whenever the claim response includes it; the claim response always includes `resolved_image` because the four-level precedence (per-task `image` -> `task_types.default_image` -> `source_systems.default_image` -> required `teams.default_image`) is guaranteed to resolve at registration time. The Executor SHALL NOT maintain a local fallback image and SHALL NOT substitute its own image for a Registry-resolved one; if `resolved_image` cannot be pulled or started, the Executor SHALL refuse the task. Every owned container SHALL serve one claimed task on a distinct host port, be supervised until terminal task state or Executor exit, and retain the established `flowai.executor_id`, `flowai.cleanup_id`, `flowai.runtime=openhands`, and `flowai.task_id` labels plus a `flowai.team_id` label set to the parent task's `team_id`, a `flowai.executor_scope` label set to the registered scope, a `flowai.command_id` label set to the claiming `command_id`, and a `flowai.resolved_image_source` label set to the four-level precedence value (`task_override`, `task_type_default`, `source_system_default`, or `team_default`). While local capacity is available, the Executor SHALL discover only `pending` tasks in FIFO order `(ingested_at ASC, task_id ASC)` according to its scope: when `scope = team`, it SHALL match the task's immutable `team_id` AND the task's required tag against the Executor's registered team and tag; when `scope = system`, it SHALL match the task's required tag only and SHALL return metadata-only summaries that omit task payload values, environment values, secret material, and event history (task payload is delivered only in a successful `200 claimed` response for the assigned task). In both cases it SHALL request explicit State Registry claim for a chosen `task_id`, SHALL provide a `command_id`, and SHALL start one container only after `200 claimed`. It SHALL NOT start work after `404`, `409 older_task_must_be_claimed_first`, `409 task_already_claimed`, or any other claim error. State Registry SHALL NOT gate discovery or claim on capacity.

#### Scenario: Executor starts a same-team, same-tag pending task up to capacity

- **WHEN** the Docker Executor has free local capacity, discovers the oldest eligible same-team same-tag `pending` task in `(ingested_at ASC, task_id ASC)` order, and receives `200 claimed` with `resolved_image` and `command_id`
- **THEN** it sets `tasks.owner_command_id`, appends the first `created` event (via State Registry), starts one labeled OpenHands container for that claimed task using `resolved_image`, supervises it, and later appends `finished` or `failed`

#### Scenario: System-owned Executor starts a cross-team task up to capacity

- **WHEN** a system-owned Docker Executor has free local capacity, discovers the oldest eligible cross-team `pending` task whose tag matches its single registered tag, and receives `200 claimed` with `resolved_image` and `command_id`
- **THEN** it sets envelope `team_id` to the parent task's `team_id`, `executor_id` to its authenticated identity, and `command_id` to the immutable claim id; starts one labeled OpenHands container with `resolved_image`; supervises it; and later appends `finished` or `failed`

#### Scenario: Executor rejects a different image than Registry resolved

- **WHEN** a State Registry claim response carries a `resolved_image` reference that differs from the Executor's local configured image (if any)
- **THEN** the Executor SHALL use `resolved_image` verbatim and SHALL NOT substitute the local image, and SHALL refuse the task if `resolved_image` cannot be pulled or started

#### Scenario: Executor respects FIFO

- **WHEN** the Executor discovers an eligible `pending` task list and the oldest eligible `(ingested_at ASC, task_id ASC)` task is NOT the one the Executor would otherwise choose
- **THEN** the Executor claims the oldest eligible task; claiming any other eligible task SHALL receive `409 older_task_must_be_claimed_first`, and the Executor SHALL return to discovery

#### Scenario: Executor leaves excess pending tasks

- **WHEN** same-team, same-tag `pending` tasks exceed the Executor's locally available container slots
- **THEN** the Executor claims only up to its free-slot count in FIFO order and leaves remaining tasks `pending` unless another eligible Executor claims them

#### Scenario: Executor reports capacity as an observation

- **WHEN** the Executor registers or emits a capacity self event
- **THEN** it reports observed `max_capacity` and `running_count` (or its provider-neutral equivalent), while continuing to enforce capacity locally and never relying on a Registry capacity decision

### Requirement: Docker Executor registers with the State Registry

The Docker Executor SHALL generate a unique `executor_id` at process startup and authenticate with a service identity bound to exactly one immutable authorized `team_id` (when `scope = team`) or to no team (when `scope = system`). For team-owned Executors, it SHALL register that same existing `team_id` and one authorized tag, Executor type, observed `max_capacity`, observed `running_count`, and runtime metadata. State Registry SHALL reject a registration whose `team_id` does not match the authenticated service binding or does not reference an existing team, that attempts to change the Executor's team, that omits the tag, or that supplies more than one tag. The Executor's `scope` SHALL be `team` or `system` and SHALL be persistent and immutable from registration onward. `max_capacity` and `running_count` SHALL remain informational observations and SHALL NOT influence State Registry discovery or claim.

#### Scenario: Executor registers one team and one tag on startup

- **WHEN** the Executor starts with a service identity authorized for `team-a` and authenticates to State Registry
- **THEN** it registers immutable `team_id = team-a` (referencing an existing team), identity, type, exactly one authorized tag, observed `max_capacity`, observed `running_count`, and metadata

#### Scenario: Executor attempts to register another team

- **WHEN** an Executor identity bound to `team-a` submits `team_id = team-b`
- **THEN** State Registry rejects the registration and the Executor does not proceed to discovery

#### Scenario: Executor registration against an unknown team is rejected

- **WHEN** an Executor identity submits a `team_id` that does not reference an existing team
- **THEN** State Registry rejects the registration without persisting or partially creating any Executor or team row

#### Scenario: Executor records lifecycle self events

- **WHEN** the Executor starts, becomes healthy, becomes busy, becomes idle, stops, or fails
- **THEN** it appends the corresponding self event to `POST /v1/executors/{executor_id}/events` with non-null `executor_id`, non-null `team_id` equal to the authenticated binding (or null when `scope = system`), `event_id`, `event_type`, `occurred_at`, and `payload`

### Requirement: Docker Executor forwards state to the State Registry

The Docker Executor SHALL append OpenHands observations, Docker lifecycle observations, canonical task lifecycle events, task messages, cancellation outcomes, and Executor lifecycle events to State Registry using only its authenticated one-team identity (or no team, for `scope = system`). Task events SHALL use `POST /v1/tasks/{task_id}/events`; Executor self events SHALL use `POST /v1/executors/{executor_id}/events`. Every Executor-emitted event envelope SHALL carry non-null `executor_id`, `team_id` (when `scope = team`, equal to the Executor's immutable binding; when `scope = system`, equal to the parent task's `team_id`), stable `event_id`, `event_type`, `occurred_at`, and `payload`, plus `task_id` when task-scoped. The Executor SHALL refuse to send an event whose envelope `team_id` does not equal its authenticated binding (when `scope = team`) or whose envelope `team_id` does not equal the parent task's `team_id` (when `scope = system`). The claiming Executor SHALL emit `running` after a successful claim, and exactly one of `finished` or `failed` at terminal state. It SHALL generate each task's new lifecycle events in valid lifecycle order with a strictly increasing `(occurred_at, event_id)` tuple, SHALL retry an ambiguous write with the same `event_id`, and SHALL NOT send or rely on an `accepted_sequence` override.

#### Scenario: Executor retries an event write

- **WHEN** an event write receives an ambiguous response
- **THEN** the Executor retries the identical event with the same scoped `event_id` so State Registry stores it at most once

#### Scenario: Executor records terminal state

- **WHEN** the runtime reports completion or failure after `running` was accepted
- **THEN** the Executor appends `finished` or `failed` with a later ordering tuple and frees its local capacity slot after terminal handling

#### Scenario: Registry rejects an out-of-order event

- **WHEN** State Registry rejects a new event because its ordering tuple is not later than the task's latest accepted event
- **THEN** the Executor does not attempt an ordering bypass or start duplicate work and surfaces the failed event delivery for recovery

#### Scenario: Executor sends task event with envelope team_id

- **WHEN** the Executor sends a task event to `POST /v1/tasks/{task_id}/events`
- **THEN** the envelope carries non-null `team_id` equal to the Executor's authenticated immutable binding (when `scope = team`) or equal to the parent task's `team_id` (when `scope = system`)

### Requirement: Docker Executor handles container failures independently

The Docker Executor SHALL treat any unexpected exit of an owned OpenHands container as a task failure for that container's task. The Executor SHALL record a terminal `task.failed` event for that task and free the container slot. Other running task containers SHALL continue unless Docker or the Executor process itself becomes unhealthy.

#### Scenario: One OpenHands container exits unexpectedly

- **WHEN** one OpenHands container exits while its task is in flight and other task containers remain healthy
- **THEN** the Executor records `task.failed` for that task, removes that container, keeps other task containers running, and may accept another queued task on the next Router task listing

#### Scenario: Docker daemon or Executor process fails

- **WHEN** Docker or the Executor process becomes unable to supervise owned containers
- **THEN** the Executor records `executor.failed` when possible and stops accepting new work

### Requirement: Docker Executor cleans up on startup

The Docker Executor SHALL detect leftover OpenHands containers from a previous Executor process using the stable `flowai.cleanup_id` label (not the per-process `flowai.executor_id` UID, which is unique to a single process and cannot discover leftovers across restarts) and SHALL remove them before accepting new work. The Executor SHALL NOT re-attach to surviving containers across Executor restarts. The cleanup_id file is held in a user-owned, non-world-writable location (under the OS user cache dir or under `FLOWAI_CLEANUP_ID_DIR`); the `/tmp` legacy fallback is best-effort and never the source of a cross-process cleanup on a multi-tenant host.

#### Scenario: Executor finds leftover containers on startup

- **WHEN** the Executor starts and `docker ps -a --filter label=flowai.cleanup_id=<id>` returns existing containers
- **THEN** the Executor removes those containers before proceeding with State Registry registration and Router polling

### Requirement: Docker Executor shuts down gracefully

The Docker Executor SHALL handle SIGTERM by stopping Router task listing, telling each owned OpenHands container with an in-flight task to drain or interrupt (with a configurable deadline), recording terminal task and Executor events to the mocked State Registry, stopping and removing all owned OpenHands containers, and exiting. If the drain deadline elapses, the Executor SHALL force-kill remaining containers before exiting.

#### Scenario: SIGTERM during idle polling

- **WHEN** the Docker Executor receives SIGTERM while no task containers are running
- **THEN** it records `executor.stopping` and `executor.stopped` and exits

#### Scenario: SIGTERM during in-flight tasks

- **WHEN** the Docker Executor receives SIGTERM while one or more task containers are running
- **THEN** it tells each owned OpenHands container to drain/interrupt, waits up to `OPENHANDS_DRAIN_TIMEOUT_SECONDS`, records terminal task and Executor events to the mocked State Registry, removes all owned containers, and exits; if the deadline elapses, it force-kills remaining containers before exiting

### Requirement: Docker Executor exposes only localhost platform health probes

The Docker Executor SHALL expose a REST API bound to `EXECUTOR_API_BIND` (default `127.0.0.1:8020`). The API SHALL be reachable only from the host loopback. The API SHALL be URI-versioned under `/v1` and expose only liveness (`GET /v1/livez`) and readiness (`GET /v1/readyz`). The Executor SHALL NOT expose local REST endpoints for task start, task interrupt, task messages, or OpenHands proxying.

#### Scenario: Operator hits the liveness endpoint

- **WHEN** an operator makes a `GET /v1/livez` request to the Executor
- **THEN** the Executor returns `200 OK` if the Executor process is alive, regardless of OpenHands container state

#### Scenario: Operator hits the readiness endpoint

- **WHEN** an operator makes a `GET /v1/readyz` request to the Executor
- **THEN** the Executor returns `200 OK` only when it is ready to list Router workload and has capacity or running containers it can supervise; otherwise it returns `503 Service Unavailable`

### Requirement: Go backend services use the platform service standard

All Go backend services in this change SHALL use the stateless platform Go service standard: the golang-standards project layout, `net/http` with `chi`, `/v1/livez` and `/v1/readyz` probes, `slog` JSON logs, and YAML configuration files.

#### Scenario: Developer creates a Go service package

- **WHEN** a developer implements the Docker Executor or mocked registry/router services
- **THEN** the code is organized under project-layout-style `cmd/`, `internal/`, and `configs/` directories, uses `net/http` + `chi` for HTTP routing, emits JSON logs through `slog`, and reads YAML config

### Requirement: Docker Executor API contract is documented as OpenAPI

The Docker Executor SHALL keep OpenAPI 3.1 documents for its localhost health probes and State Registry client contracts. Task discovery, explicit task claim, registration, task events, Executor events, controls, and scoped open-environment reads SHALL document the Executor's immutable `team_id` binding, exact single tag, same-team failure semantics, and team-bound scope-token fields. No Router or Env Registry OpenAPI contract SHALL be required.

#### Scenario: Developer implements Executor service clients

- **WHEN** a developer implements registration, discovery, claim, event writes, control reads, or open-environment reads
- **THEN** request and response shapes match the corresponding State Registry operations, including one-team identity and non-revealing `404` behavior for foreign identifiers

### Requirement: Mocked task server keeps only ephemeral state

The mocked task server SHALL keep Router tasks, State Registry executor records and events, and Env Registry values in memory only. It SHALL NOT require a database or promise durability across process restarts. Durable State Registry persistence SHALL be implemented by `v0002-state-registry`.

#### Scenario: Mocked task server restarts

- **WHEN** the mocked task server process restarts
- **THEN** previously seeded tasks, executor registrations, events, and environment values MAY be absent without violating the v0001 contract

### Requirement: Docker Executor remains local and non-authoritative

The Docker Executor SHALL NOT ingest external tasks, own the durable database, persist secrets, call Web UI or API Gateway, run Kubernetes Pods, assign work to other Executors, authorize teams, trust `team_name` or caller-selected tenant headers, or run unbounded containers. It SHALL execute only same-team, same-tag tasks explicitly claimed by State Registry, open environments only for its assigned same-team tasks (or assigned task across teams when `scope = system`), and enforce only its own local runtime capacity.

#### Scenario: Executor does not own task selection or tenancy

- **WHEN** the Docker Executor requests work
- **THEN** it discovers `pending` tasks by exact equality on its registered immutable `team_id` and single tag (or by exact tag only when `scope = system`), selects the oldest eligible task by `(ingested_at ASC, task_id ASC)`, and asks State Registry to atomically claim that task with a `command_id`

#### Scenario: Executor does not persist task history or secrets

- **WHEN** the Docker Executor produces an event or opens a task environment
- **THEN** it keeps no durable database or secret store and uses returned plaintext values only in the assigned runtime process

### Requirement: Docker Executor registers with team-owned or system-owned scope

The Docker Executor SHALL register itself with State Registry using one of two ownership scopes chosen by the operator configuration: `scope = team` (the Executor is bound to exactly one immutable authorized `team_id`) or `scope = system` (the Executor has no team binding and receives tasks from every team whose tag matches the Executor's registered tag). The Executor SHALL derive its scope from operator configuration at process startup and SHALL submit the same `scope` value on every registration, re-registration, and self event. When `scope = team`, the Executor SHALL submit its immutable authorized `team_id` (which SHALL reference an existing team) on registration and SHALL reject any local change to that binding. When `scope = system`, the Executor SHALL submit `team_id = null` on registration and SHALL not assume a team. The Executor SHALL refuse to start work when its registered scope does not match its operator configuration, SHALL refuse a re-registration that would change the scope, and SHALL record its registered scope on every audit entry it produces. The Executor SHALL NOT use its registration body to create or update a team; the Executor assumes the team already exists.

#### Scenario: Operator deploys a team-owned Docker Executor

- **WHEN** the operator starts the Docker Executor with `executor.scope = team` and a configured `team_id = team-a`
- **THEN** the Executor registers with State Registry as team-owned (team references an existing team), persists the immutable `team_id = team-a` on its row, and proceeds to same-team + same-tag FIFO discovery

#### Scenario: Operator deploys a system-owned Docker Executor

- **WHEN** the operator starts the Docker Executor with `executor.scope = system` and no configured `team_id`
- **THEN** the Executor registers with State Registry as system-owned, persists `team_id = NULL` on its row, and proceeds to cross-team + same-tag FIFO discovery

#### Scenario: Docker Executor refuses a re-registration that changes scope

- **WHEN** an already-registered Docker Executor attempts to re-register with a different `scope` than its original registration
- **THEN** the Executor logs the rejection, keeps its existing record, and continues without restarting work

#### Scenario: Docker Executor rejects a foreign task when scope = team

- **WHEN** a team-owned Docker Executor names a `task_id` owned by another team in any State Registry call (discovery return, claim, or task event)
- **THEN** the Executor treats the task as unknown or unavailable, starts no container, appends no task event, and makes no cross-team attempt

### Requirement: Docker Executor discovers and claims the oldest eligible same-team matching task

The Docker Executor SHALL discover `pending` tasks from State Registry by exact equality on both its authenticated immutable `team_id` and single registered tag (or by tag only when `scope = system`). The discovery response SHALL be ordered `(ingested_at ASC, task_id ASC)` and SHALL contain only eligible tasks. The Executor SHALL choose the oldest eligible task while locally observed capacity is available and SHALL ask `POST /v1/executors/{executor_id}/claim` whether it may claim that `task_id`, supplying a stable `command_id`. State Registry SHALL claim the task only when the requested task is the oldest currently eligible `pending` task for that authenticated Executor, atomically setting `owner_command_id`, `executor_id`, and `tasks.resolved_image`; appending the first `created` event; and removing the task from this Executor's scope of discovery. On `409 older_task_must_be_claimed_first`, the Executor SHALL return to discovery without starting a runtime. On `409 task_already_claimed`, the Executor SHALL discard the candidate without starting a runtime and return to discovery. On non-revealing `404`, including a foreign-team identifier, the Executor SHALL treat the task as unavailable and SHALL NOT probe for its existence through another endpoint. A same `(task_id, command_id)` retry by the same Executor SHALL return the original successful claim response without appending or changing anything. The FlowAI `task_id` SHALL remain the canonical platform identifier. State Registry SHALL NOT consider capacity observations during discovery or claim.

#### Scenario: State Registry claims the oldest eligible same-team matching task

- **WHEN** the Executor has free local capacity, discovers the oldest eligible `pending` task matching its registered team and tag in `(ingested_at ASC, task_id ASC)` order, and receives `200 claimed` with `command_id`
- **THEN** it sets `tasks.owner_command_id` and `tasks.executor_id` (immutable from claim onward), appends `created` (the first lifecycle event) with `executor_id` non-null and payload `task <task_id> loaded by <executor_id>`, starts a dedicated OpenHands container with `tasks.resolved_image`, and later appends `finished` or `failed`

#### Scenario: Executor requests a non-oldest eligible task

- **WHEN** the Executor requests claim for a `pending` task whose `(ingested_at, task_id)` is not the oldest eligible tuple for that authenticated Executor
- **THEN** State Registry returns `409 older_task_must_be_claimed_first`, appends no event, sets no fields, and the Executor returns to discovery without starting a runtime

#### Scenario: Another eligible Executor wins claim

- **WHEN** the Executor requests claim after another eligible Executor has claimed the task
- **THEN** it receives `409 task_already_claimed`, starts no container, appends no `running` event, and resumes discovery

#### Scenario: Executor retries with the same command

- **WHEN** the Executor repeats a successful claim with the same `(task_id, command_id)`
- **THEN** State Registry returns the original `200 claimed` response without appending an additional event

#### Scenario: One command may own many tasks

- **WHEN** the Executor submits successive successful claims using the same `command_id` for different tasks
- **THEN** every claimed task sets `tasks.owner_command_id` to the same `command_id`, every claim returns `200 claimed`, and one `created` event exists per task

#### Scenario: Foreign task identifier returns not found

- **WHEN** the Executor submits a task identifier that State Registry reports as `404`
- **THEN** it treats the task as unknown or unavailable, starts no container, appends no task event, and makes no cross-team discovery attempt

#### Scenario: State Registry returns a same-team control request

- **WHEN** State Registry returns a pending control for a task assigned to this Executor in its team
- **THEN** the Executor applies the control only to that task's runtime and records the resulting task events

### Requirement: Docker Executor uses its registered scope as its authorization identity

A Docker Executor process and its authenticated service identity SHALL honor the `scope` it registered with State Registry for the process lifetime. When `scope = team`, the Executor SHALL use its immutable `team_id` binding for registration, discovery, claim, task events, self events, control reads, and environment opens; it SHALL reject any task, control, token, or environment data whose `team_id` differs from that binding. When `scope = system`, the Executor SHALL NOT assume a single team; it SHALL use the parent task's `team_id` as the per-task authorization context for claim, task events, environment opens, and control reads, and SHALL reject any task, control, token, or environment data whose task-side `team_id` is missing, foreign to the canonical task, or contradicts any other claim. In both scopes, the Executor SHALL NOT use `team_name` as authorization evidence. The Executor SHALL NOT use image equality or reuse of an opaque image string as evidence that the same team owns the task.

#### Scenario: Team-owned Executor receives data with a mismatched team

- **WHEN** a State Registry response or scope token contains a `team_id` different from the team-owned Executor's authenticated team binding
- **THEN** the Executor rejects the data locally, starts no runtime, injects no environment value, and surfaces the protocol violation

#### Scenario: System-owned Executor receives a task with envelope team_id mismatched

- **WHEN** a system-owned Executor receives a task whose envelope `team_id` differs from the parent task's canonical `team_id`
- **THEN** the Executor rejects the data locally, starts no runtime, and surfaces the protocol violation

#### Scenario: Team display name changes

- **WHEN** the team's display-only `team_name` changes while `team_id` remains the same
- **THEN** the team-owned Executor's authorization identity and registered ownership remain unchanged; a system-owned Executor is unaffected because it has no team binding

### Requirement: Docker Executor opens task-bound environments from State Registry

The Docker Executor SHALL request an environment only after State Registry claims its task and only for an assigned task. It SHALL authenticate with its registered identity and present the signed scope token returned with the claim response. The Executor SHALL call `GET /v1/environments/{environment_id}/open?task_id={task_id}` (carrying the canonical `task_id` as the query parameter) with the compact three-part scope token in the `X-FlowAI-Scope-Token` request header (the request body SHALL carry no scope-token fields). Before using returned values, the Executor SHALL require the token to use the allow-listed HMAC algorithms (`HS256`/`HS384`/`HS512`), the protected header to carry `alg` (allow-listed), `kid`, and `typ` (`scope-token+json`) with `kid == payload key_id`, and the payload to carry `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id` (matching the query parameter), `environment_id`, `executor_id`, the literal `audience` (`state-registry.environment.open`), `key_id` inside the documented active key window, `issued_at <= server_now + 30 seconds`, `expiry > issued_at`, and `expiry - issued_at <= 5 minutes`. The Executor SHALL reject the request locally and start no runtime when the query `task_id`, the header `alg`/`kid`/`typ`, the protected-header `kid` versus payload `key_id` equality, the algorithm allow-list, the project-scope rule for `project_id`, the literal audience, the active key window, the `issued_at`/`expiry` window, the assigned Executor identity, the same `command_id` that owns the task, or the non-terminal task state does not match the approved runtime context. The Executor SHALL NOT log, return to API Gateway, or durably persist plaintext secrets, token claims, MAC bytes, key material, derived key bytes, nonces, ciphertext bytes, authentication tag bytes, or associated-data values beyond identifier-level metadata. The Executor SHALL treat its own retry of the same token within TTL as allowed but SHALL still defer every canonical, transition, and assignment decision to State Registry, and SHALL NOT extend TTL or revive expired tokens. State Registry SHALL only invoke its local AES-256-GCM decryption after every check passes; no decrypt operation runs otherwise.

#### Scenario: Executor starts a claimed task with an environment

- **WHEN** a claimed task references an applicable environment and the Executor sends `GET /v1/environments/{environment_id}/open?task_id={task_id}` with the compact three-part scope token in the `X-FlowAI-Scope-Token` request header, the protected header carrying `alg` (allow-listed `HS256`/`HS384`/`HS512`), `kid`, and `typ` (`scope-token+json`) with `kid == payload key_id`, and the payload's `team_id`, `project_id` (required; null only when the canonical environment has no project scope), `task_id` (matching the query), `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `key_id` inside the documented active key window, `issued_at <= server_now + 30 seconds`, `expiry > issued_at`, and `expiry - issued_at <= 5 minutes` all matching canonical records
- **THEN** State Registry returns authorized env-style values after local AES-256-GCM decryption, the Executor injects them into that task's OpenHands container, and discards plaintext after runtime setup

#### Scenario: Scope token team does not match Executor

- **WHEN** a scope token's `team_id`, Executor, task, project, environment, audience, `key_id`, `issued_at`, `expiry`, the protected-header `kid` versus payload `key_id` equality, the allow-listed HMAC algorithm, or the `typ` value does not match the claimed runtime context or falls outside the documented active key window
- **THEN** the Executor rejects the token locally, requests no plaintext values, starts no task with that environment, and State Registry returns the same non-revealing `404 environment_unknown_or_unavailable` shape with zero decrypt operations

#### Scenario: Executor retries the same token within TTL

- **WHEN** the assigned Executor presents the same scope token again before `expiry` for the same assigned task using `GET /v1/environments/{environment_id}/open?task_id={task_id}` and the `X-FlowAI-Scope-Token` request header
- **THEN** the Executor treats the retry as allowed but relies on State Registry for canonical verification, non-terminal state, and current assignment on each retry, and SHALL NOT extend TTL or revive an expired token
