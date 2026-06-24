# FlowAI Platform — Architecture

> **Status:** Planning phase. No code written yet.
>
> **One-paragraph summary:** FlowAI is a platform that runs **agentic AI tasks** (LLM + tools, multi-turn) inside containers. A control plane schedules work; an executor daemon starts containers locally or in Kubernetes behind a single interface; a web UI provides a dashboard with live observation and intervention. Containers run pre-built images of external agent frameworks (OpenHands, Claude Agent SDK, LangGraph, ...) over a defined contract. A state store holds tasks, projects, and audit log; a task queue handles pub/sub and rate limits; a separate Secrets Registry service holds encrypted secrets per user / per project and issues short-lived grant tokens to running containers. OpenTelemetry traces every hop. Deployment is K8s-native via Helm; dev/test runs on k3d.

## System context

![C4 Context](diagrams/01-context.svg)

Source: [`diagrams/01-context.puml`](diagrams/01-context.puml) (PlantUML)

The platform sits between the user and the agentic-loop world. It owns scheduling, isolation, secrets, observability, and cost — and delegates the actual LLM/tool loop to a framework inside a container.

## Task flow (runtime slice)

![Task flow](diagrams/02-containers.svg)

Source: [`diagrams/02-containers.puml`](diagrams/02-containers.puml) (PlantUML)

Five services that a task touches as it goes from "automation submits" to "task done", plus the per-task agent container. No human in this slice.

## Operator surface (user-facing slice)

![Operator surface](diagrams/03-operator-surface.svg)

Source: [`diagrams/03-operator-surface.puml`](diagrams/03-operator-surface.puml) (PlantUML)

What a human touches: Web UI as the single frontend, Control Plane API as its backend, Secret Service for CRUD. The UI is the only CRUD client of the Secret Service. The Control Plane is the only backend for the UI. There is no other path for a human to manage secrets, observe a task, or intervene.

Five things live inside the platform: UI, control plane API, scheduler, executor, and the data tier (Postgres + Redis). The agent container is per-task and ephemeral.

## Task lifecycle

```mermaid
%% 04 — Task lifecycle sequence
sequenceDiagram
    autonumber
    participant U as User (UI/API)
    participant API as Control Plane API
    participant DB as Postgres
    participant Q as Redis Queue
    participant SCH as Scheduler
    participant EX as Executor Daemon
    participant CR as Container Runtime
    participant AG as Agent Container
    participant LLM as LLM Provider

    U->>API: POST /tasks { definition, hints }
    API->>API: Validate, resolve secrets refs
    API->>DB: INSERT task (status=queued)
    API->>Q: LPUSH task_id
    API-->>U: 202 task_id, run_id

    SCH->>Q: BLPOP task_id
    SCH->>DB: UPDATE status=running
    SCH->>EX: Submit(task_spec)
    EX->>CR: Pull image, start container
    EX->>AG: Inject env (secrets, tool defs)
    EX-->>SCH: run_handle

    loop agent loop
        AG->>LLM: POST prompt + history
        LLM-->>AG: completion (text or tool_use)
        AG-->>EX: event stream
        EX-->>API: stream events
        API->>DB: persist events
        API-->>U: WS push (live)
    end

    AG->>EX: final result
    EX->>API: task finished (status)
    API->>DB: UPDATE status=succeeded, save artifacts
    API-->>U: WS final
    Note over AG,CR: Container terminated. Artifacts in storage.
```

Source: [`diagrams/04-sequence-task-lifecycle.mmd`](diagrams/04-sequence-task-lifecycle.mmd)

Submit -> validate -> enqueue -> pick -> start -> agent loop (LLM + tools) -> finalize -> cleanup. Every hop is traced.

## Secret injection

```mermaid
%% 05 — Secret injection sequence
sequenceDiagram
    autonumber
    participant U as User
    participant API as Control Plane API
    participant V as Encrypted Vault
    participant EX as Executor Daemon
    participant AG as Agent Container

    Note over U,AG: Submit-time secret binding (never stored in cleartext in container env)

    U->>API: POST /tasks { tools: [github], secret_refs: [github_token] }
    API->>V: Fetch encrypted github_token
    V-->>API: ciphertext blob
    API->>API: Decrypt in-memory, build ephemeral side-channel plan

    API->>EX: Submit(task_spec) with encrypted_payload_ref
    EX->>EX: Pull pre-built agent image
    EX->>AG: Mount tmpfs with decrypted secret, mode 0400
    EX->>AG: ENV SECRET_REFS=github_token:/run/secrets/github_token
    AG->>AG: SDK reads at /run/secrets/github_token
    AG->>AG: Tool (github) reads file, makes API call

    Note over AG: After task ends. Container destroyed, tmpfs unmounted, decrypted bytes vanish.

    Note over API,V: At rest. ciphertext only. Decryption key from KMS or platform root key (out of band).
```

Source: [`diagrams/05-sequence-secrets.mmd`](diagrams/05-sequence-secrets.mmd)

Secrets are ciphertext in Postgres, plaintext only inside a tmpfs mounted into the running container, and gone the moment the container stops.

## Task state machine

```mermaid
%% 06 — Task state machine
stateDiagram-v2
    [*] --> Queued: POST /tasks
    Queued --> Scheduled: scheduler picks
    Queued --> Cancelled: user cancel
    Scheduled --> Pulling: executor.Submit
    Pulling --> Starting: image ready
    Pulling --> Failed: pull error
    Starting --> Running: agent loop begins
    Starting --> Failed: start error
    Running --> Running: tool_use / llm_response / file_change
    Running --> Succeeded: agent returns final
    Running --> Failed: unhandled error / max_steps
    Running --> Cancelled: user cancel (graceful)
    Running --> Timeout: wall_clock > limit
    Failed --> [*]
    Succeeded --> [*]
    Cancelled --> [*]
    Timeout --> [*]
    note right of Running: events streamed to UI. persisted to DB
```

Source: [`diagrams/06-state-task.mmd`](diagrams/06-state-task.mmd)

Every task moves through this state graph. Terminal states are persisted with the full event log so a task can be replayed in the UI.

## What's not in this plan (yet)

Open questions, deferred until implementation:

- **Specific agent framework** — OpenHands vs Claude Agent SDK vs LangGraph. Choice inside a pre-built image.
- **Argo Workflows / Kueue** — useful for fan-out and queuing inside K8s.
- **Artifact storage** — object store / PVC / local fs.
- **Cost / quota enforcement** — token budgets, timeouts, max concurrent tasks.
- **Network policy / egress allowlist** — what can the agent container reach?
- **Tool registry** — which tools are first-class, how they are versioned, how users add their own.
- **Specific tech stack per component** — decided per component at implementation time.

## How to read this document

1. Skim the system context (diagram 01) and the containers (diagram 02). That's the shape.
2. Read the lifecycle (diagram 04) — that is the happy path.
3. To change something, edit the relevant diagram source (`.puml` / `.mmd`) and re-render.
4. The `diagrams` skill (in `.opencode/skills/diagrams`) explains how to render and verify.
