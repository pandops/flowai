# Executor

## Purpose

The Executor runs Router-provided FlowAI tasks in bounded agent-runtime containers and forwards all historical state to the State Registry. The v0001 implementation is the Docker Executor for OpenHands containers.

## Requirements

### Requirement: Docker Executor runs bounded OpenHands containers

The Docker Executor SHALL run OpenHands containers concurrently up to configured capacity. Each container SHALL run the OpenHands agent runtime image (`ghcr.io/openhands/agent-server:latest-python`). The Executor SHALL start one container per accepted task, expose each container's REST/WebSocket API on a distinct host port, and supervise each lifecycle until the task reaches a terminal state or the Executor exits.

#### Scenario: Executor starts task containers up to capacity

- **WHEN** the Docker Executor has `running_child_count < EXECUTOR_MAX_CONTAINERS` and the Router returns queued matching tasks
- **THEN** it starts one Docker container per accepted task with labels `flowai.executor_id=<executor_id>`, `flowai.runtime=openhands`, and `flowai.task_id=<task_id>` until configured capacity is full

#### Scenario: Executor reports bounded capacity

- **WHEN** the Executor registers or updates its lifecycle state
- **THEN** it reports `capacity=EXECUTOR_MAX_CONTAINERS` and current `running_child_count` to the mocked State Registry

#### Scenario: Executor leaves excess tasks queued

- **WHEN** matching queued tasks exceed available container slots
- **THEN** the Executor starts only as many tasks as it has free slots and leaves the remaining tasks in Router for later polling or other Executors

### Requirement: Docker Executor registers with the State Registry

The Docker Executor SHALL generate a unique `executor_id` UID at process startup and SHALL use that UID as its unique identifier for the lifetime of the process. The Docker Executor SHALL register the active Executor process instance with the mocked State Registry via `PUT /v1/executors/{executor_id}` using `executor_type: "docker-openhands"`, the configured `routing_target`, configured capacity, current `running_child_count`, and container metadata. The State Registry SHALL be the owner of active Executor records and historical information about every Executor that has existed.

#### Scenario: Executor registers on startup

- **WHEN** the Executor starts and is ready to poll Router tasks
- **THEN** the Executor generates a unique `executor_id` UID, registers the active process instance with the mocked State Registry via `PUT /v1/executors/{executor_id}`, and posts `{executor_id, executor_type: "docker-openhands", routing_target, capacity: EXECUTOR_MAX_CONTAINERS, running_child_count: 0, metadata}`

#### Scenario: Executor records lifecycle events

- **WHEN** the Executor starts, becomes healthy, becomes busy, becomes idle, stops, or fails
- **THEN** it appends the corresponding Executor event to `POST /v1/executors/{executor_id}/events`

### Requirement: Docker Executor lists Router tasks for workload

The Docker Executor SHALL poll the mocked Router only for workload via `GET /v1/tasks?filter=<tag>`, using its configured routing target as `filter`. The Router SHALL return all tasks when `filter` is omitted. Matching queued tasks SHALL represent new workload, and matching running tasks MAY include pending actions such as `interrupt_task` or `append_task_message`. Executor registration and historical Executor state SHALL NOT be stored in the Router.

The Router-provided `task_id` SHALL be the FlowAI platform task identifier created outside the Executor and outside OpenHands. When the Executor submits a task to OpenHands, OpenHands MAY create its own conversation/run identifier; the Executor SHALL keep the FlowAI `task_id` as the canonical platform identifier and map it to the OpenHands identifier only for runtime calls and State Registry event payloads.

#### Scenario: Router returns queued matching tasks

- **WHEN** the Router returns tasks with `status: queued` and matching `routing_target`
- **THEN** the Executor records `task.started` and `task.start_message` for each accepted task, starts a dedicated OpenHands container per task, and submits each task to its container's OpenHands REST API

#### Scenario: OpenHands returns its own runtime identifier

- **WHEN** OpenHands returns a conversation or run identifier after task submission
- **THEN** the Executor stores a mapping from FlowAI `task_id` to the OpenHands identifier and continues to use FlowAI `task_id` for Router and State Registry calls

#### Scenario: Router returns an interrupt action

- **WHEN** the Router returns `interrupt_task` in `pending_actions` for an active task
- **THEN** the Executor routes the interrupt to that task's OpenHands container, records `task.cancel_requested`, and records `task.cancelled`, `task.interrupted`, or `task.failed` to the mocked State Registry

#### Scenario: Router returns a message action

- **WHEN** the Router returns `append_task_message` in `pending_actions` for an active task
- **THEN** the Executor routes the message to that task's OpenHands container and records `task.message_forwarded` to the mocked State Registry

### Requirement: Docker Executor forwards state to the State Registry

The Docker Executor SHALL forward OpenHands events, Docker lifecycle events, task lifecycle events, task start messages, task cancellation events, intermediate in-flight messages, and Executor lifecycle events to the mocked State Registry. Task events SHALL use `POST /v1/tasks/{task_id}/events`; Executor events SHALL use `POST /v1/executors/{executor_id}/events`.

#### Scenario: Executor journals task start

- **WHEN** the Executor starts a queued task
- **THEN** it records both `task.started` and `task.start_message` before or at the same time it submits the prompt to OpenHands

#### Scenario: Executor forwards OpenHands events

- **WHEN** any owned OpenHands container emits an event (LLM call, tool use, message, status change)
- **THEN** the Executor forwards that event to the mocked State Registry via `POST /v1/tasks/{task_id}/events` without modification, including intermediate in-flight messages before terminal task state

#### Scenario: Executor journals cancellation

- **WHEN** the Executor receives an interrupt/cancel action for an active task
- **THEN** it records `task.cancel_requested` and later records `task.cancelled`, `task.interrupted`, or `task.failed` according to the OpenHands outcome

#### Scenario: Executor records task terminal state

- **WHEN** OpenHands reports task completion or failure
- **THEN** the Executor records `task.finished` or `task.failed` to the mocked State Registry and frees that task's container slot

#### Scenario: Executor records container lifecycle events

- **WHEN** an owned OpenHands container starts, dies, or fails a health check
- **THEN** the Executor records the corresponding lifecycle event to the mocked State Registry with the FlowAI `task_id`

### Requirement: Docker Executor handles container failures independently

The Docker Executor SHALL treat any unexpected exit of an owned OpenHands container as a task failure for that container's task. The Executor SHALL record a terminal `task.failed` event for that task and free the container slot. Other running task containers SHALL continue unless Docker or the Executor process itself becomes unhealthy.

#### Scenario: One OpenHands container exits unexpectedly

- **WHEN** one OpenHands container exits while its task is in flight and other task containers remain healthy
- **THEN** the Executor records `task.failed` for that task, removes that container, keeps other task containers running, and may accept another queued task on the next Router task listing

#### Scenario: Docker daemon or Executor process fails

- **WHEN** Docker or the Executor process becomes unable to supervise owned containers
- **THEN** the Executor records `executor.failed` when possible and stops accepting new work

### Requirement: Docker Executor cleans up on startup

The Docker Executor SHALL detect leftover OpenHands containers from a previous Executor process (via the `flowai.executor_id` label) and SHALL remove them before accepting new work. The Executor SHALL NOT re-attach to surviving containers across Executor restarts.

#### Scenario: Executor finds leftover containers on startup

- **WHEN** the Executor starts and `docker ps -a --filter label=flowai.executor_id=<id>` returns existing containers
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

All Go backend services in this change SHALL use the platform Go service standard: the golang-standards project layout, `net/http` with `chi`, `/v1/livez` and `/v1/readyz` probes, `slog` JSON logs, YAML configuration files, `pgx` with `sqlc` for PostgreSQL access, and `goose` for migrations.

#### Scenario: Developer creates a Go service package

- **WHEN** a developer implements the Docker Executor or mocked registry/router services
- **THEN** the code is organized under project-layout-style `cmd/`, `internal/`, `configs/`, and migration directories, uses `net/http` + `chi` for HTTP routing, emits JSON logs through `slog`, reads YAML config, and uses `pgx` + `sqlc` + `goose` for database-backed services

### Requirement: Docker Executor API contract is documented as OpenAPI

The Docker Executor SHALL keep proposed OpenAPI 3.1 documents under `specs/openapi/`: `executor.openapi.yaml` for the Executor's platform health-probe localhost REST API, `router.openapi.yaml` for Router task-list polling, `state-registry.openapi.yaml` for State Registry Executor registration and event writes, and `env-registry.openapi.yaml` for Env Registry open-env reads.

#### Scenario: Developer implements the Executor REST API

- **WHEN** a developer implements `GET /v1/livez` or `GET /v1/readyz`
- **THEN** the request and response shapes match `specs/openapi/executor.openapi.yaml`

#### Scenario: Developer implements mocked task server surfaces

- **WHEN** a developer implements `/v1/tasks`, `/v1/executors/{executor_id}`, `/v1/executors/{executor_id}/events`, `/v1/tasks/{task_id}/events`, or `/v1/env`
- **THEN** the request and response shapes match the corresponding service contract in `specs/openapi/router.openapi.yaml`, `specs/openapi/state-registry.openapi.yaml`, or `specs/openapi/env-registry.openapi.yaml`

### Requirement: Docker Executor remains local and non-authoritative

The Docker Executor SHALL NOT queue tasks, own the historical database, persist secrets, call Web UI or API Gateway, run Kubernetes Pods, decide which tasks should be scheduled, proxy OpenHands, or run unbounded containers. The Docker Executor SHALL execute Router-provided tasks and pending task actions within configured capacity, then forward all historical state to the mocked State Registry surface.

#### Scenario: Executor does not own scheduling

- **WHEN** the Docker Executor is running
- **THEN** all scheduling decisions live in the Router (mocked in v0001, real in v0004); the Executor only executes matching tasks and pending actions the Router returns while capacity is available

#### Scenario: Executor does not persist task history

- **WHEN** the Docker Executor forwards a state event
- **THEN** the event is forwarded to the State Registry surface (mocked in v0001, real in v0002); the Executor does not maintain its own database
