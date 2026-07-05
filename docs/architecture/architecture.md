# FlowAI Platform — Architecture

> **Status:** Planning phase. No code written yet.
>
> **One-paragraph summary:** FlowAI is a platform that runs **agentic AI tasks** (LLM + tools, multi-turn) inside containers. The system has two layers: a thin **API Gateway** that is an **auth-aware reverse proxy** (its only logic is to validate auth, route to a backend service, and return the backend's response — no business logic, no state, no writes) and a set of **backend services** that own all writes and process logic: an **Event Router** (central message broker + state holder, owns its own database, receives task submissions and live events via REST, maintains an executor pool (executors register and signal availability; ER matches pending tasks to available executors and dispatches), pushes live events to subscribers, sends REST queries to the Secret Service, does NOT modify message content), a **Secret Service** with two surfaces — `store` (write-only, called by the API Gateway on behalf of the UI) and `open env` (read, called by the Event Router and the Executors), and **Executors** that register with the Event Router on startup, signal availability, start containers locally or in Kubernetes behind a single interface. Containers run pre-built images of external agent frameworks (OpenHands, Claude Agent SDK, LangGraph, ...) over a defined contract. There are **no queues** — every task, event, and piece of state lives in the Event Router's database. OpenTelemetry traces every hop. Deployment is K8s-native via Helm; dev/test runs on k3d.
>
> **Hard rule #1 — UI talks to the API Gateway only.** No other service.
>
> **Hard rule #2 — The API Gateway is an auth-aware reverse proxy. ONLY logic: validate auth, route to a backend service, return the backend's response.** No business logic. No state. No writes. No aggregation. No caching. WS connections from the UI are accepted, validated, and then proxied to the Event Router; live event frames from the Event Router are proxied back to the UI.
>
> **Hard rule #3 — There are no queues.** Every task and event lives in the Event Router's database. Producers REST-POST to the Event Router. Consumers REST-poll (or REST-subscribe) the Event Router.
>
> **Hard rule #4 — All write operations and process logic live in backend services** (Event Router, Secret Service, Executors). The API Gateway never writes anything except the audit log of auth events (which itself can be a backend call).
>
> **Hard rule #5 — Tasks are created without the API Gateway.** Sources (automation, CI, webhooks, future direct UI submission) REST-POST directly to the Event Router. The Event Router is the single entry point for task creation. The API Gateway is not on the create path.
>
> **Hard rule #6 — The Event Router owns the executor pool and waits for available executors.** Executors register with the Event Router on startup, signal availability, and accept dispatched tasks. The Event Router matches pending tasks to available executors and dispatches. Dispatch is push from the Event Router to the matched executor, but only when the executor is available.
>
> **Diagrams are `.puml` only.** No `.svg` / `.png` / `.html` is checked in. See `README.md` for the rationale and how to render locally.

## System context

Source: [`diagrams/01-context.puml`](diagrams/01-context.puml) — C4 System Context (PlantUML)

The platform sits between the user and the agentic-loop world. The user is either a human operating a dashboard, or an automation (CI, scheduler, webhook) that authenticates with the API Gateway and submits tasks directly to the Event Router. All other traffic is operator -> UI -> API Gateway -> backend service.

## Task flow (runtime slice)

Source: [`diagrams/02-containers.puml`](diagrams/02-containers.puml) — C4 Runtime slice (PlantUML)

No queues. Automation submits via REST to the Event Router. The Event Router owns its database (tasks, events, routing rules, audit). It dispatches via REST to executors and receives live events from them via REST. The API Gateway is a thin auth+route proxy: it sits between the UI and the backend services (Event Router, Secret Service) but does no work of its own. The Secret Service has two surfaces: `store` (write, called via API Gateway proxying UI) and `open env` (read, called directly by the Event Router and the Executors).

## Operator surface (user-facing slice)

Source: [`diagrams/03-operator-surface.puml`](diagrams/03-operator-surface.puml) — C4 User-facing slice (PlantUML)

What a human touches: the Web UI as the single frontend, the API Gateway as its only backend. The UI talks to the API Gateway only — no other service. The API Gateway is a pure proxy: validate auth, route to the right backend service, return the response. There is no other path for a human to observe a task or intervene. Automation (CI, scheduler, webhook) authenticates with the API Gateway to obtain a bearer token, then submits tasks directly to the Event Router.

Three things live inside the platform: the API Gateway (auth+route proxy), the Event Router (with its database), and the Secret Service. Executors are an external fleet.

## Separation of concerns

### API Gateway

- **ONLY logic:** validate auth, route to a backend service, return the backend's response.
- **NO business logic.** No validation of payloads, no transformation of messages, no aggregation, no caching of state.
- **NO state of its own.** No database. No in-memory caches for task or routing data.
- **NO writes** (except optionally its own auth-audit log, which is a backend call).
- **Holds WS connections** to the UI for live event streaming. At WS upgrade, validates the bearer token. Then proxies WS frames between the UI and the Event Router.
- **Proxies REST calls** to backend services. The UI's HTTP requests reach the backend through GW, with auth headers validated and forwarded.

The API Gateway is essentially a TLS termination + auth + reverse proxy. It exists so the UI has a single, stable entry point and so cross-cutting auth concerns are handled in one place.

### Backend services

The data plane. All writes and process logic happen here.

#### Event Router (and its database)

- **Sole owner** of its own database. Tasks, events, routing rules, audit, **and the executor pool** (executor registry + availability) — all live here.
- **Replaces queues** entirely. The "source queue", "per-executor queues", and "event fanout queue" are not separate infrastructure; they are tables in the Event Router's database.
- **Single entry point for task creation.** Sources REST-POST directly to the Event Router (`POST /tasks`). The API Gateway is NOT on the create path. Any source (automation, CI, webhooks, future direct UI submission) goes straight to ER.
- **Owns the executor pool.** Executors register on startup (`POST /executors`) and signal availability (`POST /executors/:id/ready`). ER tracks each executor's `routing_target`, configurable `capacity N`, and `slots_in_use` counter.
- **Waits for an available slot, then dispatches.** ER matches a pending task to an executor whose `routing_target` matches AND whose `slots_in_use` is less than `capacity` (i.e., the executor has at least one free slot for a new parallel task). ER then dispatches (`POST /dispatch`) to the matched executor and increments `slots_in_use`. Each executor can run up to `N` tasks in parallel — `N` is configurable per executor at registration time.
- **Receives** live events from executors via REST (`POST /events`).
- **Receives** control events from the API Gateway (on behalf of operator intervention) via REST (`POST /control`).
- **Pushes** live events to subscribers (the API Gateway) via REST/WS.
- **Sends REST queries** to the Secret Service: `open env` to validate `secret_ids` at submit time, and to fetch env-style values for the executor at dispatch.
- **Does NOT modify message content.** Events are stored and forwarded as-is. The only fields the Event Router owns are its own internal columns (routing decisions, status, timestamps, executor registry).

#### Secret Service

- **`store` (write-only)** — accepts only `POST /secrets`. Called by the API Gateway (proxying UI requests). Returns `secret_id`.
- **`open env` (read)** — given a `scope_token`, returns env-style key=value pairs. Called by:
  - **Event Router** — at submit time, to validate `secret_ids` exist; at dispatch time, to attach env values to the dispatched task.
  - **Executor** — at task start, to inject secrets into the container env / tmpfs.
- The API Gateway no longer calls `open env` — it has no business logic to apply the result to.

#### Executor

- **Registers** with the Event Router on startup (`POST /executors { routing_target, capacity, metadata }`). The `capacity` is configurable per executor (typical values: 1 for small hosts, N for large GPU pools). ER assigns an `executor_id`.
- **May run up to `capacity` tasks in parallel.** `capacity` is the number of **concurrent containers** the executor can host. Each task runs in its own container; up to `capacity` containers can be live on the executor at once. The executor is responsible for resource isolation (container runtimes, cgroups, namespaces).
- **Signals availability** to the Event Router (`POST /executors/:id/ready`) when ready for more work — on startup, and after each task completes. The ready signal does not free the entire capacity, only one slot per task completion (one task completing frees one slot).
- **Receives** dispatched tasks from the Event Router via REST (`POST /dispatch`). Pulls the image, starts the container. Up to `capacity` containers can be live on the executor at once.
- **Calls** the Secret Service directly via `open env` at task start to mount secrets into the container.
- **Streams** live events back to the Event Router via REST as the agent runs.
- **Reports** completion to the Event Router (`POST /events { status=terminal, result }`) and signals availability again. ER decrements `slots_in_use` on its side.

### Web UI

- Thin frontend. Talks to the API Gateway only. HTTPS for REST, WSS for live events and intervention.
- Authenticates with the API Gateway to obtain a bearer token; sends the bearer token on every subsequent request.
- All secret registration goes through the API Gateway; the UI has no read access to the Secret Service (write-only at the platform boundary).
- All task and event data comes through the API Gateway, which proxies to the Event Router.

### Automation

- Authenticates with the API Gateway to obtain a bearer token.
- Submits tasks **directly** to the Event Router via REST with the bearer token (no GW on the create path).
- Receives completion callbacks directly from the Event Router.

## Task lifecycle

Source: [`diagrams/04-sequence-task-lifecycle.puml`](diagrams/04-sequence-task-lifecycle.puml) — Sequence (PlantUML)

**Executor side (steady state):** Executor registers on startup, signals ready, then waits for dispatch. Receives `POST /dispatch`, runs the task, reports completion, signals ready again. Loops.

**Task side (one task):** Source -> ER direct (no GW) to create. ER validates via SS open env, builds scope, persists. ER matches the pending task to a ready executor and dispatches. EX runs the agent loop. EX streams events to ER; ER pushes to GW; GW proxies to UI. On completion, EX reports terminal; ER updates DB and pushes final; GW proxies to UI. EX signals ready again.

**Steady state:** The Event Router is always **waiting for an available slot on an executor**. When a slot frees (a task completes, ER decrements `slots_in_use`), ER checks if any pending task matches that executor's `routing_target`. If so, ER matches and dispatches. With `capacity N` per executor, the EX can hold up to N concurrent dispatches. The match-and-dispatch loop continues as long as there are pending tasks and available slots.

The API Gateway appears in two places: (1) UI-originated auth+route proxying (e.g., secret registration, intervention, state queries), and (2) live event relay (ER -> GW -> UI over WS). It is **not** on the task create path.

## Secret injection

Source: [`diagrams/05-sequence-secrets.puml`](diagrams/05-sequence-secrets.puml) — Sequence (PlantUML)

Operator registers a secret via UI -> API Gateway -> Secret Service (write). At submit, the Event Router validates the requested `secret_ids` exist via `open env`. At task start, the Executor resolves the env via `open env` and mounts the result as container env / tmpfs. The API Gateway appears once: as a proxy between the UI and the Secret Service on the write path. It has no other role in the secret lifecycle.

## Task state machine

Source: [`diagrams/06-state-task.puml`](diagrams/06-state-task.puml) — State diagram (PlantUML)

Every task moves through this state graph. Terminal states are persisted with the full event log so a task can be replayed in the UI. The `Queued -> Preparing` transition is triggered by the Event Router dispatching via REST to the executor.

## End-to-end flows

Ten activity diagrams, one per flow, walk through every interaction in the system. Each uses PlantUML swimlanes so the actor doing each step is visible at a glance.

| Flow | Source |
|---|---|
| F1 — Operator creates a secret | [`f1-operator-creates-secret.puml`](diagrams/f1-operator-creates-secret.puml) |
| F2 — Automation submits a task | [`f2-automation-submits-task.puml`](diagrams/f2-automation-submits-task.puml) |
| F3 — Operator opens live observation | [`f3-operator-ws-subscribe.puml`](diagrams/f3-operator-ws-subscribe.puml) |
| F4 — Event Router dispatches a task | [`f4-event-router-dispatch.puml`](diagrams/f4-event-router-dispatch.puml) |
| F5 — Secret resolve at task start | [`f5-secret-resolve.puml`](diagrams/f5-secret-resolve.puml) |
| F6 — Agent loop + live event fanout | [`f6-agent-loop-fanout.puml`](diagrams/f6-agent-loop-fanout.puml) |
| F7 — Operator intervenes mid-loop | [`f7-operator-intervention.puml`](diagrams/f7-operator-intervention.puml) |
| F8 — Task finalization | [`f8-task-finalization.puml`](diagrams/f8-task-finalization.puml) |
| F9 — User cancels a task | [`f9-user-cancel.puml`](diagrams/f9-user-cancel.puml) |
| F10 — Container cleanup, scope expiry | [`f10-cleanup-scope-expires.puml`](diagrams/f10-cleanup-scope-expires.puml) |

Flow relationships:

- **F4** (executor registers + ER dispatches) is the **steady state** — it shows the lifecycle of an executor in the pool and the matching/dispatch logic. It is also where the "ER waits for avail executor" rule is implemented.
- **F1** stands alone — secret registration, pre-task.
- **F2 -> F4 -> F5 -> F6 -> F8 -> F10** is the happy path for a submitted task. (F2 creates; F4 shows the matching/dispatch; F5 is secret resolve at start; F6 is the agent loop; F8 is finalization and EX-signal-ready; F10 is cleanup.)
- **F3** can fire in parallel with F2 (operator opens UI before/during automation).
- **F7** interrupts F6 from outside.
- **F9** is a cancel that may branch through F7 (Running) or run directly (Queued / Preparing), then terminates into F10.
- **F10** is the universal cleanup, reached from F8, F7 (cancel), or F9.

## What's not in this plan (yet)

Open questions, deferred until implementation:

- **Auth mechanism** — JWT vs opaque session tokens? Where do refresh tokens live? Is there an external IdP, or does the API Gateway issue its own? Affects the "validate bearer_token" step in every flow.
- **Event Router transport** — REST only, or also gRPC / WebSocket? Affects F4, F6, F7, F8, F9.
- **Event Router subscription semantics** — long-poll, server-sent events, or WebSocket? Affects the F3 / F6 / F8 / F9 paths.
- **Specific agent framework** — OpenHands vs Claude Agent SDK vs LangGraph. Choice inside a pre-built image.
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