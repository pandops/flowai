# AGENTS.md — FlowAI Platform

> **Planning-phase architecture repository.** No runtime service is implemented
> yet. Current-state files describe only what exists now; planned behavior lives
> under active OpenSpec changes until implementation, verification, acceptance,
> and sync.
>
> **Service catalog index.** Per-service planned target descriptions (purpose,
> responsibilities, will-not-do) live in OpenSpec change artifacts under
> `openspec/changes/*/specs/` until accepted into real state. This file is the

## Repository layout convention: one directory per service, no shared code

Each runtime service in this repository MUST live under its own top-level
directory named after the service. The directory is fully self-contained:
every package, binary, config, migration, and test lives inside it. **No
shared code between services** — only third-party libraries. Cross-cutting
concerns (HTTP scaffolding, logging, wire types, persistence) are duplicated
per service so each service can evolve independently.

```
mocked-task-server/               # v0001 Mocked Task Server (a separate service)
├── cmd/mocked-task-server/
├── configs/mocked-task-server.yaml
├── internal/
│   ├── config/                   # YAML loader (this service only)
│   ├── httpapi/                  # chi scaffolding (this service only)
│   ├── logging/                  # slog setup (this service only)
│   ├── platform/                 # wire types (this service's view of the contract)
│   ├── server/                   # the HTTP server implementation
│   │   ├── server.go
│   │   └── server_test.go
│   └── store/                    # ephemeral in-memory mocked service state
└── test/                         # integration tests for this service

executor-docker/                  # v0001 Docker Executor
├── cmd/docker-executor/
├── configs/docker-executor.yaml
├── internal/
│   ├── config/                   # YAML loader (this service only)
│   ├── httpapi/                  # chi scaffolding (this service only)
│   ├── logging/                  # slog setup (this service only)
│   ├── platform/                 # wire types (this service's view of the contract)
│   ├── dockerclient/             # Docker REST client (executor-only)
│   ├── openhands/                # OpenHands REST client (executor-only)
│   ├── mockedclient/             # Mocked-task-server HTTP client (executor-only)
│   ├── executor/                 # Core Executor logic + state machine
│   │   ├── executor.go
│   │   ├── executor_unit_test.go # Pure unit tests (config validation, etc.)
│   │   └── ...
│   └── mocks/                    # Test fakes for executor-only interfaces
│       ├── docker/
│       └── openhands/
└── test/                         # integration tests for this service

autotest/                         # Cross-service Playwright e2e tests (root only)
└── (placeholder for v0006+; v0001 has no UI yet)

docs/adr/                         # Architectural Decision Records
```

Rules:

1. **No shared code between services.** Each service duplicates cross-cutting
   concerns (HTTP scaffolding, logging, wire types, persistence) inside its
   own `internal/`. Wire types are duplicated per service because each service
   owns its view of the contract — the OpenAPI specs are the single source of
   truth and each service defines Go types that match them.
2. **Per-service boundaries.** Service-specific code (interfaces that the
   service uses, clients to external systems, test fakes of those clients,
   database migrations) lives under that service's directory. Go's `internal/`
   rule means each service's internals can only be imported by packages
   sharing the same prefix — this is intentional and prevents cross-service
   coupling.
3. **Test fakes follow the package they fake.** A test fake for an
   executor-only interface (e.g. `dockerclient.Client`) goes under
   `executor-docker/internal/mocks/`. Test fakes for a shared component (e.g.
   the mocked task server, which is itself a service) live with that service.
4. **Per-service `test/` directory.** Unit and integration tests for a
   single service live under `<service>/test/`. These tests use only the
   service's own packages + stdlib + in-process fakes. They do NOT import
   packages from other services (Go's `internal/` rule prevents this anyway).
5. **Cross-service e2e tests live in `autotest/` at the root.** Tests that
   span more than one service — typically Playwright e2e tests — live at
   `autotest/` in the repo root, NOT inside any service directory. They
   drive running services via their public HTTP/CLI surfaces.
6. **One `go.mod` at the root.** The whole repository is a single Go module
   (`github.com/flowai/platform`); service directories are organization only.
7. **Future services follow the same pattern.** When v0004-router,
   v0005-executor-k8s, etc. land, they each get their own top-level directory
   (`router/`, `executor-k8s/`). Each is fully self-contained.
8. **The v0001 mocked task server is stateless.** Its Router, State Registry,
   and Env Registry surfaces keep ephemeral in-memory data only. Durable
   PostgreSQL persistence belongs to the real State Registry introduced by
   `v0002-state-registry`, not to `mocked-task-server/`.
> cross-cutting view: connection matrix, state ownership, cross-cutting rules,
> and the diagram map.
>
> Last synced with architecture topology on this branch: yes (see git log).

## OpenSpec management rules

- `openspec/specs/` is the canonical real-state baseline for behavior that has
  been implemented, verified, accepted, and synced.
- `openspec/changes/*/specs/` contains proposed spec deltas.
- `openspec/changes/*/specs/diagrams/` contains proposed `.puml` diagrams for a
  change.
- `docs/adr/` contains accepted Architectural Decision Records: why current
  architecture choices were made.
- `docs/architecture/diagrams/` contains `.puml` diagrams for implemented
  current state only. It stays empty when no current implementation exists.
- `openspec/changes/*/tasks.md` contains implementation steps for a change.
- Do not treat a spec requirement as implemented merely because it is in the
  baseline; implementation status requires code and verification artifacts.
- Validate OpenSpec before implementation and before completion:
  `npx -y @fission-ai/openspec@1.5.0 validate <change-id>` for changes and
  `npx -y @fission-ai/openspec@1.5.0 validate --all` for the full baseline.
- Planned diagrams move with the change they describe. Sync diagrams to
  `docs/architecture/diagrams/` only after the change is implemented and
  accepted as current state.

## Target service catalog

Each planned target service has an OpenSpec file inside an active change. Click through for the full purpose / responsibilities / will-not-do.

- [**Docker Executor**](openspec/changes/v0001-executor-docker/specs/executor/spec.md) — first local worker type; controls only Docker container lifecycle for agent runtimes.
- [**State Registry**](openspec/changes/v0002-state-registry/specs/state-registry/spec.md) — historical record of the platform; canonical task records, events, audit.
- [**Env Registry**](openspec/changes/v0003-env-registry/specs/env-registry/spec.md) — executor environment store with non-secret env vars and encrypted secrets; two surfaces: `store` (write) and `open env` (read).
- [**Router**](openspec/changes/v0004-router/specs/router/spec.md) — task queue manager; the only broker in the architecture.
- [**K8s Executor**](openspec/changes/v0005-executor-k8s/specs/executor/spec.md) — cluster worker type; controls only Kubernetes Pod lifecycle for agent runtimes.
- [**Web UI**](openspec/changes/v0006-web-ui/specs/web-ui/spec.md) — thin frontend, sole surface for human operators, initially without auth.
- [**API Gateway**](openspec/changes/v0006-web-ui/specs/api-gateway/spec.md) — Web UI's sole backend, initially no-auth; auth is added by [**Auth**](openspec/changes/v0007-auth/specs/auth/spec.md).

## Planned final connection matrix (who calls whom)

This matrix describes the target after the numbered Executor, registry, Router,
Web UI, and Auth changes land. `v0001-executor-docker` uses a mocked task server
for Docker Executor registration, polling, and status until `v0004-router` exists.

| Caller ↓ / Callee → | Web UI | API Gateway | Router | State Registry | Env Registry | Executor | Automation |
|---|---|---|---|---|---|---|---|
| **Web UI** | — | HTTPS, WS | ❌ no | ❌ no | ❌ no | ❌ no | n/a |
| **API Gateway** | via WS proxy | — | ❌ no | REST (read state, proxy control requests) | REST (proxy env/secret CRUD) | ❌ no | n/a |
| **Router** | n/a | ❌ no | — | ❌ no | ❌ no | REST (queue + status) | REST (publish task) |
| **State Registry** | n/a | REST (respond to query) | ❌ no | — | ❌ no | REST (historical event writes) | n/a |
| **Env Registry** | n/a | REST (respond to env/secret CRUD) | ❌ no | ❌ no | — | REST (open env at task start) | n/a |
| **Executor** | n/a | ❌ no | REST (register, ready, pull, status, running-child count) | REST (historical events, control reads) | REST (open env) | — | n/a |
| **Automation** | n/a | ❌ no (auth only) | REST (publish task) | n/a (or direct REST for record) | n/a | n/a | — |

**Hard final topology rules (encoded in active OpenSpec change files):**

- The **Web UI** talks ONLY to the **API Gateway**.
- The **API Gateway** talks ONLY to the **State Registry** and the **Env Registry** (independently; it does not broker between them).
- The **Router**, the **State Registry**, and the **Env Registry** have **no direct connection with each other**.
- The **State Registry** and the **Env Registry** have **no direct connection with each other**.
- The **API Gateway** has **no connection** to the **Router**.
- **Executors** talk to the Router (queue + status + running-child count), the State Registry (historical writes and control-request reads), and the Env Registry (open env). They do not talk to the API Gateway or the Web UI.
- **Automation** (CI, webhooks, future direct UI submission) talks to the **Router** for task submission. Automation does NOT talk to the Web UI, the API Gateway, the State Registry, or the Env Registry.

## Planned final state ownership matrix

| Concern | Owner | Notes |
|---|---|---|
| Incoming task queue | **Router** | Transient — until processed and dispatched |
| Processing task list | **Router** | Transient |
| Executor wait list | **Router** | Tasks ready to be picked up |
| Executor registry | **Router** | `executor_type`, `routing_target`, `capacity`, `slots_in_use`, `running_child_count` per executor |
| Task assignment (which executor runs which task) | **Router** | In-flight state |
| Task records (canonical history) | **State Registry** | Including status, timestamps, parameters |
| Event log (per-task events) | **State Registry** | Historical, replayable in the UI |
| Audit trail | **State Registry** | Who did what when |
| Operator control requests | **State Registry** | Audit-backed requests read by Executors for assigned tasks |
| Executor environment definitions | **Env Registry** | Non-secret env vars, secret references, scope metadata |
| Encrypted secret values | **Env Registry** | Ciphertext + metadata |
| Bearer tokens, sessions | **API Gateway** | Stateless per request; no long-lived session store |
| UI state (UI prefs, sessions) | **Client-only** | The Web UI maintains its own; no server-side state |

**The Router is the queue manager (transient task state).**
**The State Registry is the historical record (canonical platform state).**
**The Env Registry is the executor environment store and encrypted secret store.**
**The API Gateway is the auth-aware reverse proxy (no persistent state).**

## Cross-cutting rules

### What "the Web UI talks to the API Gateway only" means in practice

- The Web UI has a single base URL: the API Gateway. There is no other base URL configured.
- The Web UI's HTTP client refuses to send requests to any origin other than the API Gateway. (Defense in depth: even a bug in the UI cannot leak.)
- The Web UI does not use any background process that calls a backend service.

### What "the API Gateway has no connection to the Router" means

- The gateway's outbound allow-list contains only the State Registry and the Env Registry.
- The gateway's deployment does not deploy Router's address or credentials.
- The WebSocket frames from the UI that the gateway proxies go to the State Registry's event subscription endpoint, not to the Router.

### What "the State Registry and the Env Registry are independent" means

- They do not share a database (Postgres or otherwise). Each can be scaled, replaced, and versioned independently.
- The State Registry never calls Env Registry. Even if a task payload references an env or secret id, the State Registry stores only identifiers, never env values or secret values.
- A failure of one does not propagate to the other. An Env Registry outage does not block task recording in the State Registry.

### What "Automation submits tasks to the Router" means

- Automation does not go through the Web UI or the API Gateway for task submission.
- Automation authenticates with the API Gateway once, receives a bearer token, and uses that token when calling the Router's `POST /tasks`.
- An Automation writer does not need to know about the State Registry, the Env Registry, the Web UI, or the API Gateway beyond auth.

---

## How diagrams relate to this document

Proposed architecture diagrams (`.puml`) live under `openspec/changes/*/specs/diagrams/`. Current-state architecture diagrams live under `docs/architecture/diagrams/` only after implementation is accepted. If diagrams and specs disagree, **the active OpenSpec change files win for planned service behavior, baseline specs win for current behavior, and this file wins for cross-cutting topology**. Update the relevant spec first, then redraw.

The diagram ↔ catalog map:

| Diagram | What it visualizes |
|---|---|
| `openspec/changes/*/specs/diagrams/*.puml` | Proposed system/design diagrams for a change |
| `docs/architecture/diagrams/*.puml` | Implemented current-state system/design diagrams |
