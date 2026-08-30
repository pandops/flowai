## MODIFIED Requirements

### Requirement: Web UI renders one selected-team operator surface

The Web UI SHALL authenticate only through API Gateway and render every canonical `{team_id, team_name, archived_at?}` entry returned by the Gateway from its service-owned OIDC membership mapping and current State Registry presentation data. It SHALL display archived teams with an explicit archived state and SHALL keep them selectable for authenticated access to existing data. It SHALL treat `team_name` and `archived_at` as presentation context, SHALL NOT derive membership from either field, and SHALL NOT accept arbitrary browser-entered team or OIDC team identifiers. When more than one team is returned, it SHALL allow the user to select one resolved active or archived team for a new one-team working token. It SHALL NOT provide team CRUD, task creation, or membership-management UI.

#### Scenario: Authenticated operator opens the dashboard

- **WHEN** an authenticated operator's OIDC teams resolve to multiple State Registry teams
- **THEN** the UI displays their canonical IDs, names, and archived state, permits selection only from that list, and presents no team CRUD, task-creation, or membership controls

#### Scenario: Operator selects an archived team

- **WHEN** Gateway returns a mapped team with non-null `archived_at` and the operator selects it
- **THEN** Web UI marks the team archived and allows authenticated access to its existing data through the same read surfaces as for an active team

### Requirement: Operator actions carry the selected team identifier

The Web UI SHALL start OIDC authentication through API Gateway, receive accessible teams only from Gateway, store the returned one-team working bearer only as client session state, and attach it to subsequent API Gateway REST requests and WebSocket upgrades. Operator actions SHALL no longer use the configured-team no-auth behavior introduced by v0006 and SHALL remain bound to the working token's one immutable `team_id`.

The production browser origin SHALL be composed by a separate ingress or reverse proxy: `/` and Web UI static assets route to the Web UI service, while `/auth`, `/admin`, `/ui`, and WebSocket upgrades route to API Gateway. The Web UI and API Gateway SHALL remain separate runtime services and SHALL NOT serve or embed one another.

> Historical requirement identifier retained verbatim from v0006 so this
> MODIFIED delta can replace it. The normative paragraph above defines the
> authenticated v0007 behavior and disables the no-auth bootstrap behavior.

#### Scenario: Operator authenticates

- **WHEN** an operator signs in through the Web UI
- **THEN** the Web UI authenticates only through API Gateway, displays the Gateway-returned resolved teams, and uses the selected team's working bearer only on API Gateway requests

### Requirement: Web UI does not assert authentication context

The Web UI SHALL NOT generate, persist, or rely on OIDC team claims, `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, `X-FlowAI-Team-Name`, `X-FlowAI-Request-ID`, or equivalent trusted downstream identity headers. It SHALL send only the OIDC provider authorization response or working bearer to API Gateway and SHALL treat every canonical `team_name` as display-only.

#### Scenario: Authenticated browser request is created

- **WHEN** the Web UI creates an authenticated REST request or WebSocket upgrade
- **THEN** it sends the bearer credential to API Gateway without asserting operator, team, team-name, or request-ID headers
