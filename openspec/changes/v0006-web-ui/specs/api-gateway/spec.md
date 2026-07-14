## ADDED Requirements

### Requirement: Gateway runs configured single-team no-auth proxy mode

The API Gateway SHALL accept Web UI REST requests and WebSocket upgrades without login or bearer-token validation only in a bootstrap mode bound to exactly one deployment-configured non-empty bootstrap `operator_id`, one deployment-configured non-empty stable `team_id`, and one deployment-configured non-empty canonical `team_name`. It SHALL refuse to serve operator proxy traffic when any of the configured `operator_id`, `team_id`, or `team_name` is missing, empty, ambiguous, or otherwise invalid. The configured bootstrap `operator_id` is audit attribution context only and SHALL NOT be treated as an authentication proof. The stable `team_id` SHALL scope access; `team_name` SHALL be display-only and SHALL NOT be used for authorization or ownership decisions.

#### Scenario: Web UI request reaches configured no-auth gateway

- **WHEN** a Web UI request reaches a correctly configured API Gateway before auth is implemented
- **THEN** the Gateway handles it only within the single configured `team_id`, attributes it to the configured bootstrap `operator_id`, and does not require a bearer token

#### Scenario: Gateway operator configuration is invalid

- **WHEN** the Gateway has a missing, empty, or ambiguous configured bootstrap `operator_id`
- **THEN** it rejects operator proxy traffic before creating a State Registry child request

#### Scenario: Gateway team configuration is invalid

- **WHEN** the Gateway has a missing, empty, or ambiguous configured team identity
- **THEN** it rejects operator proxy traffic before creating a State Registry child request

### Requirement: Gateway routes only to allowed backend services

The API Gateway SHALL proxy Web UI requests only to State Registry and SHALL NOT configure or call an Executor or any other platform backend.

#### Scenario: Web UI reads task state

- **WHEN** the Web UI requests task history or task events
- **THEN** the API Gateway creates a child request only to State Registry

#### Scenario: Web UI writes executor environment data

- **WHEN** the Web UI submits an executor environment or secret write
- **THEN** the API Gateway creates a child request only to State Registry

### Requirement: Gateway establishes trusted configured-team context

For every proxied REST request and WebSocket subscription, the API Gateway SHALL remove all client-supplied internal identity/context headers, including `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, `X-FlowAI-Team-Name`, and `X-FlowAI-Request-ID`; SHALL inject the configured bootstrap `operator_id`, the configured `team_id`, the configured display-only `team_name`, and a gateway-generated request ID into the State Registry child request; and SHALL treat the configured bootstrap `operator_id` as audit attribution only and not as an authentication proof. Every child request SHALL therefore be attributed to the configured bootstrap operator and scoped to the configured team regardless of browser-supplied values.

#### Scenario: Browser spoofs internal identity headers

- **WHEN** a REST request or WebSocket upgrade supplies forged internal operator, team, team-name, or request identifiers
- **THEN** the Gateway discards those values and sends only gateway-established configured bootstrap operator, configured team, configured display-only team name, and a gateway-generated request ID to State Registry

#### Scenario: Gateway creates WebSocket subscription

- **WHEN** the Gateway accepts a WebSocket upgrade for live events
- **THEN** it creates the State Registry subscription using the same configured bootstrap `operator_id` and configured `team_id` scope as REST child requests

### Requirement: State Registry remains authoritative for resource ownership

The API Gateway SHALL pass trusted bootstrap `operator_id` and `team_id` context to State Registry but SHALL NOT decide whether a task, event, control, environment, or secret belongs to that team. State Registry SHALL remain authoritative for resource ownership and SHALL use the stable `team_id`, never `team_name`, for isolation decisions, while attributing mutations and reads to the trusted `operator_id` and gateway-generated `request_id`.

#### Scenario: Configured team requests another team's resource

- **WHEN** a configured-team child request identifies a resource not owned by that `team_id`
- **THEN** State Registry makes the ownership decision and the Gateway returns its denial unchanged

### Requirement: Gateway remains business-logic-free

The API Gateway SHALL return State Registry response status, headers, and body without platform-state mutation, aggregation, task validation, queueing, caching, business-policy enforcement, or response transformation. Removing untrusted identity headers, injecting trusted security context, generating a request ID, and enforcing the allowed backend target SHALL be transport security behavior and SHALL NOT be considered platform business logic.

#### Scenario: State Registry returns a task record

- **WHEN** State Registry returns a task record through a configured-team child request
- **THEN** the API Gateway returns the response status, headers, and body to the Web UI unchanged and adds no platform fields

#### Scenario: State Registry denies team ownership

- **WHEN** State Registry returns an ownership denial
- **THEN** the API Gateway forwards that denial unchanged rather than replacing it with a Gateway policy decision
