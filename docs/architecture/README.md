# FlowAI Platform — Architecture

The [current overview](overview.md) covers State Registry, both OpenHands
Executors, API Gateway, and Web UI. The [documentation index](../README.md)
links the [planned changes](../README.md#planned-changes) and
[archived changes](../README.md#archived-changes).

## Sources of truth

- [Current OpenSpec contracts](../../openspec/specs/) describe the accepted baseline.
- [Accepted ADRs](../adr/README.md) record architectural decisions.
- Active changes contain proposed behavior, decisions, and diagrams until acceptance and sync.
- Archived changes preserve delivered and superseded designs; consult their status before treating them as current.

## Diagrams

[diagrams/](diagrams/) contains checked-in PlantUML source. Keep rendered SVG,
PNG, and HTML previews local and Git-ignored. Proposed diagrams belong under
`openspec/changes/<change-id>/specs/diagrams/` until accepted.

Some retained Web UI diagrams have `no-auth` in their names and describe the
historical bootstrap flow. The current authentication contract is in
[auth](../../openspec/specs/auth/spec.md), with
[login](diagrams/auth-login-sequence.puml),
[authenticated requests](diagrams/authenticated-request-sequence.puml), and
[authenticated WebSockets](diagrams/authenticated-websocket-sequence.puml)
as the corresponding authentication diagrams.

Render a preview beside its source from the repository root:

```bash
plantuml -tsvg docs/architecture/diagrams/auth-login-sequence.puml
```

This writes a local SVG; do not commit generated output. For syntax validation
without rendering:

```bash
plantuml -checkonly docs/architecture/diagrams/*.puml
```

## Validation

From the repository root:

```bash
npx -y @fission-ai/openspec@1.5.0 validate --all --strict
npx markdownlint-cli2 README.md CONTRIBUTING.md 'docs/**/*.md'
```

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for staged-file formatting,
verification, and the OpenSpec workflow. Documentation corrections should describe
existing behavior; proposed architecture stays in its owning change until accepted.
