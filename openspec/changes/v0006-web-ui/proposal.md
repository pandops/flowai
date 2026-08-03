# Change: v0006-web-ui

## Why

After backend services and executors exist, FlowAI needs an operator surface for reading task history, watching events, writing executor environment data, and recording control requests. This change provides a deliberately limited bootstrap deployment: the Web UI and API Gateway run without operator authentication, but every request is confined to one configured team and attributed to one configured bootstrap operator so context-free proxying cannot accidentally become a cross-team or unaccounted audit contract that v0002 State Registry would silently accept.

## What Changes

- Add Web UI as the operator-facing frontend with the API Gateway as its only HTTP and WebSocket backend.
- Run API Gateway in configured single-team no-auth bootstrap mode using
  exactly one deployment-configured non-empty `team_id`, exactly one
  deployment-configured non-empty bootstrap `operator_id`, and an optional
  canonical display-only `team_name`.
- Treat the configured bootstrap `operator_id` as audit attribution context only. It SHALL NOT be used as an authentication proof and SHALL be replaced by an authenticated canonical `operator_id` in `v0007-auth`.
- Display `team_name` as non-authoritative presentation context without adding a team selector, team membership UI, or team administration.
- Strip every browser-supplied internal identity/context header and inject the configured bootstrap `operator_id`, configured `team_id`, display-only `team_name`, and gateway-generated request ID on every REST and WebSocket child request to State Registry.
- Reject operator proxy traffic before creating any child request when the
  configured `operator_id` or `team_id` is missing, empty, ambiguous, or
  otherwise invalid; reject `team_name` only when it is supplied but empty or
  invalid.
- Web UI SHALL NOT generate, persist, or rely on `X-FlowAI-Operator-ID` (or any other internal identity/context header) and SHALL NOT display, select, or supply the configured bootstrap `operator_id`.
- Proxy operator task reads, live-event streams, control requests, and
  team-owned environment/secret writes only to State Registry; State
  Registry remains authoritative for resource ownership.
- Expose no team, source-system, or task-type registration through Web UI or
  API Gateway. Those create-only `/admin/*` APIs are direct State Registry
  surfaces for authenticated system administrators and are not available in
  the no-auth bootstrap operator path.
- Treat trusted security-context enrichment as gateway transport behavior, not platform business logic, while preserving State Registry response status, headers, and body.

## Impact

- Change type: development
- Affected specs: `specs/web-ui/spec.md`, `specs/api-gateway/spec.md`
- Affected ADRs: none
- Affected diagrams: `specs/diagrams/01-operator-surface-no-auth.puml`, `specs/diagrams/02-gateway-routing-no-auth.puml`, `specs/diagrams/03-live-events-no-auth.puml`, `specs/diagrams/04-env-secret-write-no-auth.puml`, `specs/diagrams/05-control-request-no-auth.puml`
- Affected test cases: `specs/test-cases/v0006.*.md` (11 contiguous E2E definitions)
- Affected code: future `web-ui/`, `api-gateway/`, and root `autotest/` implementation paths

## Out of Scope

- Bearer-token validation, login, operator identity, or authenticated canonical operator/team claims; these are introduced by `v0007-auth`, which replaces the configured bootstrap `operator_id` with an authenticated canonical `operator_id`.
- Team CRUD, team membership management, team selector UI, multi-team sessions, or project RBAC.
- System-administrator registration or projection APIs, including
  `/admin/teams`, `/admin/source-systems`, `/admin/task-types`,
  `/admin/tags`, and `/admin/tasks`.
- API Gateway calls to Executors or Web UI calls to State Registry or Executors.
- Gateway ownership decisions, platform-state persistence, response transformation, caching, queueing, or other business logic.
- Using `team_name` or the configured bootstrap `operator_id` for authorization, ownership, isolation, session binding, or as an authentication proof.
