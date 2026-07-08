# AGENTS.md — FlowAI Platform

> **Service catalog index.** Per-service descriptions (purpose, responsibilities, will-not-do) live in OpenSpec source artifacts under `openspec/specs/`. This file is the cross-cutting view: connection matrix, state ownership, cross-cutting rules, and the diagram map.
>
> Last synced with architecture topology on this branch: yes (see git log).

## Service catalog

Each service has its own OpenSpec file. Click through for the full purpose / responsibilities / will-not-do.

- [**Web UI**](openspec/specs/web-ui/spec.md) — thin frontend, sole surface for human operators.
- [**API Gateway**](openspec/specs/api-gateway/spec.md) — auth-aware reverse proxy; sole backend of the Web UI.
- [**Event Router**](openspec/specs/event-router/spec.md) — task queue manager; the only broker in the architecture.
- [**State Store**](openspec/specs/state-store/spec.md) — historical record of the platform; canonical task records, events, audit.
- [**Secret Service**](openspec/specs/secret-service/spec.md) — credential store with two surfaces: `store` (write) and `open env` (read).
- [**Executor**](openspec/specs/executor/spec.md) — worker fleet (service type, multiple instances); runs agent tasks in containers.

## Connection matrix (who calls whom)

| Caller ↓ / Callee → | Web UI | API Gateway | Event Router | State Store | Secret Service | Executor | Automation |
|---|---|---|---|---|---|---|---|
| **Web UI** | — | HTTPS, WS | ❌ no | ❌ no | ❌ no | ❌ no | n/a |
| **API Gateway** | via WS proxy | — | ❌ no | REST (read state) | REST (proxy secret CRUD) | ❌ no | n/a |
| **Event Router** | n/a | ❌ no | — | ❌ no | ❌ no | REST (queue + control + status) | REST (publish task) |
| **State Store** | n/a | REST (respond to query) | ❌ no | — | ❌ no | REST (historical event writes) | n/a |
| **Secret Service** | n/a | REST (respond to secret CRUD) | ❌ no | ❌ no | — | REST (open env at task start) | n/a |
| **Executor** | n/a | ❌ no | REST (register, ready, pull, status) | REST (historical events) | REST (open env) | — | n/a |
| **Automation** | n/a | ❌ no (auth only) | REST (publish task) | n/a (or direct REST for record) | n/a | n/a | — |

**Hard topology rules (encoded in each service OpenSpec file):**

- The **Web UI** talks ONLY to the **API Gateway**.
- The **API Gateway** talks ONLY to the **State Store** and the **Secret Service** (independently; it does not broker between them).
- The **Event Router**, the **State Store**, and the **Secret Service** have **no direct connection with each other**.
- The **State Store** and the **Secret Service** have **no direct connection with each other**.
- The **API Gateway** has **no connection** to the **Event Router**.
- **Executors** talk to the Event Router (queue + control + status), the State Store (historical writes), and the Secret Service (open env). They do not talk to the API Gateway or the Web UI.
- **Automation** (CI, webhooks, future direct UI submission) talks to the **Event Router** for task submission. Automation does NOT talk to the Web UI, the API Gateway, the State Store, or the Secret Service.

## State ownership matrix

| Concern | Owner | Notes |
|---|---|---|
| Incoming task queue | **Event Router** | Transient — until processed and dispatched |
| Processing task list | **Event Router** | Transient |
| Executor wait list | **Event Router** | Tasks ready to be picked up |
| Executor registry | **Event Router** | `routing_target`, `capacity`, `slots_in_use` per executor |
| Task assignment (which executor runs which task) | **Event Router** | In-flight state |
| Task records (canonical history) | **State Store** | Including status, timestamps, parameters |
| Event log (per-task events) | **State Store** | Historical, replayable in the UI |
| Audit trail | **State Store** | Who did what when |
| Routing rules (config) | **State Store** | How incoming tasks are routed to executors |
| Encrypted secret values | **Secret Service** | Ciphertext + metadata |
| Bearer tokens, sessions | **API Gateway** | Stateless per request; no long-lived session store |
| UI state (UI prefs, sessions) | **Client-only** | The Web UI maintains its own; no server-side state |

**The Event Router is the queue manager (transient task state).**
**The State Store is the historical record (canonical platform state).**
**The Secret Service is the credential store.**
**The API Gateway is the auth-aware reverse proxy (no persistent state).**

## Cross-cutting rules

### What "the Web UI talks to the API Gateway only" means in practice

- The Web UI has a single base URL: the API Gateway. There is no other base URL configured.
- The Web UI's HTTP client refuses to send requests to any origin other than the API Gateway. (Defense in depth: even a bug in the UI cannot leak.)
- The Web UI does not use any background process that calls a backend service.

### What "the API Gateway has no connection to the Event Router" means

- The gateway's outbound allow-list contains only the State Store and the Secret Service.
- The gateway's deployment does not deploy ER's address or credentials.
- The WebSocket frames from the UI that the gateway proxies go to the State Store's event subscription endpoint, not to the Event Router.

### What "the State Store and the Secret Service are independent" means

- They do not share a database (Postgres or otherwise). Each can be scaled, replaced, and versioned independently.
- The State Store never calls Secret Service. Even if a task payload references a `secret_id`, the State Store stores only the id, never the value.
- A failure of one does not propagate to the other. A Secret Service outage does not block task recording in the State Store.

### What "Automation submits tasks to the Event Router" means

- Automation does not go through the Web UI or the API Gateway for task submission.
- Automation authenticates with the API Gateway once, receives a bearer token, and uses that token when calling the Event Router's `POST /tasks`.
- An Automation writer does not need to know about the State Store, the Secret Service, the Web UI, or the API Gateway beyond auth.

---

## How diagrams relate to this document

Every architecture diagram (`.puml` files in `docs/architecture/diagrams/`) is a visualization of the relationships described here and in the per-service OpenSpec files. If the diagrams and these files disagree, **the per-service OpenSpec files win for service behavior, and this file wins for cross-cutting topology**. Update the relevant file first, then redraw.

The diagram ↔ catalog map:

| Diagram | What it visualizes |
|---|---|
| `01-context.puml` | System context (people, system, external systems) |
| `02-containers.puml` | Runtime slice; connection matrix (data plane) |
| `03-operator-surface.puml` | Operator surface (UI, GW, SS, SR); no ER |
| `04-sequence-task-lifecycle.puml` | Lifecycle across all services |
| `05-sequence-secrets.puml` | Secret write (UI→GW→SR) and open env (EX→SR) |
| `06-state-task.puml` | Per-task state machine inside the Event Router |
| `f1`–`f10` | Per-flow activity diagrams; swimlanes map to the services in the catalog above |
