# Tasks

## ADRs (must precede implementation)

- [x] Create ADR-0001: choose the implementation programming language/runtime for FlowAI services.
- [x] Create ADR-0002: choose the Docker client/runtime integration approach.
- [x] Create ADR-0003: choose the OpenHands agent runtime image and integration surface.
- [x] Create ADR-0004: lock in bounded multi-container Executor capacity.

## Mocked task server

- [x] Implement mocked task server covering three surfaces (Router task listing with optional `filter`, State Registry Executor registration/events, Env Registry open-env) with the wire shapes documented in `specs/openapi/router.openapi.yaml`, `specs/openapi/state-registry.openapi.yaml`, and `specs/openapi/env-registry.openapi.yaml`.
- [x] Persist State Registry records/events to PostgreSQL using `pgx`, `sqlc` query code, and `goose` migrations.
- [x] Add a fixture `task_spec` for the integration verification scenario.

## Executor implementation

- [x] Implement Executor process startup and YAML configuration reading with env overrides (per `design.md` Configuration).
- [x] Implement OpenHands image pull (honoring `IMAGE_PULL_POLICY`) and per-task `docker run` with the documented labels and port-range publishing.
- [x] Implement OpenHands health-check wait (`/health` polling until `OPENHANDS_STARTUP_TIMEOUT_SECONDS`).
- [x] Implement mocked task server client covering all three surfaces: Router (`GET /v1/tasks?filter=<ROUTING_TARGET>` only), State Registry (Executor registration plus Executor/task events), Env Registry (open-env).
- [x] Implement capacity-aware task forwarding to per-task OpenHands containers with State Registry `task.started` and `task.start_message` journal entries before or alongside submission.
- [x] Implement OpenHands event streaming → mocked State Registry `POST /v1/tasks/{task_id}/events`, including all intermediate in-flight messages/events before terminal state.
- [x] Implement Docker event subscription for the labeled container (start, die, health-check) and forwarding to mocked State Registry.
- [x] Implement per-container failure handling: affected task terminal status, slot release, and continued supervision of unaffected containers.
- [x] Implement Executor startup cleanup: remove leftover containers from previous run via Docker labels.
- [x] Implement graceful shutdown on SIGTERM: stop Router task listing, drain all owned OpenHands containers, append terminal State Registry events, stop containers, exit; force-kill fallback after `OPENHANDS_DRAIN_TIMEOUT_SECONDS`.
- [x] Implement Go service baseline: golang-standards layout (`cmd/`, `internal/`, `configs/`), `net/http` + `chi`, `slog` JSON logs, YAML config, `pgx` + `sqlc`, and `goose` migrations for DB-backed mocked services.
- [x] Implement Executor REST API (`GET /v1/livez` and `GET /v1/readyz` only) bound to `EXECUTOR_API_BIND` with request/response shapes from `specs/openapi/executor.openapi.yaml`.
- [x] Implement Router `interrupt_task` handler: append `task.cancel_requested`, forward `POST {OPENHANDS_INTERRUPT_ENDPOINT}` to OpenHands, wait for state-change acknowledgement, append `task.cancelled`/`task.interrupted`, force-stop fallback with `task.failed` after `OPENHANDS_INTERRUPT_TIMEOUT_SECONDS`.
- [x] Implement Router `append_task_message` handler: forward `POST {OPENHANDS_MESSAGE_ENDPOINT}` to OpenHands with the documented event payload, append `task.message_forwarded` on acknowledgement, and journal any resulting intermediate OpenHands messages.
- [x] Wire SIGTERM-in-busy into the interrupt handler so SIGTERM propagates as an interrupt before graceful shutdown.

## Tests (split per scenario group)

- [x] Tests for mocked task server (all three surfaces): unit tests for `/v1/tasks` with and without `filter`, `/v1/executors/{executor_id}`, `/v1/executors/{executor_id}/events`, `/v1/tasks/{task_id}/events`, `/v1/env` returning the shapes in the corresponding service OpenAPI files under `specs/openapi/`; PostgreSQL persistence for State Registry records/events through `pgx`, `sqlc`, and `goose` migrations.
- [x] Tests for Executor mocked-server client: unit tests for wire format, retries, error handling.
- [x] Tests for OpenHands image pull and capacity-aware `docker run` invocation: integration test against a real Docker daemon covering labels, distinct ports, and `EXECUTOR_MAX_CONTAINERS` limit.
- [x] Tests for OpenHands health-check wait: unit test with mock OpenHands that responds 200 OK after a delay; test that startup timeout triggers correctly.
- [x] Tests for task forwarding: integration test where Executor receives multiple queued tasks from the mocked Router and submits only up to configured capacity to OpenHands containers.
- [x] Tests for event forwarding: integration test where OpenHands emits start, intermediate message/event, cancel, and terminal events and Executor forwards each to mocked State Registry.
- [x] Tests for container failure: integration test where one OpenHands container is killed mid-task and Executor fails only that task while other task containers continue.
- [x] Tests for graceful shutdown: integration test where SIGTERM during busy state drains all owned containers and force-kills leftovers after deadline.
- [x] Tests for Executor REST API: unit tests for `/v1/livez` and `/v1/readyz` only.
- [x] Tests for interrupt propagation: integration test where Router returns `interrupt_task`, OpenHands receives the documented pause request, and the Executor forwards the resulting state-change event to the mocked State Registry.
- [x] Tests for interrupt timeout: integration test where OpenHands does not acknowledge within `OPENHANDS_INTERRUPT_TIMEOUT_SECONDS` and the Executor force-stops the container and posts terminal `failed` status.
- [x] Tests for SIGTERM-as-interrupt: integration test where SIGTERM during busy state triggers the interrupt path before graceful shutdown.
- [x] Tests for message injection: integration test where Router returns `append_task_message`, OpenHands receives the documented event payload, and the Executor appends `task.message_forwarded`.
- [x] End-to-end test: pull OpenHands image, start container, submit a sample task through Router task listing, observe completion, verify events landed in mocked State Registry; second end-to-end test that interrupts mid-task via a Router task `pending_actions` item.

## Sample task verification

- [x] Verify a sample Router task runs end-to-end through an OpenHands container and its events are recorded in the mocked State Registry.

## Documentation

- [x] Validate each service OpenAPI file under `specs/openapi/` with an OpenAPI parser or linter.
- [x] Render proposed diagrams and keep only `.puml` sources in `specs/diagrams/`.
- [x] Run `npx -y @fission-ai/openspec@1.5.0 validate v0001-executor-docker --strict --no-interactive`.