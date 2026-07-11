# Design: Docker Executor (MVP)

## Runtime Shape

- The Executor is a single Go process that runs **multiple OpenHands containers up to a configured capacity**.
- The Executor pulls the OpenHands image, starts containers with port 8000 published on distinct host ports, and supervises their lifecycle via the Docker daemon.
- The Executor registers with the mocked State Registry, lists matching workload from the mocked Router, forwards task work/actions to the OpenHands REST API, and writes all executor/task lifecycle history to the mocked State Registry.
- The Executor host process never executes agent code; all agent execution is inside the OpenHands container.
- The Executor is not a proxy for OpenHands. It exposes only its own platform health probes.
- One Executor = one worker process with bounded container capacity. Future Executors for different agent runtimes are separate processes.

## Components

### Executor process

- Long-running Go daemon.
- Reads YAML configuration with environment variable overrides (see Configuration).
- Generates a unique `executor_id` UID at process startup and uses that UID as the identity for State Registry registration, State Registry events, Env Registry reads, health-probe responses, and Docker labels for the lifetime of that process.
- Owns zero or more Docker containers labeled `flowai.executor_id=<executor_id>`, `flowai.runtime=openhands`, and `flowai.task_id=<task_id>`, capped by `EXECUTOR_MAX_CONTAINERS`.
- Maintains clients to the mocked Router, State Registry, and Env Registry surfaces.
- Maintains per-task HTTP clients to OpenHands containers on distinct `localhost:<mapped_port>` values (or container IPs).
- Exposes only `GET /v1/livez` and `GET /v1/readyz` on localhost. It does not expose task start, interrupt, message, or OpenHands proxy endpoints.

### OpenHands container

- Image: `ghcr.io/openhands/agent-server:latest-python` (see ADR-0003).
- Exposes REST/WebSocket API on container port 8000.
- Runs the OpenHands agent runtime: LLM calls, tool use, code execution, browser automation (all inside the container).
- OpenHands itself emits observability via OpenTelemetry (Laminar by default; any OTLP-compatible backend via env vars). The Executor does not proxy or interpret this observability.
- Exposes a pause endpoint (`POST /api/conversations/{conversation_id}/pause`) used by the Executor after the Router returns an `interrupt_task` action.
- Exposes an event-creation endpoint (`POST /api/conversations/{conversation_id}/events`) used by the Executor after the Router returns an `append_task_message` action.

### Mocked task server

The mocked task server imitates three eventually-separate services. The exact request and response schemas are defined in `specs/openapi/router.openapi.yaml`, `specs/openapi/state-registry.openapi.yaml`, and `specs/openapi/env-registry.openapi.yaml`.

All mocked-server REST paths are served under `/v1` and use a shared error envelope with request correlation metadata.

**Router surface (eventually v0004)**:
- `GET /v1/tasks?filter=<tag>` — Executor lists Router tasks matching its routing tag. Without `filter`, Router returns all tasks. Matching queued tasks are new workload; matching running tasks can carry `pending_actions` such as `interrupt_task` or `append_task_message`.

**State Registry surface (eventually v0002)**:
- `PUT /v1/executors/{executor_id}` — Executor registers the active Executor process instance using the `executor_id` UID generated at startup and payload `{executor_id, executor_type: "docker-openhands", routing_target, capacity, running_child_count, metadata}`. The State Registry is the owner of known active Executor records and historical Executor identity records.
- `POST /v1/executors/{executor_id}/events` — Executor records its own lifecycle and health-relevant events. State Registry derives information about every existed Executor from these records.
- `POST /v1/tasks/{task_id}/events` — Executor records task/container/runtime events, including `task.started`, `task.finished`, `task.failed`, `task.interrupted`, `task.message_forwarded`, OpenHands events, and Docker lifecycle events.

**Env Registry surface (eventually v0003)**:
- `GET /v1/env` — Executor presents executor, routing, task, and scope identifiers as query parameters and receives `KEY=value` pairs before starting OpenHands.

## Router Task List Contract

`GET /v1/tasks?filter=<tag>` returns a task list. `filter` is the routing tag; omitting it returns all tasks.

- `task_id` is the FlowAI platform task identifier created before the task reaches the Executor. It is not created by the OpenHands SDK/server.
- When the Executor submits a task to OpenHands, OpenHands creates its own conversation/run identifier. The Executor keeps an in-memory mapping from FlowAI `task_id` to the OpenHands conversation/run identifier for later interrupt/message actions and includes both identifiers in State Registry event payloads when available.
- A task with `status: queued` is new workload for a matching Executor.
- A running task can include `pending_actions` with `interrupt_task` or `append_task_message`.
- Completed and failed tasks may appear when no filter is supplied for inspection, but Executors ignore them for new work.

The Executor translates queued tasks and pending actions into OpenHands calls. It writes the result to the mocked State Registry. It does not return task results through its own REST API.

## Container Lifecycle

### Identification

Each OpenHands container is started with Docker labels:

- `flowai.executor_id=<executor_id>`
- `flowai.runtime=openhands`
- `flowai.task_id=<task_id>`

These labels make containers discoverable via `docker ps --filter label=flowai.executor_id=<id>` and tie each container to one FlowAI task.

### Start sequence

1. Read configuration.
2. Generate a unique `executor_id` UID for this Executor process instance.
3. Pull the OpenHands image (idempotent; honors `IMAGE_PULL_POLICY`).
4. Resolve env values via `GET /v1/env` on the mocked Env Registry; merge with any literals.
5. Register the active Executor instance with the mocked State Registry via `PUT /v1/executors/{executor_id}` using the generated `executor_id`, configured capacity, and current `running_child_count`.
6. Append `executor.registered` and `executor.healthy` to the mocked State Registry.
7. Enter the ready state. Containers are started per queued task until capacity is full.

### Steady state

- List matching Router tasks via `GET /v1/tasks?filter=<ROUTING_TARGET>`.
- While `running_child_count < EXECUTOR_MAX_CONTAINERS`, start one OpenHands container per queued matching task, append `task.started` and `task.start_message`, submit the task to that task's container, observe OpenHands events, append every intermediate message/event to State Registry, and append `task.finished` or `task.failed` when terminal.
- On a pending `interrupt_task` action for the active task: append `task.cancel_requested`, forward to OpenHands pause endpoint, then append `task.cancelled`/`task.interrupted` or `task.failed` on timeout.
- On a pending `append_task_message` action for the active task: forward to OpenHands event endpoint and append `task.message_forwarded`; any later OpenHands message caused by that input is also journaled as an intermediate task message/event.
- Subscribe to Docker events for the labeled container and append container lifecycle events to State Registry.

### Capacity and scheduling

- `EXECUTOR_MAX_CONTAINERS` is the configured hard limit for concurrently running OpenHands containers in this Executor process.
- `OPENHANDS_HOST_PORT_START` and `OPENHANDS_HOST_PORT_END` define the host port range available for per-container port publishing. The range size must be at least `EXECUTOR_MAX_CONTAINERS`.
- The Executor reports `capacity=EXECUTOR_MAX_CONTAINERS` and current `running_child_count` to the State Registry.
- The Executor does not start more tasks when `running_child_count` equals capacity; it leaves excess queued tasks in Router for later polling or other Executors.

### Failure of an OpenHands container

- If a task container exits unexpectedly, the Executor appends `task.failed` for that task, frees the task slot, and may start another queued task on the next Router listing.
- If Docker or the Executor process itself becomes unhealthy, the Executor appends `executor.failed` and stops accepting new work.

### Executor restart

- On Executor startup, the Executor runs `docker ps -a --filter label=flowai.executor_id=<id>`.
- If previous containers exist, the Executor removes them (no re-attach across Executor restarts).
- The Executor proceeds with a fresh start sequence and appends new State Registry executor events.

### Graceful shutdown

- On SIGTERM, the Executor:
  1. Stops listing Router tasks.
  2. For each in-flight task, tells the corresponding OpenHands container to drain/interrupt with a configurable deadline.
  3. Appends terminal task and executor events to State Registry.
  4. Stops and removes all owned OpenHands containers (`docker stop` then `docker rm`).
  5. Exits.

If the drain deadline elapses, the Executor force-kills the container and exits after appending failure events.

## Executor State Machine

States: `starting`, `registering`, `ready`, `busy`, `draining`, `stopped`.

Transitions:

- `starting → registering` after process init
- `registering → ready` after startup cleanup, State Registry registration, and initial executor event append succeed
- `registering → stopped` if registration, event append, Docker image preparation, or startup cleanup fails after retries
- `ready → busy` when at least one task container is running
- `busy → ready` when all task containers have terminal events appended to State Registry
- `busy → busy` when an individual task container exits and other task containers remain running
- `* → draining` on SIGTERM
- `draining → stopped` after graceful drain or force-kill fallback
- `* → stopped` on fatal error

See `specs/diagrams/03-executor-state-machine.puml`.

## Configuration

| Key | Description | Example |
|---|---|---|
| `MOCKED_SERVER_URL` | Base URL of the mocked task server API | `http://127.0.0.1:8080/v1` |
| `EXECUTOR_API_BIND` | Host:port the Executor's platform health API binds to (localhost-only in v0001) | `127.0.0.1:8020` |
| `ROUTING_TARGET` | Single tag for Router dispatch matching | `openhands` |
| `EXECUTOR_MAX_CONTAINERS` | Maximum concurrent OpenHands containers owned by this Executor | `4` |
| `DOCKER_SOCKET_PATH` | Docker daemon socket | `/var/run/docker.sock` |
| `OPENHANDS_IMAGE` | OCI image reference for the agent runtime | `ghcr.io/openhands/agent-server:latest-python` |
| `OPENHANDS_HOST_PORT_START` | First host port available for OpenHands container port 8000 publishing | `8000` |
| `OPENHANDS_HOST_PORT_END` | Last host port available for OpenHands container port 8000 publishing | `8099` |
| `OPENHANDS_API_KEY` | API key sent in `X-Session-API-Key` header on every OpenHands call (optional in v0001 if server is started without auth) | `oh-sk-...` |
| `OPENHANDS_INTERRUPT_ENDPOINT` | OpenHands route the Executor POSTs to after Router returns `interrupt_task` | `/api/conversations/{conversation_id}/pause` |
| `OPENHANDS_MESSAGE_ENDPOINT` | OpenHands route the Executor POSTs to after Router returns `append_task_message` | `/api/conversations/{conversation_id}/events` |
| `OPENHANDS_MESSAGE_EVENT_TYPE` | OpenHands event type used for Router message actions | `message` |
| `OPENHANDS_INTERRUPT_TIMEOUT_SECONDS` | Time to wait for OpenHands to acknowledge an interrupt before force-stopping | `15` |
| `OPENHANDS_STARTUP_TIMEOUT_SECONDS` | Time to wait for OpenHands `/health` to respond | `60` |
| `OPENHANDS_DRAIN_TIMEOUT_SECONDS` | Graceful drain deadline on SIGTERM | `30` |
| `IMAGE_PULL_POLICY` | `always` \| `if-not-present` \| `never` | `if-not-present` |
| `LOG_LEVEL` | `debug` \| `info` \| `warn` \| `error` | `info` |

## Go Service Baseline

All Go backend services implemented by this change use the platform service baseline:

- Common Go project layout inspired by `golang-standards/project-layout`: `cmd/` for service entrypoints, `internal/` for private application code, `configs/` for YAML config templates, and migration directories for DB-backed services.
- HTTP servers use `net/http` with `chi` routers and middleware.
- Platform probes are `GET /v1/livez` and `GET /v1/readyz`.
- Logs are JSON emitted through the standard library `slog`.
- Config is YAML-first with environment variable overrides.
- PostgreSQL access uses `pgx` with `sqlc`-generated query code.
- Database schema changes use `goose` migrations.

## Executor REST API

The exact schema is defined in `specs/openapi/executor.openapi.yaml`. The API is URI-versioned under `/v1` and exposes only platform health probes:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/livez` | Liveness for the Executor process only |
| `GET` | `/readyz` | Readiness for listing Router workload and executing it through owned OpenHands containers |

The probe convention is platform-wide, not language-specific. `/readyz` reports the Executor's own readiness; it is not an OpenHands proxy endpoint.

## Router-Driven Interruption and Message Flow

Interruption and message injection are triggered only by pending actions returned on Router tasks:

```text
Executor lists GET /v1/tasks?filter=<ROUTING_TARGET>
       │
       ├─ queued task → append task.started/task.start_message → submit prompt to OpenHands
       ├─ in-flight OpenHands output → append intermediate task.message/openhands.event entries
       ├─ pending interrupt_task → append task.cancel_requested → POST OPENHANDS_INTERRUPT_ENDPOINT → append task.cancelled/task.interrupted/task.failed
       └─ pending append_task_message → POST OPENHANDS_MESSAGE_ENDPOINT → append task.message_forwarded
```

SIGTERM during `busy` still uses the same internal interrupt/drain logic, but it is process-local signal handling, not an Executor REST endpoint.

## Agent Runtime Independence

- The Executor pulls a configured OCI image, starts it, supervises its lifecycle, exposes a port, and forwards events.
- The Executor does NOT introspect the agent runtime's internal state beyond what Docker exposes and the OpenHands API responses needed to execute Router actions.
- The Executor does NOT proxy OpenHands through its own API.
- Mid-flight observability of the agent is the responsibility of the agent runtime itself (in-band REST for OpenHands; out-of-band OTel for Claude Code, etc.).

Future Executors for different agent runtimes follow the same pattern and are separate processes.

## Boundaries

- The Executor runs multiple containers up to `EXECUTOR_MAX_CONTAINERS`. Capacity accounting and per-container task ownership are Executor responsibilities; global scheduling remains Router's responsibility.
- The Executor host process never executes agent code; all agent execution is inside the OpenHands container.
- No Kubernetes support in this change.
- No real State Registry, Env Registry, or Router in this change; the mocked task server imitates all three.
- No Web UI, API Gateway, or authentication in this change.
- Only OpenHands is supported in v0001. Claude Code and future agent runtimes are separate Executors.

## Proposed API Contract

- `specs/openapi/executor.openapi.yaml` — OpenAPI 3.1 contract for the Executor's platform health-probe localhost REST API.
- `specs/openapi/router.openapi.yaml` — OpenAPI 3.1 contract for mocked Router task-list polling via `GET /v1/tasks?filter=<tag>`.
- `specs/openapi/state-registry.openapi.yaml` — OpenAPI 3.1 contract for mocked State Registry executor registration plus executor and task event writes.
- `specs/openapi/env-registry.openapi.yaml` — OpenAPI 3.1 contract for mocked Env Registry environment-opening reads.

## Proposed Diagrams

- `specs/diagrams/01-docker-executor-topology.puml` — topology: Executor, mocked task server (3 surfaces), OpenHands container, Docker daemon.
- `specs/diagrams/02-docker-task-lifecycle.puml` — per-task activity: list matching Router tasks → start a task container when capacity allows → submit to OpenHands → observe → forward State Registry events.
- `specs/diagrams/03-executor-state-machine.puml` — Executor process states and transitions.
