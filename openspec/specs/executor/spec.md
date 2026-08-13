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

### Requirement: Executors preserve configured ownership without transport identity

Every concrete Executor SHALL honor its configured and persisted `scope` for
the process lifetime without using transport identity as authorization
evidence. For team scope, it SHALL keep its immutable configured `team_id` in
registration, discovery, claims, events, controls, and environment opens. For
system scope, it SHALL omit registration `team_id` and use each claimed task's
canonical `team_id` for task-scoped operations. State Registry rejection of
inconsistent canonical data SHALL stop the affected operation. An Executor
SHALL NOT treat `executor_id`, `team_name`, or image equality as credentials or
authorization evidence.

#### Scenario: Team-owned Executor receives data with a mismatched team

- **WHEN** a State Registry response or scope token contains a `team_id` different from the team-owned Executor's configured binding
- **THEN** the Executor rejects the data locally, starts no runtime, injects no environment value, and surfaces the protocol violation

#### Scenario: System-owned Executor receives a task with envelope team_id mismatched

- **WHEN** a system-owned Executor receives a task whose envelope `team_id` differs from the parent task's canonical `team_id`
- **THEN** the Executor rejects the data locally, starts no runtime, and surfaces the protocol violation

#### Scenario: Team display name changes

- **WHEN** the team's display-only `team_name` changes while `team_id` remains the same
- **THEN** the team-owned Executor's authorization identity and registered ownership remain unchanged; a system-owned Executor is unaffected because it has no team binding

### Requirement: Executors use unauthenticated backend HTTP

Every concrete Executor SHALL call State Registry over plain HTTP without a
client certificate, private key, CA bundle, authorization credential, or
certificate-derived service identity. It SHALL preserve the current
registration lifecycle and take `scope`, optional `team_id`, and
`authorized_tag` from configuration. Legacy State Registry TLS/mTLS fields
SHALL be accepted and ignored without reading referenced files.

#### Scenario: Team-owned Executor starts without certificate files

- **WHEN** an Executor starts with a State Registry `http://` URL, `scope = team`, configured `team_id`, one tag, and no certificate files
- **THEN** it registers and proceeds using persisted scope rules without constructing a TLS client

#### Scenario: Legacy mTLS fields remain configured

- **WHEN** legacy certificate, key, CA, or TLS fields contain missing or invalid paths
- **THEN** the Executor ignores them, performs no filesystem read for those paths, and bases readiness on HTTP connectivity

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

### Requirement: Docker OpenHands concrete service uses the correctly spelled identifier

The existing Docker OpenHands Executor SHALL use `executor/docker_openhands`
as its service directory and Go import-path segment. It SHALL use
`executor_docker_openhands` consistently as its command, binary, config YAML
basename, wire `executor_type`, slog `service` value, probe service label,
qa-e2e package, and current documentation identifier. Its Go constant SHALL remain
`ExecutorTypeDockerOpenHands` and SHALL have the wire value
`executor_docker_openhands`. Non-archived runtime code SHALL provide no alias
or compatibility registration for `executor_docker_openhands`; archived
OpenSpec artifacts MAY retain that spelling as historical evidence.

#### Scenario: Docker OpenHands registers under the corrected wire value

- **WHEN** the renamed Docker OpenHands Executor starts and registers with
  State Registry
- **THEN** it runs from the `executor/docker_openhands` service surface and
  registers `executor_type = "executor_docker_openhands"`, while the old
  spelling is absent from non-archived runtime and current-state files

### Requirement: K8s Executor participates in State Registry FIFO task claims

The K8s Executor SHALL live under `executor/k8s-openhands/` and register with
the State Registry using the concrete service and wire identifier
`executor_k8s_openhands`, with OpenHands as its agent tool, and exactly one immutable ownership
`scope` from `{team, system}`. It SHALL submit exactly one
`authorized_tag`, observed `max_capacity`, observed `running_count`, and
runtime metadata. It SHALL NOT submit an `identity`; State Registry SHALL
generate and persist a server-defined `executor_id` and resolve the
canonical Executor record by that identifier on every subsequent
operation. Team scope SHALL submit exactly one immutable `team_id`
that references an existing team and SHALL NOT submit `team_name`;
system scope SHALL omit the `team_id` property and have no team binding; it
SHALL NOT submit explicit null. v0009: backend transport is plain HTTP;
the State Registry does not authenticate the Executor connection — the
deployment network policy owns the Executor-caller boundary; the
State Registry validates the canonical record shape (scope, immutable
`team_id`, exactly one tag) and never derives identity from a peer
certificate. The K8s Executor
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
  observed `max_capacity`, observed `running_count`, and metadata, with no
  `identity` or `team_name` field in the request body; team scope includes
  exactly one identity-bound `team_id`, while system scope includes no team
  binding

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
task. Every `failed` event payload SHALL carry a non-empty,
machine-readable `failure_reason`. If the Pod's agent container restarts
before OpenHands emits terminal `execution_status = finished`, the Executor
SHALL NOT continue or recreate the execution and SHALL emit exactly one
`failed` event with `failure_reason = "pod_restarted_before_finish"`.

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
delay, then idempotent stop/removal. If that Docker task container exits
before OpenHands emits terminal `execution_status = finished`, the Docker
Executor SHALL emit exactly one `failed` event with non-empty
`failure_reason = "container_exited_before_finish"`; it SHALL NOT infer
success from exit code `0` and SHALL NOT resume or recreate the execution.
Every Executor-emitted task or
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

#### Scenario: Pod restarts before OpenHands finishes

- **WHEN** the assigned task Pod's agent container restart count increases
  before OpenHands emits terminal `execution_status = finished`
- **THEN** the K8s Executor emits exactly one `failed` event whose payload
  carries `failure_reason = "pod_restarted_before_finish"`, does not resume
  or recreate the execution, and applies `failed_cleanup_delay` after State
  Registry returns `202 accepted`

#### Scenario: Docker OpenHands uses the same terminal cleanup delay

- **WHEN** the Docker OpenHands Executor receives an accepted terminal
  OpenHands conversation event
- **THEN** it selects `finished_cleanup_delay` for `finished` or
  `failed_cleanup_delay` for `failed`, retains the task container and
  continues counting it against local capacity for the selected delay,
  and idempotently stops and removes it afterward without appending
  another terminal task event

#### Scenario: Docker container exits before OpenHands finishes

- **WHEN** a Docker OpenHands task container exits with any exit code before
  OpenHands emits terminal `execution_status = finished`
- **THEN** the Docker Executor emits exactly one `failed` event whose payload
  carries `failure_reason = "container_exited_before_finish"`, does not treat
  exit code `0` as success, and does not resume or recreate the execution

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
system scope requires the `team_id` property to be absent and derives the event and access envelope
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
  `scope = system`, one `authorized_tag`, and an absent `team_id` property
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

### Requirement: K8s Executor binds to one configured team

The K8s Executor SHALL register exactly one configured `team_id` when
`scope = team` and SHALL refuse to operate when submitted or returned data
differs from that immutable binding. State Registry SHALL verify that the
submitted team exists, SHALL reject registration that omits `team_id` for team
scope or supplies it for system scope, and SHALL reject later writes that
attempt to change the stored `team_id`. It SHALL perform these checks without
an authenticated transport identity. The registration body SHALL NOT carry
`team_name`.

#### Scenario: K8s Executor registers with one team_id

- **WHEN** a K8s Executor with `scope = team` supplies one `team_id`,
  one `authorized_tag`, observed `max_capacity`, observed
  `running_count`, and metadata
- **THEN** the State Registry accepts the registration, stores the
  `team_id` on the canonical Executor record, and exposes the same
  `team_id` on subsequent reads

#### Scenario: K8s Executor registration without team_id is rejected

- **WHEN** a K8s Executor with `scope = team` submits a registration
  payload that omits `team_id`
- **THEN** the State Registry rejects the registration without creating
  an Executor record

#### Scenario: K8s Executor registration with unknown team_id is rejected

- **WHEN** a K8s Executor submits a registration whose `team_id` does not
  reference an existing team
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
Executor SHALL present the token over its configured `X-FlowAI-Executor-Id` and `X-FlowAI-Team-Id` request-data headers bound to its `team_id`. v0009: the State Registry does not authenticate the Executor connection — the scope token itself is the cryptographic proof that binds the open-environment response to the assigned task. The State Registry SHALL return the authorized
env-style values only when it has first verified that the
protected-header `kid` equals the payload `key_id`, accepted only
algorithms in the allow-listed HMAC set `HS256`/`HS384`/`HS512`,
recomputed the signature under the declared allow-listed algorithm and
compared it under constant-time comparison, verified the `key_id`
against the documented active key window (retired keys rejected),
verified `issued_at <= server_now + 30 seconds`, verified the literal
expected `audience`, verified the canonical claim shape (including the
project-scope rule for `project_id`), verified the configured
Executor `team_id`, the Executor's same-team ownership, the task
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
whose task is in another team with a non-revealing `404`. A same-team
Executor that is not the task's recorded `tasks.executor_id` SHALL receive
`403 not_assigned` without reading or applying the control.

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

#### Scenario: Same-team unassigned control read is rejected

- **WHEN** a K8s Executor requests controls for a task in its bound team that
  is assigned to another Executor
- **THEN** State Registry returns `403 not_assigned`, discloses no control
  contents, and the requesting Executor applies nothing

### Requirement: K8s Executor reconciles to State Registry after restart without reassignment

On first start, when no cache exists, the K8s Executor SHALL send `POST
/v1/executors` without `executor_id`, receive the State Registry-generated UUID
`executor_id` in `201 Created`, and atomically persist it before doing any
discovery or claim. On every later start it SHALL reuse that cached identifier
with `PUT /v1/executors/{executor_id}` and SHALL NOT request a new one. The K8s Executor SHALL maintain its durable local recovery cache on a
persistent volume; an in-memory-only cache is insufficient. For every claimed task the cache SHALL
store `task_id`, `team_id`, `executor_id`, `owner_command_id`, Pod namespace,
Pod name and UID, OpenHands conversation identity, and the last observed tool
state. It SHALL also maintain a durable event outbox containing each generated
`event_id`, event body, and delivery state (`pending` or `accepted`). The
Executor SHALL persist an event before sending it and mark it accepted only
after State Registry returns `202`; after restart it SHALL retry pending events
with the same `event_id`.
The cache SHALL be one bbolt database at `<cache_dir>/executor.db` with
versioned buckets `metadata`, `assignments`, and `event_outbox`. `metadata`
SHALL store schema version and the State Registry-generated `executor_id`;
`assignments` SHALL store runtime/conversation recovery records keyed by
`task_id`; `event_outbox` SHALL store events keyed by `event_id`. Every state
transition SHALL use a bbolt write transaction and SHALL be durably committed
before its corresponding external side effect. An unsupported schema version,
open failure, or corrupt database SHALL fail closed without registration or
runtime mutation.
The cache directory and database SHALL be owner-only. Before claim, the
Executor SHALL transactionally persist in `assignments` a claim intent keyed
by `task_id`, with stable `command_id` and state `claiming`; after an uncertain
response it SHALL retry the same `(task_id, command_id)`. After `200 claimed`
it SHALL transactionally change that record to `claimed` and persist the
runtime assignment before emitting `running` or creating a runtime. Cache
entries and logs SHALL contain no environment or secret plaintext.
Before first registration or refresh, every concrete Executor SHALL acquire a
non-blocking exclusive OS file lock on `<cache_dir>/executor.lock` and SHALL
hold it for the full process lifetime. Failure to acquire the lock SHALL make
the process unhealthy; it SHALL NOT register, refresh, discover, claim, append
events, or mutate Pods/containers. Process exit or crash SHALL release the
kernel-managed lock. K8s PVCs and Docker host-backed volumes used for cache
SHALL support POSIX advisory file locking. State Registry SHALL NOT implement
an Executor lease or fencing token. Independently copied cache volumes are an
unsupported operator action outside this local lock's protection.
Backend transport is plain HTTP. v0009 removes every backend
service-to-service mTLS contract. Legacy backend HTTP TLS/mTLS
configuration fields are accepted for staged configuration cleanup
but are never read and never affect startup. The Executor's HTTP
client never constructs a `tls.Config`; the deployment network
policy is the documented caller boundary. External HTTPS
terminates at the Ingress; PostgreSQL transport stays secure with
server-certificate verification. Certificate lifetime and renewal
schedule are owned by external PKI and deployment infrastructure
where they are still relevant and SHALL NOT be configured or
enforced as an
Executor-specific duration. After restart the Executor SHALL load non-terminal assignments only from this
cache and match cached `owner_command_id` values to immutable Pod labels
`flowai.command_id`. No State Registry assignments-list endpoint exists or is
required. If an expected cache is missing, unreadable, or corrupt, the Executor
SHALL be unhealthy, SHALL NOT claim new work, and SHALL leave existing Pods
untouched for operator recovery. The K8s Executor SHALL NOT re-discover,
SHALL NOT re-claim, SHALL NOT reassign, and SHALL NOT duplicate Pods
or `running` events for tasks already claimed by it. The K8s Executor
SHALL continue observing its existing Pods through the Kubernetes API,
reconnect to the cached OpenHands conversation without restarting the task
from the beginning, and SHALL emit exactly one terminal `finished` or `failed`
event per
Pod, each carrying `task_id`, `executor_id`, `team_id`, `event_id`,
`event_type`, `occurred_at`, and `payload`. The K8s Executor's bound
`team_id` and `authorized_tag` SHALL match the values previously
accepted by the State Registry, and any divergence SHALL be rejected
by the Registry without reassignment.

#### Scenario: K8s Executor restarts mid-run

- **WHEN** the K8s Executor restarts while one or more Pods are still
  running
- **THEN** the K8s Executor re-registers with the same bound `team_id`
  and `authorized_tag`, loads its durable recovery cache and event outbox,
  verifies the cached `executor_id`, matches each cached
  `owner_command_id` to the existing Pod's `flowai.command_id`
  label to identify which in-flight Pod to continue observing,
  re-attaches to the existing Pods in Kubernetes without creating new
  Pods, reconnects to each cached OpenHands conversation at its current
  progress, retries pending events with their original `event_id`, and emits
  exactly one terminal `finished` or `failed` event
  per task when the corresponding Pod terminates

#### Scenario: Docker Executor persists its generated identity on a volume

- **WHEN** the Docker OpenHands Executor starts for the first time with an
  empty mounted cache volume
- **THEN** it obtains one State Registry-generated UUID `executor_id` from
  first-start registration, atomically stores it and its recovery cache on
  that host-backed volume before task intake, and reuses
  the same identifier and cache after every process or container restart

#### Scenario: Second process cannot use the same cache volume

- **WHEN** another Executor process starts against a cache volume whose
  `executor.lock` is already held
- **THEN** the second process fails the non-blocking lock acquisition, reports
  unhealthy, and performs no State Registry or runtime mutation

#### Scenario: Restart does not duplicate a running event

- **WHEN** the K8s Executor restarts and re-reads a task whose latest
  task event is a registry-accepted `running` event for the same
  Executor
- **THEN** the K8s Executor creates no additional Pod and appends no
  additional `running` event for that task
