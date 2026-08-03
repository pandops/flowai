# Tasks

## 1. Implementation-start artifact transition

- [ ] Move every `specs/test-cases/v0006.*.md` definition unchanged to `autotest/test-cases/` before writing any runnable E2E test or production code; verify the source directory contains no `.md` files and destinations do not overwrite existing IDs.
- [ ] Bootstrap or extend the repository E2E harness only under `autotest/` (including Playwright configuration, service fixtures, browser/network capture, State Registry spy fixtures, and a fixture for the configured bootstrap `operator_id`); verify the harness can run a known smoke test before any v0006 RED run.

## 2. Configured bootstrap operator + team Web UI slice (`v0006.1`-`v0006.5`)

- [ ] **RED:** Generate runnable Playwright tests under `autotest/web-ui/v0006-configured-team.spec.ts` from test cases `v0006.1` through `v0006.5`, then run `npx playwright test autotest/web-ui/v0006-configured-team.spec.ts`; verify the tests fail for the behavior-specific reasons that the configured-team label, absence of team/admin registration and projection controls, Gateway-only browser topology with no browser-asserted `X-FlowAI-Operator-ID`, configured-team action scope, absence of browser identity headers (including Operator-ID), refresh reconstruction, or absence of operator selection/assertion is not yet implemented—not for fixture, syntax, or dependency failure.
- [ ] **GREEN:** Implement the minimum Web UI shell, task/detail/live-event/control/environment/secret flows, single API Gateway base URL, configured `team_name` display, and absence of team selector, membership, team/source-system/task-type registration, global admin projections, and operator-selection behavior; rerun `npx playwright test autotest/web-ui/v0006-configured-team.spec.ts` and verify all five cases pass.
- [ ] **REFACTOR:** Remove duplication and improve presentation structure without adding team management, alternate backend origins, or operator selection/assertion; rerun `npx playwright test autotest/web-ui/v0006-configured-team.spec.ts` and the related `autotest/web-ui/` suite and keep both green.
- [ ] Fill each moved `v0006.1`-`v0006.5` definition's `## Implementation reference` with `autotest/web-ui/v0006-configured-team.spec.ts` and the exact Playwright test title; record the RED failure and GREEN pass commands/results.

## 3. Configured bootstrap operator + team REST Gateway slice (`v0006.6`-`v0006.9`, `v0006.11`)

- [ ] **RED:** Generate runnable E2E tests under `autotest/api-gateway/v0006-configured-team-rest.spec.ts` for invalid/ambiguous operator-or-team configuration, State-Registry-only routing with every `/admin/*` route denied before child-request creation, spoofed-header replacement including configured bootstrap `operator_id` injection, stable-`team_id` ownership with operator attribution, and unchanged responses; run `npx playwright test autotest/api-gateway/v0006-configured-team-rest.spec.ts` and verify behavior-specific failures before Gateway production code is written.
- [ ] **GREEN:** Implement the minimum no-auth bootstrap configuration validation that rejects missing/empty/ambiguous `operator_id` or `team_id`, accepts an omitted `team_name`, and rejects a supplied empty/invalid `team_name`; State Registry operator-route allowlist excluding `/admin/*`; client internal-header removal (including `X-FlowAI-Operator-ID`); configured bootstrap `X-FlowAI-Operator-ID`, configured `X-FlowAI-Team-ID`, optional display-only `X-FlowAI-Team-Name` injection only when present; Gateway-generated `X-FlowAI-Request-ID`; and transparent response forwarding; rerun the exact RED command and verify all cases pass.
- [ ] **REFACTOR:** Isolate transport security-context enrichment from proxy response handling while keeping State Registry authoritative for resource ownership; rerun `npx playwright test autotest/api-gateway/v0006-configured-team-rest.spec.ts` and the related `autotest/api-gateway/` suite and keep both green.
- [ ] Fill moved definitions `v0006.6`-`v0006.9` and `v0006.11` with exact implementation references and recorded RED/GREEN evidence.

## 4. Configured bootstrap operator + team WebSocket slice (`v0006.10`)

- [ ] **RED:** Generate the runnable E2E WebSocket test under `autotest/api-gateway/v0006-configured-team-websocket.spec.ts`, run `npx playwright test autotest/api-gateway/v0006-configured-team-websocket.spec.ts`, and verify it fails specifically because forged context (including `X-FlowAI-Operator-ID=attacker` and `X-FlowAI-Team-ID=team-beta`) is not yet replaced or `team-beta` events are still observable.
- [ ] **GREEN:** Implement the minimum WebSocket upgrade/subscription path that validates configured bootstrap `operator_id` + configured team context, strips browser identity headers (including `X-FlowAI-Operator-ID`), injects configured bootstrap `operator_id`, configured `team_id`, optional display-only `team_name` only when present, and Gateway-generated `request_id`, and scopes the State Registry child subscription to `team_id`; rerun the exact RED command and verify only `team-alpha` events pass through unchanged.
- [ ] **REFACTOR:** Share safe context-building primitives between REST and WebSocket paths without changing their external behavior; rerun both v0006 Gateway E2E files and the related Gateway suite and keep them green.
- [ ] Fill moved definition `v0006.10-websocket-bootstrap-operator-and-team-isolation.md` with the exact implementation reference and recorded RED/GREEN evidence.

## 5. Completion gates

- [ ] Run the complete repository E2E suite rooted at `autotest/`; verify configured bootstrap operator + configured-team REST, WebSocket, UI, spoof-resistance, audit-attribution, and cross-team isolation cases pass with no skipped tests.
- [ ] Verify API Gateway has no Executor target and no `/admin/*` operator route; Web UI has no non-Gateway backend origin, team/operator selector, team/source-system/task-type registration, or global admin projection; `team_name` is never used for authorization, the configured bootstrap `operator_id` is never used as an authentication proof, and all five sequence diagrams remain sequence-family `.puml` sources.
- [ ] Run the repository's normal build and related unit/integration suites as supplementary checks; these do not replace the required E2E RED/GREEN evidence.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate v0006-web-ui --strict --no-interactive` after implementation artifacts and task evidence are complete.
