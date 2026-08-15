# Change: v0006-web-ui

## Why

After backend services and executors exist, FlowAI needs an operator surface for reading task history, watching events, writing executor environment data, and recording control requests. This change implements the Web UI against a test-only mocked proxy because API Gateway, authentication, Keycloak group discovery, and group-to-team binding do not exist at this stage. The UI receives a test-provided list of available teams, always has exactly one selected team, and sends that team's stable `team_id` on every team-scoped request and live subscription.

## What Changes

- Add Web UI as the operator-facing frontend with one configurable HTTP and
  WebSocket adapter origin. In v0006 that adapter is a test-only mocked proxy;
  no production API Gateway service is implemented.
- Provide the UI with a deterministic list of teams through mocked-proxy test
  configuration. Render it as a selector, require exactly one selected team,
  and replace the `team_id` on all later requests and subscriptions when the
  operator selects another team.
- Keep team discovery, user membership, Keycloak groups, group-to-team
  mapping, authentication, authorization, and trusted operator attribution
  outside this change. The mocked proxy does not claim to implement them.
- Extend the State Registry API contract with the exact reads, mutations,
  pagination, filtering, history, audit, controls, and streaming operations
  required by the UI.
- Present environment definitions to operators as launch parameters and add
  team- and task-type-scoped applicability. Global launch parameters
  are consumed during resolution but remain administrator-managed outside
  this change.
- Add immutable non-secret launch-parameter revision history and immutable
  task-control events so accepted cancellation requests and their later
  Executor results can be reconstructed and streamed without inventing task
  lifecycle states.
- Expose no team, source-system, or task-type registration through Web UI or
  mocked proxy. Those create-only `/admin/*` APIs are direct State Registry
  surfaces for authenticated system administrators and are not available in
  the v0006 browser adapter path.
- Keep mocked-proxy behavior limited to deterministic test transport and
  fixtures; it does not become a production service or own platform state.

## Impact

- Change type: development
- Affected specs: `specs/web-ui/spec.md`, `specs/mocked-proxy/spec.md`,
  `specs/state-registry/spec.md`
- Affected ADRs: none
- Affected diagrams: `specs/diagrams/01-operator-surface-no-auth.puml`, `specs/diagrams/02-gateway-routing-no-auth.puml`, `specs/diagrams/03-live-events-no-auth.puml`, `specs/diagrams/04-env-secret-write-no-auth.puml`, `specs/diagrams/05-control-request-no-auth.puml`
- Affected test cases: `v0006.1` through `v0006.34` (34 contiguous E2E definitions; implementation-active definitions live under `qa-e2e/test-cases/`)
- Affected code: standalone `svc/web-ui/`, State Registry API implementation, and root `qa-e2e/` mocked-proxy/test paths

## Out of Scope

- Bearer-token validation, login, operator identity, authenticated canonical
  operator/team claims, Keycloak groups, group discovery, group-to-team
  mapping, and authorization of the supplied team list.
- Team CRUD, team membership management, or project RBAC. Team selection from
  the already supplied list is in scope; populating that list is not.
- System-administrator registration or projection APIs, including
  `/admin/teams`, `/admin/source-systems`, `/admin/task-types`,
  `/admin/tags`, and `/admin/tasks`.
- A production API Gateway service or tests that claim Gateway security,
  routing, trusted-header enrichment, or Keycloak integration is implemented.
- Web UI calls to Executors or direct browser bypass of its configured adapter
  origin.
- Creating, updating, or deleting global launch parameters; that remains an
  authenticated system-administrator surface outside v0006.
