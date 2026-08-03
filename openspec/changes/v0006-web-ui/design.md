# Design: Configured Single-Team Web UI without Auth

## Runtime Shape

- Web UI has one backend base URL: API Gateway.
- API Gateway runs in no-auth bootstrap mode for exactly one deployment-configured team and exactly one deployment-configured bootstrap operator.
- Deployment configuration supplies one non-empty stable `team_id`, one non-empty canonical display-only `team_name`, and one non-empty bootstrap `operator_id`. A missing, empty, or ambiguous value for any of the three prevents the Gateway from serving operator proxy traffic.
- `team_id` is the only team value used for scoping and authorization; `team_name` is display-only and never an ownership key.
- The configured bootstrap `operator_id` is audit attribution only. It is not an authentication proof, is not bound to a session, and does not change the Gateway's no-auth proxy behavior. It is replaced by the authenticated canonical `operator_id` once `v0007-auth` introduces bearer tokens.
- API Gateway proxies every allowed platform REST request and live-event WebSocket only to State Registry.
- State Registry remains authoritative for the ownership and visibility of tasks, events, controls, environments, and secrets, and uses trusted `team_id` plus the trusted `operator_id` (bootstrap or authenticated) for audit attribution.

## Trusted Downstream Context

- Browser input is untrusted, including `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, `X-FlowAI-Team-Name`, and `X-FlowAI-Request-ID`.
- Before creating a child request, API Gateway removes all client-supplied internal identity/context headers, including any client-supplied `X-FlowAI-Operator-ID`.
- For every REST child request and WebSocket subscription child request, API Gateway injects:
  - `X-FlowAI-Operator-ID` set to the configured bootstrap `operator_id`;
  - `X-FlowAI-Team-ID` set to the configured `team_id`;
  - `X-FlowAI-Team-Name` set to the configured display-only `team_name`;
  - `X-FlowAI-Request-ID` set to a gateway-generated request identifier.
- State Registry scopes resource access from the trusted `team_id` and attributes mutations and reads to the trusted `operator_id` plus the generated `request_id`; it may use `team_name` only for display metadata.
- Header removal and trusted context injection are security transport enrichment, not platform business logic or response transformation.

## Web UI Behavior

- Web UI displays the configured canonical team name returned through the Gateway as deployment context.
- Web UI does not display, supply, select, or otherwise assert the configured bootstrap `operator_id`.
- Web UI does not offer a team selector, membership management, or team administration.
- Web UI never generates, persists, or relies on `X-FlowAI-Operator-ID` or any other internal identity/context header and never calls State Registry or an Executor directly.

## Response and Topology Boundaries

- API Gateway returns State Registry response status, headers, and body unchanged after request-side security-context enrichment.
- API Gateway never calls an Executor and never persists canonical platform state.
- `v0007-auth` replaces the configured bootstrap `operator_id` with an authenticated canonical `operator_id` and adds bearer-token validation; the single configured `team_id`/`team_name` configuration model is replaced by per-token canonical team context at that point.

## Proposed Diagrams

- `specs/diagrams/01-operator-surface-no-auth.puml`
- `specs/diagrams/02-gateway-routing-no-auth.puml`
- `specs/diagrams/03-live-events-no-auth.puml`
- `specs/diagrams/04-env-secret-write-no-auth.puml`
- `specs/diagrams/05-control-request-no-auth.puml`
