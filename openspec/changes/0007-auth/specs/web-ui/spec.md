## MODIFIED Requirements

### Requirement: Web UI authenticates operators through the API Gateway

The Web UI SHALL authenticate operators through the API Gateway, store the returned bearer token as client session state, and attach it to subsequent REST requests and WebSocket upgrades.

#### Scenario: Operator authenticates

- **WHEN** an operator signs in through the Web UI
- **THEN** the Web UI authenticates through the API Gateway and uses the returned bearer token on subsequent requests
