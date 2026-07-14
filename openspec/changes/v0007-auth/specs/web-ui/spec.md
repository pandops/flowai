## MODIFIED Requirements

### Requirement: Web UI renders the configured-team operator surface

The Web UI SHALL render the operator surface in an authenticated session bound to exactly one immutable active `team_id`. It MAY display the optional canonical `team_name` as presentation context, SHALL NOT use `team_name` for authorization, and SHALL NOT provide team selection, team switching, team CRUD, or membership-management UI.

#### Scenario: Authenticated operator opens the dashboard

- **WHEN** an authenticated operator opens the Web UI
- **THEN** the UI presents the single token-bound team context, optionally labels it with canonical `team_name`, and presents no team selector or membership controls

### Requirement: Operator actions use configured-team no-auth gateway-mediated APIs

The Web UI SHALL authenticate operators through API Gateway, store the returned bearer token only as client session state, and attach it to subsequent API Gateway REST requests and WebSocket upgrades. Operator actions SHALL no longer use the configured-team no-auth behavior introduced by v0006 and SHALL remain bound to the token's one immutable `team_id`.

> Historical requirement identifier retained verbatim from v0006 so this
> MODIFIED delta can replace it. The normative paragraph above defines the
> authenticated v0007 behavior and disables the no-auth bootstrap behavior.

#### Scenario: Operator authenticates

- **WHEN** an operator signs in through the Web UI
- **THEN** the Web UI authenticates only through API Gateway and uses the returned bearer token only on API Gateway requests for that token-bound team

### Requirement: Web UI does not assert internal identity context

The Web UI SHALL NOT generate, persist, or rely on `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, `X-FlowAI-Team-Name`, `X-FlowAI-Request-ID`, or equivalent trusted downstream identity headers. It SHALL send only the bearer credential to API Gateway for authentication and SHALL treat any canonical `team_name` as display-only.

#### Scenario: Authenticated browser request is created

- **WHEN** the Web UI creates an authenticated REST request or WebSocket upgrade
- **THEN** it sends the bearer credential to API Gateway without asserting operator, team, team-name, or request-ID headers
