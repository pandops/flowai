# FlowAI Platform — Architecture

> **Status:** Planning phase. No code written yet.
>
> **Scope:** AI platform that runs **agentic-loop** AI tasks (LLM + tools, multi-turn) inside containers — locally (Docker) or in Kubernetes.
>
> **Source of truth:** Accepted real state lives in [`openspec/specs/`](../../openspec/specs/). Planned architecture lives in active numbered changes under [`openspec/changes/`](../../openspec/changes/) (`v0001-executor-docker` through `v0007-auth`). Proposed diagrams live under `openspec/changes/*/specs/diagrams/`. This directory documents implemented current-state design only.

This directory holds implemented current-state architecture documentation for
`flowai_v2`. Because no runtime services are implemented yet, the diagram set is
currently empty.

## Conventions

### Diagrams are `.puml` only — no rendered images in the repo

The `diagrams/` directory contains **only implemented current-state `.puml` source files**. We do **not** commit `.svg`, `.png`, or `.html` rendered bundles. Rationale:

- The `.puml` source is the single source of truth. Rendered images go stale the moment a diagram is edited.
- Pre-rendering adds noise to diffs (large binary blobs) and to `git log`.
- Anyone who needs a rendered view can produce one locally with `plantuml` (see Tooling below) — fast (sub-second per diagram) and deterministic.
- Proposed diagrams belong in `openspec/changes/*/specs/diagrams/` until accepted.

To preview a diagram locally:

```bash
plantuml -tsvg docs/architecture/diagrams/<accepted-current-diagram>.puml
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

That command runs OpenSpec strict validation, `markdownlint`, and a PlantUML syntax check on every current-state `.puml` file (compile-only, no image output). Proposed diagrams are validated with their owning OpenSpec change.

## Diagrams

No current-state diagrams exist yet. Proposed target diagrams currently live in
the numbered change directories under `openspec/changes/*/specs/diagrams/`.

## Tooling

- **All diagrams**: PlantUML, rendered via the `plantuml` CLI. C4 diagrams (01–03) use the [C4-PlantUML](https://github.com/plantuml-stdlib/C4-PlantUML) stdlib (`!include <c4/C4_Container>` URL form); sequence (04–05), state (06), and activity (F1–F10) use native PlantUML syntax. Layout engine SMETANA (no Graphviz required).

Use the PlantUML commands above as the canonical render path. If a future
diagram-specific skill is added, keep it aligned with this README and the
OpenSpec-derived diagram sources.

## Implementation conventions

- Tech stack decisions are accepted in `docs/adr/` only after an ADR change is accepted.
- Diagramming conventions and verification live in this README and the `.puml`
  sources until a diagram-specific skill is added.
- All architecture changes go through OpenSpec first; mirror to this folder only when the rendered view needs updating.

## Open questions (parking lot)

Deferred — not blocking the architecture, revisit before implementation:

- Choice of specific agent framework per pre-built image.
- Storage backend for artifacts (object store / PVC / local fs).
- LLM provider abstraction (single SDK vs per-provider).
- Cost / quota enforcement (token budgets, timeouts, max concurrent tasks).
- Egress allowlist for tools (network policy in container).
- Whether live task observation ever needs a gateway-mediated stream from the
  agent runtime container; the current target architecture keeps the Web UI
  connected only to the API Gateway.
