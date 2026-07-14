## MODIFIED Requirements

### Requirement: Gateway runs configured single-team no-auth proxy mode

The API Gateway SHALL disable the configured-team no-auth operator proxy mode from v0006 and SHALL require a valid bearer token on every protected Web UI REST request and WebSocket upgrade. It SHALL establish exactly one canonical active operator/team context and reject the request before proxying when authentication or team context is unusable.

> Historical requirement identifier retained verbatim from v0006 so this
> MODIFIED delta can replace it. The normative paragraph above defines the
> authenticated v0007 behavior and disables the no-auth bootstrap mode.

#### Scenario: Authenticated proxy request

- **WHEN** a Web UI request carries a valid bearer token with canonical current operator and team claims
- **THEN** the Gateway may create a team-scoped State Registry child request

#### Scenario: Missing bearer token

- **WHEN** a protected Web UI request has no valid bearer token
- **THEN** the Gateway rejects it before creating any backend request or subscription

### Requirement: Gateway establishes trusted configured-team context

For every authenticated REST request and WebSocket subscription, the API Gateway SHALL remove all client-supplied internal identity/context headers, validate the bearer token's canonical `operator_id` and exactly one immutable active `team_id`, and inject `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, optional display-only `X-FlowAI-Team-Name`, and a Gateway-generated `X-FlowAI-Request-ID` into the State Registry child request. It SHALL omit `X-FlowAI-Team-Name` when no canonical name is present and SHALL never use that name for authorization.

#### Scenario: Browser spoofs authenticated context headers

- **WHEN** a validly authenticated browser supplies forged operator, team, team-name, or request-ID headers
- **THEN** the Gateway discards them and State Registry receives only canonical token/auth-state operator and team context plus a Gateway-generated request ID

#### Scenario: Authenticated WebSocket subscription is created

- **WHEN** the Gateway accepts a protected WebSocket upgrade
- **THEN** it establishes the same canonical operator/team/request context on the State Registry child subscription as on REST child requests

### Requirement: State Registry remains authoritative for resource ownership

The API Gateway SHALL pass canonical authenticated `operator_id`, stable `team_id`, optional display-only `team_name`, and request ID to State Registry but SHALL NOT decide whether a resource belongs to that team. State Registry SHALL remain authoritative for task, event, control, environment, and secret ownership and SHALL use stable `team_id`, never `team_name`, for isolation.

#### Scenario: Authenticated team requests another team's resource

- **WHEN** a valid token-bound team requests a resource owned by a different `team_id`
- **THEN** State Registry denies access using resource ownership and the Gateway returns that denial unchanged

### Requirement: Gateway remains business-logic-free

The API Gateway SHALL return State Registry REST response status, headers, and body and WebSocket frames without platform-state mutation, aggregation, task validation, queueing, caching, business-policy enforcement, or response transformation. Authentication validation, bearer containment, spoofed-header removal, trusted security-context injection, request-ID generation, and allowed-target enforcement SHALL be transport security behavior and SHALL NOT be considered platform business logic.

#### Scenario: Authenticated State Registry response returns

- **WHEN** State Registry returns a success or ownership denial after evaluating canonical team context
- **THEN** the Gateway forwards the response status, headers, and body unchanged and adds no platform fields

## ADDED Requirements

### Requirement: Gateway rejects unusable authenticated team context

The API Gateway SHALL reject a protected REST request or WebSocket upgrade before proxying when `operator_id` or `team_id` is missing, when team identity is duplicated or ambiguous, or when the token's team assignment is inactive or stale relative to canonical authentication state.

#### Scenario: Team claim is missing or ambiguous

- **WHEN** a bearer token lacks `team_id` or represents more than one team identity
- **THEN** the Gateway rejects it before State Registry receives a child request

#### Scenario: Team claim is stale

- **WHEN** the token's immutable `team_id` no longer matches the operator's active canonical team assignment
- **THEN** the Gateway rejects both REST and WebSocket access before proxying

### Requirement: Gateway contains browser bearer credentials

The API Gateway SHALL remove the browser `Authorization` header and SHALL NOT forward the bearer token, refresh token, login credential, cookie credential, or equivalent browser authentication secret to State Registry on REST or WebSocket child requests.

#### Scenario: Valid browser bearer is consumed

- **WHEN** the Gateway validates a browser bearer token and creates a State Registry child request
- **THEN** State Registry receives trusted identity context headers and no browser credential

### Requirement: Gateway provides canonical audit attribution context

For every accepted operator mutation, the API Gateway SHALL provide State Registry with canonical `operator_id`, stable `team_id`, and Gateway-generated request ID so State Registry can persist the canonical platform audit record. The optional `team_name` SHALL be display-only and SHALL NOT determine audit ownership.

#### Scenario: Authenticated operator creates a control request

- **WHEN** an authenticated operator submits a control request for a task owned by the token's `team_id`
- **THEN** State Registry records the mutation and audit attribution using canonical operator, stable team, and request identifiers supplied by the Gateway
