# OpenSpec Agent Instructions — FlowAI

This repository is OpenSpec-managed. Treat the files under `openspec/` as the
governing contract for architecture and future implementation work.

## Real-state vs planned-state rule

The specs in `openspec/specs/` describe only the current accepted real state.
Planned architecture belongs under `openspec/changes/<change-id>/specs/` until a
completed change adds code, tests, and verification evidence and is accepted into
the baseline. Do not claim a service is approved or implemented unless that
evidence has been added by a completed OpenSpec change and synced into
`openspec/specs/`.

## Required workflow

1. Read `openspec/project.md`, root `AGENTS.md`, and the relevant specs before
   proposing or implementing a change.
2. For any behavior, topology, state ownership, API, security, diagram, or code
   change, create or update `openspec/changes/<change-id>/` first.
3. Validate the change before code or diagram edits:
   ```bash
   npx -y @fission-ai/openspec@1.5.0 validate <change-id>
   ```
4. Implement task-by-task from `tasks.md`; mark tasks complete only after their
   verification has actually passed.
5. Validate all specs before completion:
   ```bash
   npx -y @fission-ai/openspec@1.5.0 validate --all
   ```
6. Archive or sync only after implementation and verification are complete.

## Artifact rules

- Do not edit `openspec/specs/` directly for behavior changes unless syncing an
  accepted change into the baseline.
- Keep proposed `.puml` diagrams under `openspec/changes/<change-id>/specs/diagrams/`.
- Keep accepted current-state `.puml` diagrams under `docs/architecture/diagrams/`.
- Keep accepted ADRs under `docs/adr/`; planned ADR creation belongs in a change
  until accepted.
- Keep requirements observable: use SHALL/MUST language plus scenarios.
- Encode forbidden topology as explicit negative requirements.
- Keep diagrams derived from specs and root `AGENTS.md`; when diagrams disagree,
  update OpenSpec first and redraw second.
- In `tasks.md`, put ADR creation tasks first for changes that choose a database,
  framework, runtime, auth scheme, deployment substrate, SDK, or other technology.
- Write backend and integration tests in the selected implementation language.
- Write browser E2E tests and API setup for UI scenarios in Playwright, under a
  separate Playwright test folder (`tests/playwright/` unless superseded by ADR).
- Keep `.opencode/commands/` and `.opencode/skills/` tracked when they are the
  generated OpenSpec workflow surface for this repo.

## Status language

Use precise status labels:

- **Real state**: accepted behavior in `openspec/specs/` with implementation and
  verification evidence.
- **Planned**: behavior under `openspec/changes/<change-id>/`.
- **Implemented**: behavior with code/tests/manual verification added by a
  completed change.
