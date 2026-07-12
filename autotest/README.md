# autotest/

This directory is for **cross-service end-to-end (e2e) tests** that exercise
more than one FlowAI service together — typically via Playwright driving real
HTTP endpoints.

Tests in this directory are NOT scoped to a single service. They are the
**only** place where multiple services are wired up and verified as a system.

For unit tests and per-service integration tests, see each service's own
`test/` directory:

- `mocked-task-server/test/server_test.go` — server unit tests (in-process)
- `executor-docker/test/executor_test.go` — executor integration tests (in-process fakes)
- `executor-docker/test/container_execution_test.go` — container-execution unit tests (in-process fakes)
- `executor-docker/internal/mocks/docker/` — fake Docker client (in-process)

## Test files

| File | Tests | Style | What it covers |
|---|---|---|---|
| [`docker-executor/tests/executor.spec.ts`](docker-executor/tests/executor.spec.ts) | 8 | Cross-service (real binaries) | Wire-contract e2e: probes, task listing, env, registration, API surface limits |
| [`docker-executor/tests/container-execution.spec.ts`](docker-executor/tests/container-execution.spec.ts) | 3 | Cross-service (real binaries + real Docker daemon) | Real container execution against the real OpenHands V1 agent-server (health-failure, V1 happy-path with fake LLM, V1 interrupt) |

**Total: 11 Playwright tests, all passing.**

The two files correspond to two distinct concerns:
- `executor.spec.ts` — does the executor talk the right wire protocol? (no Docker required)
- `container-execution.spec.ts` — does the executor actually drive a real daemon to create a container? (requires a Docker-compatible daemon + the OpenHands image)

## Running

```bash
cd autotest/docker-executor
npm install          # one-time
npm test             # runs all 11 tests
npm run test:report  # opens the HTML report
```

`FLOWAI_DOCKER_SOCKET` is forwarded to the spawned executor so the test
harness can drive a rootless podman daemon at `/run/user/1000/podman/podman.sock`
without hard-coding the path. `workers: 1` keeps the shared subprocess
state isolated between specs — each spec runs to completion before the next
starts, and per-spec alt ports (MOCKED_BIND_ALT / EXECUTOR_BIND_ALT plus
per-spec container port ranges) further prevent bleed across runs.

The HTML report lives at `playwright-report/index.html` after every run.

---

## Test cases

### `executor.spec.ts` — Cross-service wire-contract e2e (8 tests)

These tests spawn the real `mocked-task-server` and `docker-executor` binaries
and drive them via Playwright's `request` fixture. The Docker daemon is **not
required** — these tests verify that the executor talks the right protocol to
the mocked task server.

#### `mocked-task-server serves /v1/livez`

- **Setup**: spawn `mocked-task-server` (port 18080) + `docker-executor` (port 18020)
- **Action**: `GET /v1/livez` against the mocked task server
- **Assert**: response status is 200; `body.status === "ok"`
- **Purpose**: confirms the executor's helper code can probe the mocked task server
  and the mocked task server is alive.

#### `mocked-task-server router task list returns empty when no tasks are seeded`

- **Setup**: both services spawned, no tasks seeded
- **Action**: `GET /v1/tasks?filter=openhands` against the mocked task server
- **Assert**: response is 200; `body.tasks` is an empty array
- **Purpose**: confirms the router task-list endpoint returns the expected
  empty-list shape when no tasks match the filter.

#### `mocked-task-server env endpoint returns 204 when scope is not configured`

- **Setup**: both services spawned, no env scopes seeded
- **Action**: `GET /v1/env?executor_id=…&routing_target=openhands&scope_token=missing-scope`
- **Assert**: response status is 204
- **Purpose**: confirms the env endpoint correctly signals "no values
  configured" via 204, not 200 with an empty body or 404.

#### `mocked-task-server rejects env lookup without scope_token`

- **Setup**: both services spawned
- **Action**: `GET /v1/env?executor_id=…&routing_target=openhands` (no `scope_token`)
- **Assert**: response status is 400
- **Purpose**: confirms the env endpoint validates required query parameters
  and returns 400 on missing `scope_token`.

#### `docker-executor livez responds on the configured bind`

- **Setup**: both services spawned
- **Action**: `GET /v1/livez` against the docker-executor
- **Assert**: response is 200; `body.status === "ok"`; `body.executor_id` is a non-empty string
- **Purpose**: confirms the executor's platform health API responds on the
  configured bind address and exposes the auto-generated executor_id.

#### `docker-executor readyz endpoint exists`

- **Setup**: both services spawned
- **Action**: `GET /v1/readyz` against the docker-executor
- **Assert**: response is 200 or 503 (both are valid readiness states)
- **Purpose**: confirms the readyz endpoint exists and is reachable; the
  specific status depends on whether the executor has had a chance to
  register yet (200) or is still in startup (503).

#### `docker-executor platform API is limited to health probes only`

- **Setup**: both services spawned
- **Action**: for path in `[/v1/tasks, /v1/tasks/abc-123/events, /v1/executors]`,
  send `POST` to the docker-executor
- **Assert**: response is 404 or 405 for all three paths
- **Purpose**: enforces the design rule that the executor exposes ONLY
  `/v1/livez` and `/v1/readyz` on its platform health API. Task-control
  endpoints belong to the Router/State Registry surfaces, never the
  executor. A 404 on these paths confirms the surface is restricted.

#### `docker-executor registers with mocked State Registry`

- **Setup**: both services spawned, executor's mocked State Registry
  endpoint is up
- **Action**: GET `/v1/livez` to discover `executor_id`, then PUT
  `/v1/executors/{executor_id}` with a registration body
- **Assert**: response is 200 or 201 (idempotent re-registration)
- **Purpose**: confirms the executor's startup registration flow works
  end-to-end against the mocked task server.

---

### `container-execution.spec.ts` — Real-container e2e (3 tests)

These tests spawn the same services as above, but also configures the
docker-executor to use the real OpenHands V1 agent-server image
(`agent-openhands-image:latest`). A TCP-to-UNIX-socket proxy is set up so
the test can drive the local podman daemon via Playwright's TCP-only request
fixture.

The harness starts a tiny in-process OpenAI-compatible LLM server (FakeLLM
in helpers) so the V1 agent-server can resolve chat completions during the
real-V1 happy-path test without external secrets. The fake LLM is reachable
from the container via `host.containers.internal` (rootless podman and
docker-desktop both resolve it to the host's loopback).

**Prerequisites**:
- A Docker-compatible daemon reachable at `FLOWAI_DOCKER_SOCKET` (default
  `/run/user/1000/podman/podman.sock`)
- The `agent-openhands-image:latest` image present locally (built from
  `autotest/agent-openhands-image/Dockerfile`)

#### `executor pulls the image, creates a labeled container, and cleans up on health-check failure`

- **Setup**:
  - spawn TCP→UNIX proxy on a random local port
  - spawn `mocked-task-server` (port 18081) and `docker-executor` (port 18021)
    with `DOCKER_SOCKET_PATH` pointing at the local daemon, `OPENHANDS_IMAGE`
    set to `agent-openhands-image:latest`, and the test-only port range
    20000–20010
  - enable the test-mode endpoints on the mocked task server
    (`MOCKED_SERVER_TEST_MODE=true`) and seed an initial empty state
  - seed a queued task via `POST /v1/_test/tasks` with a known task_id

- **Action (assertion 1 — discovery)**: poll the executor's `/v1/livez` to
  discover the generated `executor_id`; clean up any leftover containers
  with that label from previous runs

- **Action (assertion 2 — container creation)**: poll the Docker daemon's
  `/v1.41/containers/json?all=true&filters={label:[flowai.executor_id=…]}`
  endpoint until a container with our executor_id label appears
- **Assert**: container exists; its `Image` references the configured
  OpenHands image; its labels include `flowai.executor_id=<id>`,
  `flowai.runtime=openhands`, and `flowai.task_id=<seeded id>`

- **Action (assertion 3 — cleanup)**: poll the Docker daemon until the
  container is gone
- **Assert**: no containers with our label remain

- **Purpose**: proves end-to-end that the executor:
  1. talks to a real daemon (not in-process fakes)
  2. creates containers with the correct labels
  3. submits the task to OpenHands (`task.started` + `task.start_message`
     events are journaled to the mocked task server before the container is
     cleaned up)
  4. cleans up containers on shutdown

When run with `alpine:3.21` (which has no `/health`), the executor fails
the health check and tears down the container ~200ms after creation. When
run with `agent-openhands-image:latest` (the real V1 agent-server), the
container stays alive until test end and is removed by `stopServices`.

#### `executor records V1 task events through the real V1 agent-server`

- **Setup**:
  - spawn `mocked-task-server` (port 18082) and `docker-executor` (port 18022)
    with the local in-process OpenAI-compatible LLM stub wired in via
    `OPENHANDS_LLM_BASE_URL=http://host.containers.internal:<fake-llm-port>/v1`
  - the executor must satisfy the V1 conversation-start contract: it
    POSTs `workspace` + `agent_profile_id` (or inline `agent.llm.model/api_key/usage_id`)
    and a structured `initial_message` with `content` and `run` to
    `/api/conversations`, then the V1 server returns `{id}` and the executor
    dials the WS at `/sockets/events/{id}`.

- **Action**:
  - seed a task and `await waitForTaskEvent(handles, taskId, 'task.started', 60_000)`.
  - then `await waitForTaskEvent(handles, taskId, 'openhands.conversation_started', 90_000, predicate)`
    — this is the deterministic real-runtime signal that the
    V1 server accepted the conversation. `predicate` requires
    `payload.openhands_conversation_id` to be a string.
  - then poll for an intermediate `openhands.event` frame and a terminal
    `task.finished` (the LLM stub always returns a static assistant
    message so the V1 server closes the conversation cleanly).

- **Assert**:
  - the conversation_started event was journaled (so the V1 wire shape
    matches the documented contract)
  - at least one openhands.event frame was journaled
  - exactly one terminal event was journaled (`task.finished`)

- **Purpose**: end-to-end confirmation that the V1 conversation-startup
  contract (workspace + initial_message + agent or profile_id) lands a
  real V1 agent-server and that the executor's stream loop consumes its
  events.

#### `executor forwards Router pending_actions interrupt_task to OpenHands and records the cancel chain`

- **Setup**:
  - same wiring as the V1 happy-path test, but with
    `initialRun: false` (initial_message.run=false) so the V1 server
    does not enter the agent loop — the test depends on the executor's
    interrupt path alone.
  - `openHandsInterruptTimeout: 5` so the cancel times out within the
    test's 30s window without needing a live LLM response.

- **Action**:
  - seed a task and `await waitForTaskEvent(handles, taskId, 'task.started', 60_000)`.
  - `await waitForTaskEvent(handles, taskId, 'openhands.conversation_started', 90_000, predicate)`
    to confirm the V1 server accepted the conversation.
  - then `await replaceTask(handles, taskId, { pending_actions: [interrupt_task, ...] })`
    and wait for `task.cancel_requested`. Wait for exactly one
    `task.failed`/`task.interrupted`/`task.finished` terminal.

- **Assert**:
  - `task.cancel_requested` was journaled (Router → Executor → OpenHands
    interrupt path landed)
  - exactly one terminal event was journaled (no double-firing; the
    executor's interrupt_timeout path emits `task.failed phase=interrupt_timeout`)

- **Purpose**: the documented Router → Executor → OpenHands interrupt chain
  works end-to-end with the real V1 agent-server.

---

## Why this is "cross-service" and not "unit"

The user-facing rule (recorded in `AGENTS.md`) is:

- **Unit + integration tests** for ONE service live in `<service>/test/`
  and use in-process fakes for the service's external dependencies.
- **Cross-service e2e tests** for multiple services together live in
  `autotest/` and drive real binaries via HTTP.

This split is enforced by Go's `internal/` package rule: a test in
`executor-docker/test/` cannot import `mocked-task-server/internal/...`
because the latter is under a sibling service's `internal/` tree. So tests
that genuinely need both services running **must** live in `autotest/`,
where the cross-service import constraint is lifted.

The two spec files in this directory represent the two levels of e2e
fidelity:
- `executor.spec.ts` — does the wire protocol work? No Docker required.
- `container-execution.spec.ts` — does a real container actually run?
  Requires a daemon.

If the OpenHands agent-server image is unavailable, only the second
describe-block is affected; the first 8 still validate the protocol
end-to-end. If the daemon is missing, the V1 tests are fatal: the
executor publishes `executor.failed` and exits (this is by design — the
mocked task server cannot supervise containers without a real daemon).

### Where the report is

After every `npm test` run, two outputs are written:

- **`test-results/.last-run.json`** — machine-readable summary of the last run
  (just pass/fail counts and durations).
- **`playwright-report/index.html`** — full HTML report. Open in a browser to
  see per-test timing, error traces, network logs, and stdout/stderr for
  each spawned subprocess.

The HTML report is a single-file React SPA that requires no server.
It includes the test results inline so it works from `file://` URLs and can
be archived as a build artifact.

To view the latest report after a test run:

```bash
cd autotest/docker-executor
npm run test:report      # opens playwright-report/index.html in the default browser
```

`npm test` regenerates both outputs each time, so the report always reflects
the most recent run.
</content>