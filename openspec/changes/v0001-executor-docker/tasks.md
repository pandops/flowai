# Tasks

- [ ] Create ADR choosing the implementation programming language/runtime for FlowAI services before writing Docker Executor code.
- [ ] Create ADR choosing Docker client/runtime integration approach for local child-container execution.
- [ ] Implement Docker Executor process startup and configuration.
- [ ] Implement mocked task server registration, polling, and status client.
- [ ] Implement Docker container create/start/observe/cleanup flow for one task.
- [ ] Implement capacity and `running_child_count` reporting for Docker children.
- [ ] Add Docker Executor backend tests in the selected programming language.
- [ ] Add Docker Executor integration tests in the selected programming language for mocked scheduling and child-container lifecycle.
- [ ] Verify a sample task runs through a child Docker container.
- [ ] Render proposed diagrams and keep only `.puml` sources.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate v0001-executor-docker --strict --no-interactive`.
