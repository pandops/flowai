# Design: Docker Executor

## Runtime Shape

- Docker Executor starts locally and registers with a mocked task server.
- Docker Executor polls the mocked task server for task specs.
- Each task runs only as a Docker container containing the agent runtime.
- Docker Executor observes every child container it started and reports `running_child_count` to the mocked task server.

## Boundaries

- No Kubernetes support in this change.
- No State Registry or Env Registry dependency in this change.
- No Router dependency in this change; the mocked task server is temporary.

## Proposed Diagrams

- `specs/diagrams/01-docker-executor-topology.puml`
- `specs/diagrams/02-docker-task-lifecycle.puml`
