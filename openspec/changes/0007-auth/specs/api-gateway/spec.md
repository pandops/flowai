## MODIFIED Requirements

### Requirement: Gateway validates operator authentication

The API Gateway SHALL validate bearer authentication on every REST request and WebSocket upgrade received from the Web UI, and SHALL issue bearer tokens at login or token refresh.

#### Scenario: Authenticated proxy request

- **WHEN** a Web UI request reaches the API Gateway with a valid bearer token
- **THEN** the API Gateway may proxy the request to the selected allowed backend service
