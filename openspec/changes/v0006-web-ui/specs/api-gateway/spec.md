## ADDED Requirements

### Requirement: Gateway runs no-auth operator proxy mode

The API Gateway SHALL accept Web UI REST requests and WebSocket upgrades without login or bearer-token validation in this change, and auth SHALL be added only by `v0007-auth`.

#### Scenario: Web UI request reaches no-auth gateway

- **WHEN** a Web UI request reaches the API Gateway before auth is implemented
- **THEN** the API Gateway routes or rejects the request based on allowed backend target only, not bearer-token state

### Requirement: Gateway routes only to allowed backend services

The API Gateway SHALL proxy Web UI requests only to the State Registry or the Env Registry and SHALL NOT have a connection to the Router or Executors.

#### Scenario: Web UI reads task state

- **WHEN** the Web UI requests task history or task events
- **THEN** the API Gateway proxies the request to the State Registry

#### Scenario: Web UI writes executor environment data

- **WHEN** the Web UI submits an executor environment or secret write
- **THEN** the API Gateway proxies the request to the Env Registry

### Requirement: Gateway remains business-logic-free

The API Gateway SHALL return backend responses without platform-state mutation, aggregation, task validation, queueing, caching, business-policy enforcement, or message transformation.

#### Scenario: State Registry returns a task record

- **WHEN** the State Registry returns a task record through the API Gateway
- **THEN** the API Gateway returns that response to the Web UI without adding platform fields
