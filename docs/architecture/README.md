# FlowAI Platform — Architecture

> **Status:** Planning phase. No code written yet.
>
> **Scope:** AI platform that runs **agentic-loop** AI tasks (LLM + tools, multi-turn) inside containers — locally (Docker) or in Kubernetes.

This directory holds the architecture plan for `flowai_v2`. It is a living document.

## Layout

```
docs/architecture/
├── README.md            # this file
├── architecture.md      # high-level architecture overview with embedded diagrams
└── diagrams/            # sources (.puml / .mmd) + rendered .svg + preview.html
```

## Diagrams

Six diagrams that explain the platform. Source-of-truth files (`.puml` or `.mmd`) are checked in; the SVGs are pre-rendered and `preview.html` bundles them with detailed descriptions for batch review.

| # | Diagram | Type | Source |
|---|---|---|---|
| 01 | [System Context](diagrams/01-context.svg) | C4 — System Context | [`01-context.puml`](diagrams/01-context.puml) |
| 02 | [Task flow](diagrams/02-containers.svg) | C4 — Runtime slice | [`02-containers.puml`](diagrams/02-containers.puml) |
| 03 | [Operator surface](diagrams/03-operator-surface.svg) | C4 — User-facing slice | [`03-operator-surface.puml`](diagrams/03-operator-surface.puml) |
| 04 | [Task Lifecycle](diagrams/04-sequence-task-lifecycle.svg) | Sequence | [`04-sequence-task-lifecycle.mmd`](diagrams/04-sequence-task-lifecycle.mmd) |
| 05 | [Secret Lifecycle](diagrams/05-sequence-secrets.svg) | Sequence | [`05-sequence-secrets.mmd`](diagrams/05-sequence-secrets.mmd) |
| 06 | [Task State Machine](diagrams/06-state-task.svg) | State diagram | [`06-state-task.mmd`](diagrams/06-state-task.mmd) |

Open [`diagrams/preview.html`](diagrams/preview.html) in a browser for the full review experience with descriptions.

## Tooling

- **Diagrams 01–03 (C4)**: PlantUML with the [C4-PlantUML](https://github.com/plantuml-stdlib/C4-PlantUML) stdlib, layout engine SMETANA (no Graphviz required).
- **Diagrams 04–06 (sequence / state)**: Mermaid, rendered via `@mermaid-js/mermaid-cli`.

The `diagrams` skill (in `.opencode/skills/diagrams`) carries the full how-to-render instructions.

## Conventions

- Tech stack is **not** pinned in the diagrams or this README. It will be decided per-component at implementation time.
- Diagramming conventions and verification live in the `diagrams` skill.

## Open questions (parking lot)

Deferred — not blocking the architecture, revisit before implementation:

- Choice of specific agent framework per pre-built image.
- Whether to use Argo Workflows / Kueue on K8s, or plain Jobs.
- Storage backend for artifacts (object store / PVC / local fs).
- LLM provider abstraction (single SDK vs per-provider).
- Cost / quota enforcement (token budgets, timeouts, max concurrent tasks).
- Egress allowlist for tools (network policy in container).
