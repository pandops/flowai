# FlowAI OpenSpec Project Context

FlowAI is currently a planning-phase platform for running agentic-loop AI tasks
inside isolated containers. The repository is managed by OpenSpec so architecture
and future implementation work are negotiated as specs before code is written.

## Current implementation status

- No runtime services are assumed to exist yet.
- `openspec/specs/` describes only the real accepted state for this moment.
- Because no runtime service has been accepted as implemented yet,
  `openspec/specs/` is intentionally empty except for placeholders.
- Planned behavior, topology, state ownership, APIs, diagrams, and future
  implementation work live under `openspec/changes/<change-id>/specs/` until
  the matching change is implemented, verified, accepted, and synced.

## Source of truth

| Artifact | Role |
|---|---|
| `openspec/specs/` | What the system does now: current accepted capabilities only. Empty means no accepted runtime capabilities yet. |
| `openspec/changes/*/specs/` | Proposed changes as spec deltas before acceptance. |
| `openspec/changes/*/specs/diagrams/` | Proposed `.puml` diagrams for the change. |
| `docs/adr/` | Accepted ADRs explaining why current architectural decisions were made. |
| `docs/architecture/diagrams/` | Implemented current-state `.puml` system design diagrams. |
| `openspec/changes/*/tasks.md` | Implementation steps and verification checklist for a change. |
| `AGENTS.md` | Cross-cutting topology summary and agent operating instructions. |
| `.opencode/commands/` and `.opencode/skills/` | OpenSpec workflow commands generated for OpenCode. |

If artifacts disagree, resolve them in this order:

1. Active OpenSpec change specs for planned behavior, or baseline specs for accepted real behavior.
2. Root `AGENTS.md` for cross-cutting topology and state ownership.
3. Proposed change diagrams or current-state architecture diagrams as derived documentation.

## Target service catalog

The planned service catalog is indexed in root `AGENTS.md` and defined by active
change specs under `openspec/changes/<change-id>/specs/`. Do not duplicate
per-service responsibilities in this file; update the relevant change spec and
root cross-cutting matrix instead.

Each service remains planned until its change is implemented, verified, accepted,
and synced into `openspec/specs/` as real state.

## OpenSpec workflow rule

Any change that adds, removes, or modifies architecture, service behavior,
service topology, state ownership, APIs, diagrams, or future implementation code
MUST start as an OpenSpec change under `openspec/changes/<change-id>/` unless it
is a typo-only correction that does not alter behavior.

Every change should include:

- `proposal.md` with why, what changes, impact, and non-goals.
- `tasks.md` with concrete implementation and verification checklist items.
- Delta specs under `specs/<capability>/spec.md` using requirements and
  GIVEN/WHEN/THEN scenarios.
- Proposed diagrams under `specs/diagrams/*.puml` when topology or flow changes
  need a visual design.
- `design.md` when the change affects cross-service topology, state ownership,
  data model, security boundaries, or implementation strategy.

## Verification and ADR task rules

- The first implementation change MUST start with choosing and recording the
  programming language/runtime as an ADR task.
- Any change that requires choosing a database, framework, runtime, auth scheme,
  deployment substrate, SDK, or other implementation technology MUST start its
  `tasks.md` with ADR creation tasks for those choices.
- Backend and integration tests MUST be written in the selected implementation
  programming language for that service.
- Browser E2E tests and API setup/preparation for UI scenarios MUST be written
  with Playwright.
- Playwright tests MUST live in a separate folder dedicated to Playwright tests,
  currently `tests/playwright/` unless a later ADR changes the test layout.

Validate before implementation:

```bash
npx -y @fission-ai/openspec@1.5.0 validate <change-id>
```

Validate the full baseline before reporting completion:

```bash
npx -y @fission-ai/openspec@1.5.0 validate --all
```
