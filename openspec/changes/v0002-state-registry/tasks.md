# Tasks

- [ ] Create ADR choosing State Registry database technology and migration approach before schema implementation.
- [ ] Implement PostgreSQL schema for task records, events, audit entries, and control requests.
- [ ] Implement event append API.
- [ ] Implement task/audit/control read APIs.
- [ ] Implement direct control-request write API for pre-Web UI operation.
- [ ] Add State Registry backend tests in the selected programming language.
- [ ] Add State Registry integration tests in the selected programming language for PostgreSQL persistence and HTTP APIs.
- [ ] Verify event appends are stored verbatim.
- [ ] Render proposed diagrams and keep only `.puml` sources.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate v0002-state-registry --strict --no-interactive`.
