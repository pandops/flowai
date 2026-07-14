# Design: Authenticated Canonical Team Context

## Runtime Shape

- Web UI authenticates through API Gateway and sends bearer credentials only to the Gateway.
- API Gateway issues signed bearer tokens at login or refresh from canonical Gateway auth identity state.
- Each token/session binds one canonical `operator_id` to exactly one immutable active `team_id`; an optional canonical `team_name` is display-only.
- Tokens carry and the Gateway validates intended audience and expiry in addition to signature/issuer checks.
- API Gateway validates bearer and canonical team freshness on every Web UI REST request and WebSocket upgrade before creating a State Registry child request.
- API Gateway remains disconnected from Executors and State Registry remains the only platform backend.

## Canonical Auth State

- The Gateway's authentication configuration/identity source resolves one stable operator identifier and exactly one active team assignment for login.
- A token cannot contain multiple team IDs or switch its team ID during its session. A changed or inactive canonical assignment makes the existing token stale and requires a new authentication session; refresh does not silently switch teams.
- `team_name`, when present, is copied from canonical auth state for display and is never an authorization or ownership key.
- No team CRUD, membership-management UI, selector, or project RBAC is introduced.
- State Registry is not an auth session store. Any Gateway auth audit remains separate from canonical platform state.

## Trusted Downstream Context

1. Browser sends `Authorization: Bearer ...` to API Gateway and may attempt to add forged `X-FlowAI-*` headers.
2. Gateway validates signature/issuer, audience, expiry, canonical `operator_id`, exactly one non-empty `team_id`, optional canonical `team_name`, and active/non-stale team assignment.
3. Gateway rejects missing, duplicated/ambiguous, invalid, expired, or stale identity/team context before proxying or opening a child WebSocket.
4. Gateway removes browser-supplied `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, `X-FlowAI-Team-Name`, and `X-FlowAI-Request-ID`.
5. Gateway creates a request ID and injects canonical `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, optional display-only `X-FlowAI-Team-Name`, and `X-FlowAI-Request-ID` on every REST and WebSocket child request.
6. Gateway removes `Authorization` and does not forward the browser bearer token to State Registry.
7. State Registry uses trusted `team_id` for resource ownership/isolation and the canonical operator/team/request identifiers for audit attribution.

## Web UI Session Behavior

- Web UI stores the bearer token only as client session state and uses it only with API Gateway.
- Web UI displays optional canonical `team_name` from authenticated context, without inferring authorization from it.
- Web UI offers no team selector, team switch, team CRUD, or membership management.
- Web UI continues to use a single API Gateway base URL and never calls State Registry or an Executor directly.

## Business-Logic Boundary

- Authentication checks, bearer containment, spoofed-header removal, trusted security-context injection, request-ID generation, and allowed-target enforcement are Gateway transport security, not platform business logic.
- Gateway does not decide resource ownership or rewrite State Registry results.
- REST response status, headers, and body and WebSocket event frames remain unchanged across the Gateway after successful authentication/context establishment.

## Proposed Diagrams

- `specs/diagrams/01-auth-login.puml`
- `specs/diagrams/02-authenticated-request.puml`
- `specs/diagrams/03-authenticated-websocket.puml`
