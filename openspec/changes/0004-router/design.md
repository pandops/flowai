# Design: Router

## Runtime Shape

- Automation publishes tasks to Router.
- Router stores incoming queue, processing list, Executor wait list, Executor registry, slots, running child count, and in-flight assignments.
- Docker Executor registers with Router, reports readiness/running-child count, pulls work, and posts status.
- Router does not call API Gateway, State Registry, or Env Registry.

## Migration from Mocked Task Server

The mocked task server from `0001-executor-docker` is removed from the Docker Executor path. Executor scheduling calls are redirected to Router surfaces with equivalent registration, polling, and status semantics plus durable Router persistence.

## Proposed Diagrams

- `specs/diagrams/01-router-topology.puml`
- `specs/diagrams/02-router-dispatch-sequence.puml`
- `specs/diagrams/03-router-task-state.puml`
- `specs/diagrams/04-mock-server-replacement.puml`
