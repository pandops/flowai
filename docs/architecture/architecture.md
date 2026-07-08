# FlowAI Platform — Architecture

> **Status:** Planning phase. No code written yet.
>
> **One-paragraph summary:** FlowAI is a platform that runs **agentic AI tasks** (LLM + tools, multi-turn) inside containers. The system has four independent backend services: a **Router** that is a **task queue manager** — its main purpose is to organize the queue for incoming tasks, hold them, route them to processing services, and accumulate them in a wait list for executors (it has its own database for the queue state: incoming list, processing list, executor wait list, executor registry + capacity + slot accounting); a **State Registry** that owns all historical platform state (tasks records, events log, audit, operator control-request records) and exposes this to operators via the API Gateway; a **Secret Registry** that owns encrypted secrets with two surfaces (`store` write and `open env` read); and an **API Gateway** that is an **auth-aware reverse proxy** — it only validates auth and routes requests, with no business logic and no connection to the Router. **Executors** register with the Router (capacity, ready signals), pull tasks from its wait list, control agent runtime containers, call the Secret Registry for `open env`, and read assigned-task control requests from the State Registry. They write historical events directly to the State Registry and post status updates to the Router. The **Web UI** talks only to the API Gateway. The **Automation layer** (CI, webhook handlers) submits tasks directly to the Router. There are no queues outside the Router. OpenTelemetry traces every hop. Deployment is K8s-native via Helm; dev/test runs on k3d.
>
> **Hard rule #1 — UI talks to the API Gateway only.** No other service.
>
> **Hard rule #2 — The API Gateway is an auth-aware reverse proxy. ONLY logic: validate auth, route to a backend service, return the backend's response.** No business logic. No state. No writes. No aggregation. No caching. The API Gateway is a thin L7 proxy.
>
> **Hard rule #3 — The API Gateway, State Registry, and Secret Registry are independent services.** The API Gateway calls them independently. The State Registry and Secret Registry have **no direct connection**. Neither the API Gateway, the State Registry, nor the Secret Registry has **any connection** to the Router.
>
> **Hard rule #4 — The Router is the task queue manager.** Its main purpose is to organize a queue for incoming tasks, process them through services (not by itself), and accumulate them in a wait list for executors. It owns: the incoming task list, the wait list for executors, the executor pool (registry, configurable `capacity N` per executor, `slots_in_use`), and the in-flight task assignment (which executor is running which task). Executors pull tasks from the Router's wait list.
>
> **Hard rule #5 — Tasks are submitted directly to the Router.** Sources (automation, CI, webhooks) publish task submissions to the Router; the Router places them in its incoming queue for processing. The API Gateway is NOT on the task create path.
>
> **Hard rule #6 — The State Registry owns the historical record and operator control requests.** Task records, event logs, audit trail, control-request records. Executors write historical events directly to the State Registry as a task runs and read control-request records for assigned tasks.
>
> **Diagrams are `.puml` only.** No `.svg` / `.png` / `.html` is checked in. See `README.md` for the rationale and how to render locally.

## Topology

```
                    ┌──────────────┐
                    │    Web UI    │
                    └──────┬───────┘
                           │ HTTPS / WSS
                           ▼
                    ┌──────────────┐
                    │ API Gateway  │  (auth + route, no other logic)
                    └──┬───────┬───┘
                       │       │
                  REST │       │ REST
                       ▼       ▼
              ┌────────────────┐  ┌────────────────┐
              │ State Registry │  │ Secret Registry│  (independent: no direct call)
              └───────┬────────┘  └───────┬────────┘
                      │                   │
                     │  REST       │  REST
                     │   write     │   open env
                     │             │
   ┌─────────────────┐ │             │
   │     Router      │ │             │
   │  queue manager  │ │             │
   │  + executor pool│ │             │
   └──┬──────────┬───┘ │             │
      │          │     │             │
      │ pull     │ register,        │
      │ wait     │ signal ready     │
      │ list     │ capacity         │
      │          │                   │
   ┌──▼────┐  ┌──▼────┐             │
   │ Auto │  │  EX   │◄────────────┘ open env
   └──────┘  └───────┘
```

### Connection rules (enforced)

- **UI ↔ GW**: yes (HTTPS, WS).
- **GW ↔ State Registry**: yes (REST, for state observations and history queries).
- **GW ↔ Secret Registry**: yes (REST, for secret CRUD proxying).
- **Auto ↔ Router**: yes (REST, task submission to incoming queue).
- **EX ↔ Router**: yes (REST, registration, capacity, ready signals, **pull tasks from wait list**, status updates).
- **EX ↔ State Registry**: yes (REST, historical event writes and control-request reads).
- **EX ↔ Secret Registry**: yes (REST, `open env` at task start).
- **UI ↔ State Registry, Secret Registry, Router**: NO. UI goes only through GW.
- **Auto ↔ GW, State Registry, Secret Registry**: NO. Auto goes only through Router for submission.
- **GW ↔ Router**: NO. The API Gateway is isolated from the broker.
- **State Registry ↔ Router**: NO. State Registry is isolated from the broker.
- **Secret Registry ↔ Router**: NO. Secret Registry is isolated from the broker.
- **State Registry ↔ Secret Registry**: NO. They are independent services.

## System context

Source: [`diagrams/01-context.puml`](diagrams/01-context.puml) — C4 System Context (PlantUML)

The platform sits between the user and the agentic-loop world. The user is either a human operating a dashboard (UI → GW → State Registry/Secret Registry), or an automation that submits tasks directly to the Router. Executors pull tasks from the Router's wait list and call the Secret Registry for env values.

## Runtime slice

Source: [`diagrams/02-containers.puml`](diagrams/02-containers.puml) — C4 Runtime slice (PlantUML)

The Router is the queue manager (DB for queue state + executor pool). Executors pull from the wait list and call State Registry and Secret Registry directly for historical events and secrets. UI/GW/State Registry/Secret Registry form an independent operator-facing graph; no node in that graph has a connection to Router.

## Operator surface

Source: [`diagrams/03-operator-surface.puml`](diagrams/03-operator-surface.puml) — C4 User-facing slice (PlantUML)

What a human touches: the Web UI as the single frontend, the API Gateway as its only backend. The UI talks to the API Gateway only — no other service. The API Gateway is a pure proxy: validate auth, route to the right backend service (State Registry or Secret Registry), return the response. Automation submits tasks directly to the Router; humans do not. The Router, State Registry, and Secret Registry are three independent backend services — the Gateway does not broker between them, and they have no connection to each other or to the broker.

Three things live inside the platform that the operator-facing surface interacts with: the API Gateway (proxy), the State Registry (history), and the Secret Registry (encrypted secrets). The Router is in the data plane for task queueing and executor coordination, but it is NOT on the operator-facing surface; the operator only sees what is in the State Registry. Executors are an external fleet.

## Separation of concerns

### Router (task queue manager + executor pool)

- **Main purpose:** organize the queue for incoming tasks, process them through services (not by itself), and accumulate them in the wait list for executors.
- **Owns** the queue state:
  - Incoming task list (where Auto-sourced submissions land)
  - Processing list (tasks being handled by services)
  - Executor wait list (tasks ready to be picked up)
  - Executor pool (registry, configurable `capacity N`, `slots_in_use` per executor)
  - Task assignment state (which executor is running which task)
- **Has its own DB** for queue + executor pool state. Durable across broker restarts.
- **Receives** from producers (`Auto`, etc.) via REST: `POST /tasks` adds to the incoming queue.
- **Processes** through services that subscribe to Router — but Router is not on the data plane for State Registry/Secret Registry/GW (per Hard rule #3).
- **Exposes** a wait list pull API to executors: `GET /tasks/next` (or streaming) returns the next task for the caller.
- **Receives** from executors:
  - `POST /executors` for registration (with `routing_target`, `capacity`, metadata)
  - `POST /executors/:id/ready` for availability
  - `POST /tasks/:id/status` or similar for status updates
- **NO platform state** beyond the queue + executor pool. Historical records live in the State Registry.

### State Registry

- **Sole owner of historical platform state.** Task records (canonical, including terminal status), event logs, audit trail, operator control-request records.
- **Independent service** — no direct connection to the Secret Registry or the Router.
- **Exposes** a REST API used by:
  - The API Gateway (for the UI's read queries — task history, event logs, audit, control-request writes)
  - Executors (for historical event writes and control-request reads during task execution)
- **NO writer role for tasks themselves** — task creation and dispatch are handled by the Router; State Registry records what happened.

### Secret Registry

- **`store` (write-only)** — accepts only `POST /secrets`. Called by the API Gateway (proxying UI requests).
- **`open env` (read)** — given a `scope_token`, returns env-style key=value pairs. Called by Executors at task start.
- **Independent service** — no connection to any other service.

### API Gateway

- **ONLY logic:** validate auth, route to a backend service (State Registry or Secret Registry), return the response.
- **NO business logic.**
- **NO state of its own.**
- **NO writes** (except auth audit).
- **Holds WS connections** to the UI for live event streaming. At WS upgrade, validates the bearer token. Then proxies WS frames between the UI and the State Registry (subscribed events).
- **Proxies operator control requests** to the State Registry as audit-backed records.
- **NO connection to the Router.**

### Web UI

- Thin frontend. Talks to the API Gateway only.
- Authenticates with the API Gateway; receives a bearer token; uses it on every subsequent request.
- Reads historical state from State Registry (via GW).
- Writes secrets via Secret Registry (via GW).

### Automation

- Submits tasks **directly** to the Router via REST. The Router places them in its incoming queue for processing.
- Receives completion callbacks via the Router.

### Executor

- **Registers** with the Router on startup (`POST /executors { routing_target, capacity, metadata }`). The `capacity` is configurable per executor.
- **May supervise up to `capacity` task containers in parallel.** `capacity` is the number of **concurrent containers** the executor can host.
- **Signals availability** to the Router (`POST /executors/:id/ready`).
- **Pulls tasks** from the Router's wait list (`GET /tasks/next`).
- **Calls** the Secret Registry directly via `open env` at task start to mount secrets into the container.
- **Writes historical events** directly to the State Registry as the task runs.
- **Reads operator control-request records** directly from the State Registry for assigned tasks.
- **Posts status updates** to the Router (`POST /tasks/:id/status`).

## Task lifecycle

Source: [`diagrams/04-sequence-task-lifecycle.puml`](diagrams/04-sequence-task-lifecycle.puml) — Sequence (PlantUML)

1. **Submit**: Auto → Router: `POST /tasks`. Router enqueues in incoming list, then routes to processing services. Services process the task (validation, secret scope binding, etc.) and the task moves to the executor wait list.
2. **Observe** (UI): UI → GW: WS connect. GW → State Registry (read state).
3. **Pull & Dispatch**: EX → Router: `GET /tasks/next`. Router returns next task from wait list matching EX's routing_target (and slots_in_use < capacity). EX runs the task.
4. **At task start**: EX → Secret Registry: `open env`. Secret Registry returns env-style keys. EX mounts them in the container.
5. **Agent loop**: AG ↔ LLM (zero platform touch). EX captures stdout events.
6. **Event write**: EX → State Registry: `POST /events` with each event (historical record).
7. **Control requests**: UI → GW → State Registry records operator requests; EX reads pending records for assigned tasks.
8. **Status update**: EX → Router: `POST /tasks/:id/status` (live update).
9. **Finalize**: EX → Router: terminal status (task done in Router's queue). EX → State Registry: `POST /events` with terminal event.
10. **Slot free**: Router decrements `slots_in_use`. Task moves to completed/archived.

## Secret injection

Source: [`diagrams/05-sequence-secrets.puml`](diagrams/05-sequence-secrets.puml) — Sequence (PlantUML)

Operator registers a secret via UI → API Gateway → Secret Registry (write). At task start, the Executor resolves the env via `open env` directly to the Secret Registry and mounts the result as container env / tmpfs. The API Gateway appears once: as a proxy between the UI and the Secret Registry on the write path.

## Task state machine

Source: [`diagrams/06-state-task.puml`](diagrams/06-state-task.puml) — State diagram (PlantUML)

Every task moves through this state graph on the Router: `incoming → processing → wait_list → running → completed`. Terminal states are also persisted in the State Registry for replay in the UI.

## End-to-end flows

Ten activity diagrams, one per flow, walk through every interaction in the system. Each uses PlantUML swimlanes so the actor doing each step is visible at a glance.

| Flow | Source |
|---|---|
| F1 — Operator creates a secret | [`f1-operator-creates-secret.puml`](diagrams/f1-operator-creates-secret.puml) |
| F2 — Automation submits a task | [`f2-automation-submits-task.puml`](diagrams/f2-automation-submits-task.puml) |
| F3 — Operator opens live observation | [`f3-operator-ws-subscribe.puml`](diagrams/f3-operator-ws-subscribe.puml) |
| F4 — Executor registers + pulls tasks | [`f4-router-dispatch.puml`](diagrams/f4-router-dispatch.puml) |
| F5 — Secret resolve at task start | [`f5-secret-resolve.puml`](diagrams/f5-secret-resolve.puml) |
| F6 — Agent loop + live event fanout | [`f6-agent-loop-fanout.puml`](diagrams/f6-agent-loop-fanout.puml) |
| F7 — Operator intervenes mid-loop | [`f7-operator-intervention.puml`](diagrams/f7-operator-intervention.puml) |
| F8 — Task finalization | [`f8-task-finalization.puml`](diagrams/f8-task-finalization.puml) |
| F9 — User cancels a task | [`f9-user-cancel.puml`](diagrams/f9-user-cancel.puml) |
| F10 — Container cleanup, scope expiry | [`f10-cleanup-scope-expires.puml`](diagrams/f10-cleanup-scope-expires.puml) |

Flow relationships:

- **F4** (executor pool + queue pull) is the **steady state** — it shows the executor lifecycle and the task-pull loop from Router's wait list.
- **F1** stands alone — secret registration, pre-task.
- **F2 → F4 → F5 → F6 → F8 → F10** is the happy path for a submitted task.
- **F3** can fire in parallel with F2 (operator opens UI).
- **F7** interrupts F6 from outside.
- **F9** is a cancel.
- **F10** is the universal cleanup.

## What's not in this plan (yet)

Open questions, deferred until implementation:

- **Router delivery semantics** — at-least-once is assumed. Consumers must be idempotent.
- **Router transport** — REST only, or also gRPC / WebSocket?
- **State Registry and Router consistency** — they don't share a database. Eventual consistency between Router queue state and State Registry historical state.
- **Specific agent framework** — OpenHands vs Claude Agent SDK vs LangGraph.
- **Artifact storage** — object store / PVC / local fs.
- **Cost / quota enforcement** — token budgets, timeouts, max concurrent tasks.
- **Network policy / egress allowlist** — what can the agent container reach?
- **Tool registry** — which tools are first-class, how they are versioned, how users add their own.
- **Specific tech stack per component** — decided per component at implementation time.

## How to read this document

1. Skim the system context (diagram 01) and the containers (diagram 02). That's the shape.
2. Read the operator surface (diagram 03) and the **Separation of concerns** section above. Those are the rules.
3. Read the lifecycle (diagram 04) — that is the happy path.
4. Open a diagram source (`.puml`) in a PlantUML-aware editor, or render it locally with `plantuml -tsvg <file>.puml` to see it as an image.
5. The `diagrams` skill (in `.opencode/skills/diagrams`) explains how to render and verify.
