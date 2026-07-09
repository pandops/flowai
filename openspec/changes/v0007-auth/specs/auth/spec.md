## ADDED Requirements

### Requirement: Gateway issues bearer tokens

The API Gateway SHALL issue bearer tokens at login or token refresh for Web UI operator sessions.

#### Scenario: Operator logs in

- **WHEN** valid login credentials reach the API Gateway
- **THEN** the API Gateway returns a bearer token for subsequent Web UI requests

### Requirement: Gateway validates operator bearer tokens

The API Gateway SHALL validate bearer authentication on every REST request and WebSocket upgrade received from the Web UI before proxying to any backend service.

#### Scenario: Missing bearer token

- **WHEN** a Web UI request reaches the API Gateway without a valid bearer token
- **THEN** the API Gateway rejects the request before proxying it to any backend service

### Requirement: Auth remains gateway-local

The API Gateway SHALL NOT use Router, State Registry, or Env Registry as a session store, and optional auth-audit logging SHALL remain separate from platform state.

#### Scenario: Auth audit is enabled

- **WHEN** auth-audit logging is enabled
- **THEN** the API Gateway writes only auth-audit data to its own auth log and not to platform state registries
