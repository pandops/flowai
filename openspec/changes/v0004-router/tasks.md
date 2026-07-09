# Tasks

- [ ] Create ADR choosing Router database technology, migration approach, and queue persistence model before schema implementation.
- [ ] Implement Router PostgreSQL schema for queue, processing list, wait list, Executor registry, slots, running child count, and assignments.
- [ ] Implement Automation task submission API.
- [ ] Implement Docker Executor registration, ready, poll, status, and running-child-count APIs.
- [ ] Update Docker Executor scheduling client from mocked task server to Router.
- [ ] Add Router backend tests in the selected programming language.
- [ ] Add Router integration tests in the selected programming language for PostgreSQL recovery, Docker Executor registration, dispatch, and status APIs.
- [ ] Verify capacity gating by both slots in use and latest running child count.
- [ ] Verify Router restart recovers durable queue and Executor-pool state.
- [ ] Verify Router has no outbound connection to API Gateway, State Registry, or Env Registry.
- [ ] Render proposed diagrams and keep only `.puml` sources.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate v0004-router --strict --no-interactive`.
