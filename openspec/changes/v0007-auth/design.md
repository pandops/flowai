# Design: Authenticated Canonical Team Context

## Runtime Shape

- Web UI starts Keycloak authentication through API Gateway and sends credentials or authorization responses only to the Gateway.
- API Gateway completes OIDC with Keycloak, validates issuer/signature/audience/expiry, and obtains the canonical `operator_id` and group identifiers from validated Keycloak state.
- API Gateway sends the canonical group identifiers to the trusted Gateway-only State Registry team-resolution API. State Registry maps them to canonical teams and returns deterministic `{team_id, team_name}` entries.
- API Gateway returns the resolved team list to Web UI. If more than one team is available, Web UI displays the list and asks the operator to select one.
- API Gateway issues a signed working bearer token only after the selected `team_id` is confirmed in a fresh State Registry resolution. Each working token binds one canonical `operator_id` to exactly one immutable active `team_id`; `team_name` is display-only.
- Tokens carry and the Gateway validates intended audience and expiry in addition to signature/issuer checks.
- API Gateway validates bearer and canonical team freshness on every Web UI REST request and WebSocket upgrade before creating a State Registry child request.
- API Gateway remains disconnected from Executors and State Registry remains the only platform backend.

## Canonical Identity and Team State

- Keycloak is authoritative for authentication and user-to-group membership. State Registry is authoritative for group-to-team mapping and canonical team identity.
- Each team stores one immutable unique Keycloak group identifier. Group display names are not authorization keys.
- A working token cannot contain multiple team IDs or silently switch its team ID during refresh. Selecting another accessible team requires a fresh group resolution and a newly issued working token.
- `team_name` is copied from State Registry for display and is never an authorization or ownership key.
- No team membership mutation, team update/delete, or project RBAC is introduced.
- State Registry is not an auth session store. Any Gateway auth audit remains separate from canonical platform state.

## Trusted Downstream Context

1. Browser sends the Keycloak authorization response or Gateway working bearer to API Gateway and may attempt to add forged groups or `X-FlowAI-*` headers.
2. Gateway validates Keycloak state and obtains canonical group identifiers, or validates the working bearer and its one non-empty `team_id`.
3. For login, team listing, selection, and freshness checks, Gateway resolves canonical groups through State Registry and accepts a selected team only when it appears in that result.
4. Gateway rejects missing, invalid, expired, unmapped, unauthorized, or stale identity/team context before proxying or opening a child WebSocket.
5. Gateway removes browser-supplied groups and internal identity/context headers.
6. Gateway creates a request ID and injects canonical `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, optional display-only `X-FlowAI-Team-Name`, and `X-FlowAI-Request-ID` on every team-scoped REST and WebSocket child request.
7. Gateway removes `Authorization` and does not forward browser or Keycloak credentials to State Registry.
8. State Registry uses trusted `team_id` for resource ownership/isolation and the canonical operator/team/request identifiers for audit attribution.

## Web UI Session Behavior

- Web UI stores the bearer token only as client session state and uses it only with API Gateway.
- Web UI displays the State Registry-resolved team list and canonical `team_name` values without inferring authorization from names.
- Web UI may select one resolved team for a new working token. It offers no arbitrary team input, team CRUD, or membership management.
- Web UI continues to use a single API Gateway base URL and never calls State Registry or an Executor directly.

## Business-Logic Boundary

- Authentication checks, bearer containment, spoofed-header removal, trusted security-context injection, request-ID generation, and allowed-target enforcement are Gateway transport security, not platform business logic.
- Gateway does not decide resource ownership or rewrite State Registry results.
- REST response status, headers, and body and WebSocket event frames remain unchanged across the Gateway after successful authentication/context establishment.

## Proposed Diagrams

- `specs/diagrams/01-auth-login.puml`
- `specs/diagrams/02-authenticated-request.puml`
- `specs/diagrams/03-authenticated-websocket.puml`
