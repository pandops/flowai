## MODIFIED Requirements

### Requirement: Docker Executor runs bounded OpenHands containers

The Docker Executor SHALL run OpenHands containers concurrently up to its locally enforced configured capacity. Each container SHALL run the configured OpenHands agent runtime image, serve one approved task on a distinct host port, and be supervised until terminal task state or Executor exit. Every owned container SHALL retain the established `flowai.executor_id`, `flowai.cleanup_id`, `flowai.runtime=openhands`, and `flowai.task_id` labels. While local capacity is available, the Executor SHALL discover `created` tasks only when both the task's immutable `team_id` and required tag exactly equal the Executor's single registered team and tag, request explicit State Registry approval for a chosen `task_id`, and start one container only after `200 approved`. It SHALL NOT start work after `404`, `409`, or any other approval error. State Registry SHALL NOT gate discovery or approval on `max_capacity` or `running_count`.

#### Scenario: Executor starts a same-team, same-tag task up to capacity

- **WHEN** the Docker Executor has free local capacity, discovers a `created` task matching its immutable team and single tag, and receives `200 approved`
- **THEN** it appends `running`, starts one labeled OpenHands container for that approved task, supervises it, and later appends `finished` or `failed`

#### Scenario: Executor leaves excess tasks created

- **WHEN** same-team, same-tag `created` tasks exceed the Executor's locally available container slots
- **THEN** the Executor requests approval only up to its free-slot count and leaves remaining tasks `created` unless another eligible Executor is approved

#### Scenario: Executor reports capacity as an observation

- **WHEN** the Executor registers or emits a capacity self event
- **THEN** it reports observed `max_capacity` and `running_count`, while continuing to enforce capacity locally and never relying on a Registry capacity decision

### Requirement: Docker Executor registers with the State Registry

The Docker Executor SHALL generate a unique `executor_id` at process startup and authenticate with a service identity bound to exactly one immutable authorized `team_id`. It SHALL register that same `team_id`, its Executor type, exactly one authorized tag, observed `max_capacity`, observed `running_count`, and runtime metadata. State Registry SHALL reject a registration whose `team_id` does not match the authenticated service binding, that attempts to change the Executor's team, that omits the tag, or that supplies more than one tag. `max_capacity` and `running_count` SHALL remain informational observations and SHALL NOT influence State Registry discovery or approval.

#### Scenario: Executor registers one team and one tag on startup

- **WHEN** the Executor starts with a service identity authorized for `team-a` and authenticates to State Registry
- **THEN** it registers immutable `team_id = team-a`, identity, type, exactly one authorized tag, observed `max_capacity`, observed `running_count`, and metadata

#### Scenario: Executor attempts to register another team

- **WHEN** an Executor identity bound to `team-a` submits `team_id = team-b`
- **THEN** State Registry rejects the registration and the Executor does not proceed to discovery

#### Scenario: Executor records lifecycle self events

- **WHEN** the Executor starts, becomes healthy, becomes busy, becomes idle, stops, or fails
- **THEN** it appends the corresponding self event to `POST /v1/executors/{executor_id}/events` with non-null `executor_id`, non-null `team_id` equal to the authenticated binding, `event_id`, `event_type`, `occurred_at`, and `payload`

### Requirement: Docker Executor forwards state to the State Registry

The Docker Executor SHALL append OpenHands observations, Docker lifecycle observations, canonical task lifecycle events, task messages, cancellation outcomes, and Executor lifecycle events to State Registry using only its authenticated one-team identity. Task events SHALL use `POST /v1/tasks/{task_id}/events`; Executor self events SHALL use `POST /v1/executors/{executor_id}/events`. Every Executor-emitted event envelope SHALL carry non-null `executor_id`, non-null `team_id` equal to the Executor's immutable binding, stable `event_id`, `event_type`, `occurred_at`, and `payload`, plus `task_id` when task-scoped. The Executor SHALL refuse to send an event whose envelope `team_id` does not equal its authenticated binding. The assigned Executor SHALL emit `running` before starting the task container and exactly one of `finished` or `failed` at terminal state. It SHALL generate each task's new lifecycle events in valid lifecycle order with a strictly increasing `(occurred_at, event_id)` tuple, SHALL retry an ambiguous write with the same `event_id`, and SHALL NOT send or rely on an `accepted_sequence` override.

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
- **THEN** the envelope carries non-null `team_id` equal to the Executor's authenticated immutable binding and the parent task's `team_id`

### Requirement: Docker Executor API contract is documented as OpenAPI

The Docker Executor SHALL keep OpenAPI 3.1 documents for its localhost health probes and State Registry client contracts. Task discovery, explicit task approval, registration, task events, Executor events, controls, and scoped open-environment reads SHALL document the Executor's immutable `team_id` binding, exact single tag, same-team failure semantics, and team-bound scope-token fields. No Router or Env Registry OpenAPI contract SHALL be required.

#### Scenario: Developer implements Executor service clients

- **WHEN** a developer implements registration, discovery, approval, event writes, control reads, or open-environment reads
- **THEN** request and response shapes match the corresponding State Registry operations, including one-team identity and non-revealing `404` behavior for foreign identifiers

### Requirement: Docker Executor remains local and non-authoritative

The Docker Executor SHALL NOT ingest external tasks, own the durable database, persist secrets, call Web UI or API Gateway, run Kubernetes Pods, assign work to other Executors, authorize teams, trust `team_name` or caller-selected tenant headers, or run unbounded containers. It SHALL execute only same-team, same-tag tasks explicitly approved by State Registry, open environments only for its assigned same-team tasks, and enforce only its own local runtime capacity.

#### Scenario: Executor does not own task selection or tenancy

- **WHEN** the Docker Executor requests work
- **THEN** it discovers `created` tasks by exact equality on its registered immutable `team_id` and single tag, selects a candidate locally based on capacity, and asks State Registry to atomically approve that task

#### Scenario: Executor does not persist task history or secrets

- **WHEN** the Docker Executor produces an event or opens a task environment
- **THEN** it keeps no durable database or secret store and uses returned plaintext values only in the assigned runtime process

## ADDED Requirements

### Requirement: Docker Executor discovers and requests approval for same-team matching tasks

The Docker Executor SHALL discover `created` tasks from State Registry by exact equality on both its authenticated immutable `team_id` and single registered tag. It SHALL choose a candidate only while locally observed capacity is available and ask `POST /v1/executors/{executor_id}/approvals` whether it may start that `task_id`. It SHALL start only after `200 approved`. On `409 task_already_dispatched`, it SHALL discard the candidate without starting a runtime and return to discovery. On non-revealing `404`, including a foreign-team identifier, it SHALL treat the task as unavailable and SHALL NOT probe for its existence through another endpoint. The FlowAI `task_id` SHALL remain the canonical platform identifier. State Registry SHALL NOT consider capacity observations during discovery or approval.

#### Scenario: State Registry approves a same-team matching task

- **WHEN** the Executor has free local capacity, discovers a `created` task matching both its registered team and tag, and receives `200 approved`
- **THEN** it appends `running`, starts a dedicated OpenHands container, submits the approved task, and later appends `finished` or `failed`

#### Scenario: Another same-team Executor wins approval

- **WHEN** the Executor requests approval after another eligible Executor has dispatched the task
- **THEN** it receives `409 task_already_dispatched`, starts no container, appends no `running` event, and resumes discovery

#### Scenario: Foreign task identifier returns not found

- **WHEN** the Executor submits a task identifier that State Registry reports as `404`
- **THEN** it treats the task as unknown or unavailable, starts no container, appends no task event, and makes no cross-team discovery attempt

#### Scenario: State Registry returns a same-team control request

- **WHEN** State Registry returns a pending control for a task assigned to this Executor in its team
- **THEN** the Executor applies the control only to that task's runtime and records the resulting task events

### Requirement: Docker Executor uses one immutable team identity

A Docker Executor process and its authenticated service identity SHALL belong to exactly one immutable `team_id` for the process lifetime. The Executor SHALL use that binding for registration, discovery, approval, task events, self events, control reads, and environment opens. It SHALL NOT accept tasks, controls, tokens, or environment data for another team and SHALL NOT use `team_name` as authorization evidence.

#### Scenario: Executor receives data with a mismatched team

- **WHEN** a State Registry response or scope token contains a `team_id` different from the Executor's authenticated team binding
- **THEN** the Executor rejects the data locally, starts no runtime, injects no environment value, and surfaces the protocol violation

#### Scenario: Team display name changes

- **WHEN** the team's display-only `team_name` changes while `team_id` remains the same
- **THEN** the Executor's authorization identity and registered ownership remain unchanged

### Requirement: Docker Executor opens team-bound task environments from State Registry

The Docker Executor SHALL request an environment only after State Registry approves its task. It SHALL authenticate with its registered one-team identity and present the signed scope token returned with approval. The Executor SHALL call `GET /v1/environments/{environment_id}/open?task_id={task_id}` (carrying the canonical `task_id` as the query parameter) with the compact three-part scope token in the `X-FlowAI-Scope-Token` request header (the request body SHALL carry no scope-token fields). Before using returned values, the Executor SHALL require the token to use the allow-listed HMAC algorithms (`HS256`/`HS384`/`HS512`), the protected header to carry `alg` (allow-listed), `kid`, and `typ` (`scope-token+json`) with `kid == payload key_id`, and the payload to carry `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id` (matching the query parameter), `environment_id`, `executor_id`, the literal `audience` (`state-registry.environment.open`), `key_id` inside the documented active key window, `issued_at <= server_now + 30 seconds`, `expiry > issued_at`, and `expiry - issued_at <= 5 minutes`. The Executor SHALL reject the request locally and start no runtime when the query `task_id`, the header `alg`/`kid`/`typ`, the protected-header `kid` versus payload `key_id` equality, the algorithm allow-list, the project-scope rule for `project_id`, the literal audience, the active key window, the `issued_at`/`expiry` window, the assigned same-team Executor identity, or the non-terminal task state does not match the approved runtime context. The Executor SHALL NOT log, return to API Gateway, or durably persist plaintext secrets, token claims, MAC bytes, or key material. The Executor SHALL treat its own retry of the same token within TTL as allowed but SHALL still defer every canonical, transition, and assignment decision to State Registry, and SHALL NOT extend TTL or revive expired tokens. State Registry and OpenBao remain authoritative for verification and decryption respectively, and State Registry is the sole issuer of the uniform non-revealing `404 environment_unknown_or_unavailable` response with zero OpenBao operations for every invalid or unavailable variant.

#### Scenario: Executor starts an approved task with an environment

- **WHEN** an approved same-team task references an applicable environment and the Executor sends `GET /v1/environments/{environment_id}/open?task_id={task_id}` with the compact three-part scope token in the `X-FlowAI-Scope-Token` request header, the protected header carrying `alg` (allow-listed `HS256`/`HS384`/`HS512`), `kid`, and `typ` (`scope-token+json`) with `kid == payload key_id`, and the payload's `team_id`, `project_id` (required; null only when the canonical environment has no project scope), `task_id` (matching the query), `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `key_id` inside the documented active key window, `issued_at <= server_now + 30 seconds`, `expiry > issued_at`, and `expiry - issued_at <= 5 minutes` all matching canonical records
- **THEN** State Registry returns authorized env-style values, the Executor injects them into that task's OpenHands container, and discards plaintext after runtime setup

#### Scenario: Scope token team does not match Executor

- **WHEN** a scope token's `team_id`, Executor, task, project, environment, audience, `key_id`, `issued_at`, `expiry`, the protected-header `kid` versus payload `key_id` equality, the allow-listed HMAC algorithm, or the `typ` value does not match the approved runtime context or falls outside the documented active key window
- **THEN** the Executor rejects the token locally, requests no plaintext values, starts no task with that environment, and State Registry returns the same non-revealing `404 environment_unknown_or_unavailable` shape with zero OpenBao operations

#### Scenario: Executor retries the same token within TTL

- **WHEN** the assigned same-team Executor presents the same scope token again before `expiry` for the same assigned task using `GET /v1/environments/{environment_id}/open?task_id={task_id}` and the `X-FlowAI-Scope-Token` request header
- **THEN** the Executor treats the retry as allowed but relies on State Registry for canonical verification, non-terminal state, and current assignment on each retry, and SHALL NOT extend TTL or revive an expired token

## REMOVED Requirements

### Requirement: Docker Executor lists Router tasks for workload
