# Tasks

- [ ] Create ADR choosing Web UI framework, API Gateway implementation stack, and `tests/playwright/` as the dedicated Playwright test folder before UI implementation.
- [ ] Implement Web UI shell, task list, task detail, live event view, controls, and environment/secret configuration screens.
- [ ] Implement API Gateway no-auth proxy routes to State Registry for state reads, live events, audit, and control requests.
- [ ] Implement API Gateway no-auth proxy routes to Env Registry for environment and secret writes.
- [ ] Add API Gateway backend tests in the selected programming language.
- [ ] Add API Gateway integration tests in the selected programming language for no-auth proxy routes.
- [ ] Add Playwright API setup utilities for UI scenarios under `tests/playwright/`.
- [ ] Add browser E2E tests with Playwright under `tests/playwright/` for task history, live events, control request, and env/secret write flows.
- [ ] Verify Web UI has only one backend origin configured.
- [ ] Verify API Gateway has no Router or Executor target configured.
- [ ] Run Playwright browser QA for task history, live events, control request, and env/secret write flows in no-auth mode.
- [ ] Render proposed diagrams and keep only `.puml` sources.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate 0006-web-ui --strict --no-interactive`.
