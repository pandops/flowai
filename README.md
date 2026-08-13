# FlowAI Platform

FlowAI currently contains two implemented Go services:

- [State Registry](svc/state-registry/README.md) — durable PostgreSQL-backed task,
  assignment, event, environment, secret, control, audit, and admin state.
- [Docker OpenHands Executor](executor/docker_openhands/README.md) — a bounded
  worker that claims State Registry tasks and runs OpenHands in Docker.

The services communicate only through the State Registry contract. There is no
mocked task server, Router, or separate Env Registry.

## Verification

```bash
env -u GOROOT go test ./...
cd qa-e2e/state-registry && npm test
```

Playwright reports and traces are written under `qa-e2e/reports/`; see
[qa-e2e/README.md](qa-e2e/README.md). Current normative behavior lives in
`openspec/specs/`, accepted decisions in `docs/adr/`, and current diagrams
in `docs/architecture/diagrams/`.
