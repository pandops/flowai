# Tasks

- [ ] Create ADR choosing auth scheme, token format, credential validation approach, and auth-audit storage before auth implementation.
- [ ] Implement API Gateway login/token issuance and refresh route.
- [ ] Implement bearer-token validation for REST requests.
- [ ] Implement bearer-token validation for WebSocket upgrades.
- [ ] Update Web UI to authenticate and attach bearer tokens.
- [ ] Add API Gateway auth backend tests in the selected programming language.
- [ ] Add API Gateway auth integration tests in the selected programming language for login, refresh, REST token validation, and WebSocket token validation.
- [ ] Add Playwright API setup utilities for authenticated UI scenarios under `tests/playwright/`.
- [ ] Add browser E2E auth tests with Playwright under `tests/playwright/` for login, authenticated task history, live events, control request, and env/secret write flows.
- [ ] Verify missing/invalid tokens are rejected before proxying.
- [ ] Verify authenticated task history, live events, control request, and env/secret write flows still work.
- [ ] Render proposed diagrams and keep only `.puml` sources.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate v0007-auth --strict --no-interactive`.
