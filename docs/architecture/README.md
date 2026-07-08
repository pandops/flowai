# FlowAI Platform — Architecture

> **Status:** Planning phase. No code written yet.
>
> **Scope:** AI platform that runs **agentic-loop** AI tasks (LLM + tools, multi-turn) inside containers — locally (Docker) or in Kubernetes.
>
> **Source of truth:** The source of truth for architecture is [`openspec/specs/`](../../openspec/specs/) (per-service specs: `web-ui`, `api-gateway`, `event-router`, `state-store`, `secret-service`, `executor`). This README and `architecture.md` are kept as **legacy navigation and reference documentation** for browsing the diagram sources and prose. Edit the OpenSpec specs first; mirror changes here only when the source files need updating.

This directory holds the architecture plan for `flowai_v2`. It is a living document.

## Conventions

### Diagrams are `.puml` only — no rendered images in the repo

The `diagrams/` directory contains **only `.puml` source files**. We do **not** commit `.svg`, `.png`, or `.html` rendered bundles. Rationale:

- The `.puml` source is the single source of truth. Rendered images go stale the moment a diagram is edited.
- Pre-rendering adds noise to diffs (large binary blobs) and to `git log`.
- Anyone who needs a rendered view can produce one locally with `plantuml` (see Tooling below) — fast (sub-second per diagram) and deterministic.
- This matches the broader "OpenSpec is canonical, this folder is reference" stance above.

To preview a diagram locally:

```bash
plantuml -tsvg docs/architecture/diagrams/04-sequence-task-lifecycle.puml
# open the resulting .svg in a browser
```

Batch-render every diagram (no files are written to the repo):

```bash
for f in docs/architecture/diagrams/*.puml; do plantuml -tsvg "$f"; done
```

### Layout

```
docs/architecture/
├── README.md            # this file
├── architecture.md      # high-level architecture overview with linked diagram sources
└── diagrams/            # .puml sources only — no rendered artifacts
```

## Validation

Run local CI from the repository root before any commit that touches architecture files:

```
pnpm run ci:check
```

That command runs OpenSpec strict validation, `markdownlint`, and a PlantUML syntax check on every `.puml` file (compile-only, no image output). GitLab CI runs the same script via the `ci_check` job on every push (proxied through Gitea).

## Diagrams

Six architectural diagrams that explain the platform shape, plus ten activity diagrams that walk through every end-to-end flow. Each row links to its `.puml` source — render locally with `plantuml` if you want a picture.

### Architectural views

| # | Type | Source |
|---|---|---|
| 01 | C4 — System Context | [`01-context.puml`](diagrams/01-context.puml) |
| 02 | C4 — Runtime slice | [`02-containers.puml`](diagrams/02-containers.puml) |
| 03 | C4 — User-facing slice | [`03-operator-surface.puml`](diagrams/03-operator-surface.puml) |
| 04 | Sequence — Task lifecycle | [`04-sequence-task-lifecycle.puml`](diagrams/04-sequence-task-lifecycle.puml) |
| 05 | Sequence — Secret lifecycle | [`05-sequence-secrets.puml`](diagrams/05-sequence-secrets.puml) |
| 06 | State — Task state machine | [`06-state-task.puml`](diagrams/06-state-task.puml) |

### Per-flow activity diagrams

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

## Tooling

- **All diagrams**: PlantUML, rendered via the `plantuml` CLI. C4 diagrams (01–03) use the [C4-PlantUML](https://github.com/plantuml-stdlib/C4-PlantUML) stdlib (`!include <c4/C4_Container>` URL form); sequence (04–05), state (06), and activity (F1–F10) use native PlantUML syntax. Layout engine SMETANA (no Graphviz required).

The `diagrams` skill (in `.opencode/skills/diagrams`) carries the full how-to-render instructions.

## Implementation conventions

- Tech stack is **not** pinned in the diagrams or this README. It will be decided per-component at implementation time.
- Diagramming conventions and verification live in the `diagrams` skill.
- All architecture changes go through OpenSpec first; mirror to this folder only when the rendered view needs updating.

## Open questions (parking lot)

Deferred — not blocking the architecture, revisit before implementation:

- Choice of specific agent framework per pre-built image.
- Whether to use Argo Workflows / Kueue on K8s, or plain Jobs.
- Storage backend for artifacts (object store / PVC / local fs).
- LLM provider abstraction (single SDK vs per-provider).
- Cost / quota enforcement (token budgets, timeouts, max concurrent tasks).
- Egress allowlist for tools (network policy in container).
