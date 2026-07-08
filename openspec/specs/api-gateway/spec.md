## Purpose

Define the API Gateway as the Web UI's sole backend: an auth-aware L7 reverse proxy that validates auth, routes each request to one allowed backend service, and returns the backend response without owning platform state or business logic.

## Requirements

### Requirement: Gateway validates operator authentication
The API Gateway SHALL validate bearer authentication on every REST request and WebSocket upgrade received from the Web UI, and SHALL issue bearer tokens at login or token refresh.

#### Scenario: Missing bearer token
- **WHEN** a Web UI request reaches the API Gateway without a valid bearer token
- **THEN** the API Gateway rejects the request before proxying it to any backend service

#### Scenario: Operator logs in
- **WHEN** a valid login or token-refresh request reaches the API Gateway
- **THEN** the API Gateway issues a bearer token for subsequent Web UI requests

### Requirement: Gateway routes only to allowed backend services
The API Gateway SHALL proxy Web UI requests only to the State Store or the Secret Service and SHALL NOT have a connection to the Event Router or Executors.

#### Scenario: Web UI reads task state
- **WHEN** the Web UI requests task history or task events
- **THEN** the API Gateway proxies the request to the State Store

#### Scenario: Web UI writes a secret
- **WHEN** the Web UI submits a secret write
- **THEN** the API Gateway proxies the request to the Secret Service

### Requirement: Gateway proxies live-event WebSockets to the State Store
The API Gateway SHALL hold WebSocket connections to the Web UI for live event streaming and proxy accepted frames between the Web UI and the State Store.

#### Scenario: WebSocket upgrade succeeds
- **WHEN** an authenticated Web UI WebSocket upgrade is accepted for live task history
- **THEN** the API Gateway proxies the stream to the State Store event-subscription surface

### Requirement: Gateway remains business-logic-free
The API Gateway SHALL return backend responses without platform-state mutation, aggregation, task validation, queueing, caching, rate limiting, business-policy enforcement, or message transformation.

#### Scenario: State Store returns a task record
- **WHEN** the State Store returns a task record through the API Gateway
- **THEN** the API Gateway returns that response to the Web UI without adding platform fields

### Requirement: Gateway does not own platform state
The API Gateway SHALL NOT persist platform state or maintain a platform database; optional auth-audit logging MAY use a separate auth log and SHALL NOT be stored in the State Store.

#### Scenario: Gateway records auth audit
- **WHEN** auth-audit logging is enabled
- **THEN** the API Gateway writes only auth-audit data to its own auth log and not to platform state stores

### Requirement: Gateway does not mediate backend-to-backend communication
The API Gateway SHALL NOT broker between the State Store and the Secret Service and SHALL forward each request to exactly one selected backend service.

#### Scenario: Gateway handles a State Store read
- **WHEN** the API Gateway proxies a State Store read
- **THEN** it does not call the Secret Service as part of that request
