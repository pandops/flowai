# Tasks

## 1. Implementation-start artifact transition

- [ ] Move every `specs/test-cases/v0007.*.md` definition unchanged to `qa-e2e/test-cases/` before writing any runnable E2E test or production code; verify the source directory contains no `.md` files and destination IDs are not overwritten.
- [ ] Extend the repository E2E harness only under `qa-e2e/` with Keycloak OIDC/group fixtures, token issuance helpers, State Registry group-resolution/header/request spies, audit fixtures, and WebSocket event publishers; verify a harness smoke test passes before the first v0007 RED run.

## 2. Keycloak group and State Registry team-resolution slice (`v0007.16`-`v0007.19`)

- [ ] **RED:** Generate runnable API E2E tests under `qa-e2e/api-gateway/v0007-keycloak-team-resolution.spec.ts` from `v0007.16` through `v0007.19`; run the exact Playwright file and verify behavior-specific failures for forged group acceptance, absent Gateway-only resolution, missing unique group mapping, or unresolved team selection.
- [ ] **GREEN:** Implement Keycloak OIDC authorization-code flow with PKCE, canonical group extraction, immutable unique `keycloak_group_id` team registration, trusted Gateway-only group-to-team resolution, deterministic `{team_id, team_name}` results, Web UI team-list delivery, and resolved-team-only working-token issuance; rerun the exact RED command and verify all cases pass.
- [ ] **REFACTOR:** Separate Keycloak protocol handling, Gateway resolution client, and State Registry mapping storage without accepting browser group authority; rerun the targeted file and related State Registry admin tests and keep both green.
- [ ] Fill moved definitions `v0007.16`-`v0007.19` with exact runnable references and RED/GREEN evidence.

## 3. Canonical working-token and immutable-team slice (`v0007.1`-`v0007.4`)

- [ ] **RED:** Generate runnable API E2E tests under `qa-e2e/api-gateway/v0007-auth-token.spec.ts` from `v0007.1` through `v0007.4`; run `npx playwright test qa-e2e/api-gateway/v0007-auth-token.spec.ts` and verify behavior-specific failures for missing canonical claims, ambiguous/stale team acceptance, team switching on refresh, or auth-state leakage—not fixture, syntax, or dependency failures.
- [ ] **GREEN:** Implement the minimum working-token issuance and refresh after Keycloak group resolution, canonical `operator_id`, exactly one immutable selected `team_id`, canonical display-only `team_name`, audience/expiry claims, fresh group-to-team access check, refresh immutability, Gateway-local session state, and separate auth audit; rerun the exact RED command and verify all token/session cases pass.
- [ ] **REFACTOR:** Separate canonical identity resolution, token verification, and refresh invariants while preserving fail-closed behavior; rerun `npx playwright test qa-e2e/api-gateway/v0007-auth-token.spec.ts` and the related auth suite and keep both green.
- [ ] Fill moved definitions `v0007.1`-`v0007.4` with `qa-e2e/api-gateway/v0007-auth-token.spec.ts` and exact Playwright test titles; record RED failures and GREEN passes.

## 4. Authenticated resolved-team Web UI slice (`v0007.5`-`v0007.7`)

- [ ] **RED:** Generate browser E2E tests under `qa-e2e/web-ui/v0007-authenticated-team.spec.ts`; run `npx playwright test qa-e2e/web-ui/v0007-authenticated-team.spec.ts` and verify behavior-specific failures for authenticated team display, absence of membership/team-selection UI, Gateway-only bearer use, or browser-originated internal headers.
- [ ] **GREEN:** Implement the minimum Web UI login/session flow, resolved `{team_id, team_name}` list rendering, resolved-team-only selection, one-team bearer attachment only to API Gateway REST/WebSocket traffic, and absence of team CRUD/membership behavior; rerun the exact RED command and verify `v0007.5`-`v0007.7` pass.
- [ ] **REFACTOR:** Consolidate authenticated client/session handling without adding another backend origin or authorization logic based on `team_name`; rerun the targeted file and related `qa-e2e/web-ui/` suite and keep both green.
- [ ] Fill moved definitions `v0007.5`-`v0007.7` with exact implementation references and RED/GREEN evidence.

## 5. Authenticated REST context and isolation slice (`v0007.8`-`v0007.14`)

- [ ] **RED:** Generate API E2E tests under `qa-e2e/api-gateway/v0007-authenticated-rest.spec.ts` for no-auth rejection, spoofed-header replacement, REST team isolation, unchanged responses, missing/ambiguous/stale team rejection, browser bearer containment, and canonical audit attribution; run `npx playwright test qa-e2e/api-gateway/v0007-authenticated-rest.spec.ts` and verify each case fails for its absent security behavior before REST proxy production edits.
- [ ] **GREEN:** Implement the minimum REST auth middleware and proxy path that validates signature/issuer/audience/expiry and canonical team freshness, rejects unusable context before proxying, removes browser `Authorization` and all internal identity headers, injects canonical `X-FlowAI-Operator-ID`, stable `X-FlowAI-Team-ID`, optional display-only `X-FlowAI-Team-Name`, and Gateway-generated `X-FlowAI-Request-ID`, and leaves resource ownership/audit persistence to State Registry; rerun the exact RED command and verify all cases pass.
- [ ] **REFACTOR:** Centralize safe context construction and credential scrubbing while keeping response status, headers, and body unchanged and avoiding Gateway business policy; rerun the targeted file and related `qa-e2e/api-gateway/` REST suite and keep both green.
- [ ] Fill moved definitions `v0007.8`-`v0007.14` with exact implementation references and RED/GREEN evidence.

## 6. Authenticated WebSocket isolation slice (`v0007.15` plus WebSocket variants from `v0007.2`, `v0007.8`, `v0007.11`-`v0007.13`)

- [ ] **RED:** Generate WebSocket E2E tests under `qa-e2e/api-gateway/v0007-authenticated-websocket.spec.ts`; run `npx playwright test qa-e2e/api-gateway/v0007-authenticated-websocket.spec.ts` and verify behavior-specific failures for unauthenticated/invalid/stale upgrades, spoofed context, bearer forwarding, response-frame transformation, or cross-team event exposure.
- [ ] **GREEN:** Implement the minimum WebSocket upgrade and State Registry subscription path using the same claim validation, stale-team rejection, internal-header replacement, canonical operator/team/request context, and browser-credential containment as REST; rerun the exact RED command and verify only token-team event frames are observable unchanged.
- [ ] **REFACTOR:** Reuse verified auth/context primitives across REST and WebSocket paths without coupling transport handling to State Registry ownership policy; rerun both v0007 REST and WebSocket E2E files and the related Gateway suite and keep all green.
- [ ] Fill moved definition `v0007.15-authenticated-websocket-team-isolation.md` and every definition whose implementation reference includes a WebSocket variant with the exact file/test IDs and RED/GREEN evidence.

## 7. Completion gates

- [ ] Run the complete repository E2E suite rooted at `qa-e2e/`; verify token claims, single-team sessions, spoof resistance, REST isolation, WebSocket isolation, bearer containment, and audit attribution pass with no skipped tests.
- [ ] Verify Web UI has one API Gateway origin, displays only State Registry-resolved teams, and has no team CRUD/membership UI; API Gateway obtains groups only from Keycloak, never forwards browser/Keycloak credentials, and never uses `team_name` for authorization; State Registry remains group-to-team mapping, resource-ownership, and platform-audit authority.
- [ ] Verify all three auth diagrams declare the sequence family and no new diagram family was introduced.
- [ ] Run the repository's normal build and related unit/integration suites as supplementary checks; these do not replace required E2E RED/GREEN evidence.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate v0007-auth --strict --no-interactive` after implementation artifacts and task evidence are complete.
