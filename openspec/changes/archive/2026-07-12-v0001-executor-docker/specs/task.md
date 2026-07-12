# Proposed ADRs

This file contains proposed Architectural Decision Records for the v0001-executor-docker change. When the change is accepted, each section below is split into its own file in `docs/adr/NNNN-<slug>.md`.

## ADR-0001: FlowAI backend services use Go

- **Status**: Proposed
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

### Context

FlowAI is a multi-service platform (Executor, Router, State Registry, Env Registry, API Gateway, Web UI, future K8s Executor). The backend services share a need for: long-running daemon behavior, HTTP API servers, Docker daemon interaction (for executors), concurrency, structured logging, and container-friendly single-binary deployment. The Web UI is a separate decision (likely TypeScript) and is out of scope here.

### Decision

Use **Go** (version 1.22 or newer) for all FlowAI backend services. All Go services SHALL use the platform service baseline:

- Repository layout follows the common `golang-standards/project-layout` shape, especially `cmd/`, `internal/`, `configs/`, `scripts/`, `deployments/`, and migration directories where applicable.
- HTTP servers use the standard `net/http` server with `chi` routing.
- Every service exposes platform probes `GET /v1/livez` and `GET /v1/readyz`.
- Logging uses the standard library `slog` with JSON output.
- Configuration is YAML-first, with environment variable overrides for deployment-specific values.
- PostgreSQL access uses `pgx` and generated query code from `sqlc`.
- Database migrations use `goose`.

### Rationale

- **Single static binary** matches the deployment model: one container per service, no runtime to install inside.
- **First-class Docker integration** via the official Docker Engine SDK for Go — the same library Docker itself uses internally.
- **Built-in concurrency** (goroutines, channels) fits the polling/observation patterns in v0001.
- **Strong static typing** and a mature test ecosystem.
- **Small memory footprint, predictable GC** — appropriate for long-running daemons.
- **Neighborhood fit**: every neighboring project (Docker, Kubernetes, Prometheus, Loki, Temporal, etcd) is Go.
- **Standard HTTP stack**: `net/http` plus `chi` keeps services idiomatic, small, and compatible with standard middleware.
- **Database repeatability**: `goose` migrations plus `pgx`/`sqlc` gives versioned schema changes and typed SQL without ORM behavior.
- **Operational consistency**: YAML config, JSON `slog`, and `/livez`/`/readyz` probes make all backend services observable and deployable the same way.

### Consequences

Positive:

- Consistent backend stack across Executor, Router, State Registry, Env Registry, API Gateway.
- Official Docker SDK is the best-maintained and most feature-complete client available.
- Single-binary deployment is easy to roll out, roll back, and scan for CVEs.
- Shared layout and dependencies reduce service-by-service decisions.

Negative:

- The Web UI is still TypeScript/Node; Go is not a fit for the frontend. Two languages in the repo.
- Go's error-handling verbosity (`if err != nil`) is real and accumulates.
- Goroutine leaks are easy to introduce; must enforce structured cancellation and context propagation.
- The service baseline is opinionated and may be heavier than a toy single-file Go service.

### Alternatives considered

- **Rust**: Similar performance/safety, weaker Docker SDK ecosystem, steeper learning curve.
- **TypeScript/Node.js**: Familiar but heavier runtime, weaker Docker integration, more complex production tuning.
- **Python**: Excellent for agent runtime and LLM ecosystem, but poor fit for long-running daemons with strict latency.
- **Java/Kotlin (JVM)**: Mature ecosystem, heavier memory, longer cold start, more operational complexity.

### References

- ADR-0002 (Docker client integration) depends on this decision.
- `golang-standards/project-layout` documents common `cmd/`, `internal/`, `configs/`, and supporting directories.
- `chi` documentation shows idiomatic `net/http` router and middleware composition.

---

## ADR-0002: Docker Executor integrates via the official Docker Engine SDK for Go

- **Status**: Proposed
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

### Context

The Docker Executor must: pull OCI images, create/start/stop/remove containers with labels, observe container state (running, exited, dead), list containers filtered by label (`flowai.executor_id=<id>`), and stream container events.

### Decision

Use the **official Docker Engine SDK for Go** (`github.com/docker/docker/client`) over the Docker daemon UNIX socket. The socket path is configurable via `DOCKER_SOCKET_PATH` (default `/var/run/docker.sock`). All Docker calls go through a thin internal interface so the SDK can be mocked in unit tests.

### Rationale

- **Official library**: same library that Docker itself ships and uses internally.
- **Type-safe Go bindings**: no JSON parsing at the call site; compile-time checks.
- **Native streaming**: the events API streams over the same connection.
- **Label-based filtering**: `--filter label=key=value` is a first-class API in the SDK.
- **Configurable transport**: works over UNIX socket OR TCP; remote Docker hosts become possible later without code changes.
- **Testability**: wrap the SDK behind an internal interface; mock it in unit tests; use real Docker only in integration tests.

### Consequences

Positive:

- No shell-out, no string parsing, no `exec.Command("docker", ...)`.
- Native streaming for container events.
- Same library that Docker itself ships.
- Mockable behind an internal interface, keeping unit tests fast and deterministic.

Negative:

- Tightly coupled to Go (acceptable — ADR-0001 chose Go).
- Library API surface is large; the internal interface must be designed carefully to avoid leaking SDK types into business logic.
- SDK follows Docker daemon versioning; pinning the daemon version in test environments is advisable.

### Alternatives considered

- **CLI shell-out (`docker run`, `docker ps`)**: Trivially simple, but slow, error-prone, awkward for streaming, untestable without a real binary.
- **Direct HTTP REST**: Lower-level control, but reinvents what the SDK already provides and loses type safety.
- **Containerd client**: Lower-level than Docker; aligned with Kubernetes. Not needed for v0001.

### References

- ADR-0001 (Go) is the precondition for this decision.

---

## ADR-0003: v0001 Executor runs the OpenHands agent runtime

- **Status**: Proposed
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

### Context

The Executor must run an agent runtime inside its container. The agent runtime provides: LLM integration, tool execution, browser automation, code sandboxing, and event streams.

The Executor is image-agnostic (it pulls whatever OCI image is configured) but the v0001 sample task fixture needs a concrete choice. The runtime chosen here defines the integration surface (REST API, event format, env vars).

### Decision

v0001 ships the **OpenHands agent runtime**, using the official image `ghcr.io/openhands/agent-server:latest-python`. The Executor integrates via OpenHands' REST/WebSocket API on container port 8000.

### Rationale

- **Production-grade agent runtime**: OpenHands is a mature, actively-maintained open-source agent platform with a Dockerized server distribution.
- **First-class Docker integration**: OpenHands ships `DockerWorkspace` and the `ghcr.io/openhands/agent-server` image specifically for sandboxed agent execution — the exact use case we have.
- **Built-in observability**: OpenHands emits OpenTelemetry traces out of the box (Laminar by default; any OTLP-compatible backend via env vars). The Executor does not need to add observability on top.
- **REST/WebSocket server in each task container**: OpenHands runs as a long-lived HTTP server per accepted task container. The Executor submits tasks via REST and streams events back via WebSocket.
- **Familiar contract**: documented REST API, conversation/task model, and status endpoints. Less integration risk than a less-known runtime.

### Consequences

Positive:

- The Executor has a real, production-grade agent inside, not a stub.
- Observability comes for free via OTel — no Executor-side instrumentation needed.
- Future changes can swap in real Router, State Registry, Env Registry without touching OpenHands integration.
- **Interrupt and message injection surface is built-in**: OpenHands exposes a pause endpoint (`POST /api/conversations/{conversation_id}/pause`) and an event-creation endpoint (`POST /api/conversations/{conversation_id}/events`) that the Executor forwards to. Both endpoints are stable in OpenHands V1 (`OpenHands/software-agent-sdk`, MIT, v1.31.0+).

Negative:

- **Image size**: the `agent-server` image is several GB; cold start is non-trivial. Acceptable for MVP since the container stays warm.
- **OpenHands API coupling**: if OpenHands changes its REST API, the Executor must adapt. Mitigated by wrapping the OpenHands client behind an internal interface AND by making the endpoint paths configurable (`OPENHANDS_INTERRUPT_ENDPOINT`, `OPENHANDS_MESSAGE_ENDPOINT`) so other agent runtimes can be substituted.
- **OpenHands version drift**: `:latest-python` is floating; pinning to a digest is recommended for production but defer to a follow-up change.

### Integration surface used by the Executor

The Executor calls OpenHands via three documented endpoint families (all under `/api/`):

| Purpose | Default endpoint | Config key |
|---|---|---|
| Health check | `GET /health` | (hardcoded) |
| Pause / interrupt a conversation | `POST /api/conversations/{conversation_id}/pause` | `OPENHANDS_INTERRUPT_ENDPOINT` |
| Inject a message into the agent's event queue | `POST /api/conversations/{conversation_id}/events` (event of type `message`, role `user`) | `OPENHANDS_MESSAGE_ENDPOINT` |

Authentication: `X-Session-API-Key` header on every call. Configured via `OPENHANDS_API_KEY`. Optional in v0001 if the OpenHands server is started without auth (development only).

These endpoints are pinned against the OpenHands V1 server (`OpenHands/software-agent-sdk` ≥ v1.31.0). The endpoint paths are stable but the Executor should be tested against the actual OpenHands server version chosen at deployment time.

### Alternatives considered

- **Claude Agent SDK** (`@anthropic-ai/claude-agent-sdk`): Excellent OTel integration via Claude Code CLI. Requires a custom Docker image with Claude Code installed. Deferred to a separate Executor per the "one Executor = one runtime" decision.
- **LangChain / LangGraph**: Library you embed, not a server-in-a-container. Doesn't fit the Executor's container-orchestration model without a custom image.
- **Pydantic AI + Logfire**: Best OTel coverage, but Python-only library; needs custom container.
- **alpine:3.19 with `echo`**: Earlier ADR draft. Useful for testing Executor mechanics but doesn't exercise any real agent observability. Replaced by OpenHands for v0001.
- **Smolagents (HuggingFace)**: Lightweight but observability story is weak; not a fit if observability is on the must-have list.

### References

- v0001-executor-docker design.md "Components" and "Router Task List Contract".
- OpenHands docs: <https://docs.openhands.dev/sdk>

---

## ADR-0004: Executor process runs bounded concurrent containers

- **Status**: Proposed
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

### Context

The Executor must run enough agent containers to be useful as a local worker while preserving predictable resource usage. It could plausibly be designed to:
- Run multiple containers concurrently with a configured capacity limit.
- Run one container and let that container handle concurrent tasks.
- Run one unbounded container per queued task.
- Run one container per Executor process for the lifetime of the Executor.

Each model has different implications for: resource isolation, observability, restart semantics, scheduling complexity, and operational burden.

### Decision

The Executor runs **multiple OpenHands containers concurrently up to configured capacity**. Capacity is controlled by properties such as `EXECUTOR_MAX_CONTAINERS` and the configured host port range. Each accepted FlowAI task gets one OpenHands container labeled with `flowai.executor_id`, `flowai.runtime`, and `flowai.task_id`.

### Rationale

- **Useful local worker capacity**: v0001 can run more than one task without requiring multiple Executor processes.
- **Bounded resource usage**: `EXECUTOR_MAX_CONTAINERS` and port-range validation prevent accidental unbounded Docker growth.
- **Per-task isolation**: each task gets its own OpenHands container and lifecycle events.
- **Simple restart semantics**: Executor death ends owned containers. No re-attach across Executor restarts.
- **Future runtime isolation**: if the platform needs Claude Code support, it spins up a separate Executor process (probably a different image or config), not a multi-runtime abstraction inside one process. This keeps each Executor's blast radius small.
- **Global scheduling remains Router's problem**: Executor enforces local capacity only; Router decides which tasks exist and which tags they carry.

### Consequences

Positive:

- v0001 can exercise real bounded concurrency and per-task container lifecycle.
- Operational mental model remains one container per task — well-understood.
- Different agent runtimes can have different Executor implementations without sharing process boundaries.
- Restart semantics are simple: process death = all owned containers die.

Negative:

- **More bookkeeping**: Executor must track slots, task-to-container mappings, host ports, and per-container terminal state.
- **Container failure is per-task**: if one OpenHands container crashes, only that task fails; remaining containers continue. This is more complex than failing the whole Executor.
- **Port-range configuration matters**: bad ranges can prevent capacity from being usable; validation is required at startup.

### Alternatives considered

- **Exactly one container per Executor**: Simpler, but insufficient for the desired v0001 worker capacity.
- **One long-lived container handling concurrent tasks**: Avoids port/slot bookkeeping but pushes concurrency into OpenHands and weakens task isolation.
- **Unbounded one container per task**: Maximally flexible but unsafe for local resources.
- **Multi-runtime Executor (one process, multiple containers of different types)**: Adds runtime-routing logic inside the Executor; blurs the "Executor = one workload" model. Rejected; use separate Executors per runtime instead.

### References

- v0001-executor-docker proposal.md "What Changes" and "Non-Goals".
- AGENTS.md "Connection matrix" — Executor row is generic; per-runtime Executors are an implementation detail.
