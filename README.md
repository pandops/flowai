# FlowAI Platform

FlowAI currently contains two implemented Go services:

- [State Registry](state-registry/README.md) — durable PostgreSQL-backed task,
  assignment, event, environment, secret, control, audit, and admin state.
- [Docker OpenHands Executor](executor/docker_openhands/README.md) — a bounded
  worker that claims State Registry tasks and runs OpenHands in Docker.

The services communicate only through the State Registry contract. There is no
mocked task server, Router, or separate Env Registry.

## Verification

```bash
env -u GOROOT go test ./...
cd autotest/state-registry && npm test
```

Playwright reports and traces are written under `autotest/reports/`; see
[autotest/README.md](autotest/README.md). Current normative behavior lives in
`openspec/specs/`, accepted decisions in `docs/adr/`, and current diagrams
in `docs/architecture/diagrams/`.
