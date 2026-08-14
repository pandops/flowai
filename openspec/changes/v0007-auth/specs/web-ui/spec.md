## MODIFIED Requirements

### Requirement: Web UI renders State Registry-resolved teams

The Web UI SHALL authenticate only through API Gateway and render every canonical `{team_id, team_name}` entry returned by the Gateway from State Registry group resolution. It SHALL treat `team_name` as presentation context, SHALL NOT derive access from a name, and SHALL NOT accept arbitrary browser-entered team or group identifiers. When more than one team is returned, it SHALL allow the user to select one resolved team for a new one-team working token. It SHALL NOT provide team CRUD or membership-management UI.

#### Scenario: Authenticated operator opens the dashboard

- **WHEN** an authenticated operator's Keycloak groups resolve to multiple State Registry teams
- **THEN** the UI displays their canonical IDs and names, permits selection only from that list, and presents no team CRUD or membership controls

### Requirement: Operator actions use configured-team no-auth gateway-mediated APIs

The Web UI SHALL start Keycloak authentication through API Gateway, receive accessible teams only from Gateway, store the returned one-team working bearer only as client session state, and attach it to subsequent API Gateway REST requests and WebSocket upgrades. Operator actions SHALL no longer use the configured-team no-auth behavior introduced by v0006 and SHALL remain bound to the working token's one immutable `team_id`.

> Historical requirement identifier retained verbatim from v0006 so this
> MODIFIED delta can replace it. The normative paragraph above defines the
> authenticated v0007 behavior and disables the no-auth bootstrap behavior.

#### Scenario: Operator authenticates

- **WHEN** an operator signs in through the Web UI
- **THEN** the Web UI authenticates only through API Gateway, displays the Gateway-returned resolved teams, and uses the selected team's working bearer only on API Gateway requests

### Requirement: Web UI does not assert internal identity context

The Web UI SHALL NOT generate, persist, or rely on Keycloak group claims, `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, `X-FlowAI-Team-Name`, `X-FlowAI-Request-ID`, or equivalent trusted downstream identity headers. It SHALL send only the Keycloak authorization response or working bearer to API Gateway and SHALL treat every canonical `team_name` as display-only.

#### Scenario: Authenticated browser request is created

- **WHEN** the Web UI creates an authenticated REST request or WebSocket upgrade
- **THEN** it sends the bearer credential to API Gateway without asserting operator, team, team-name, or request-ID headers
