## ADDED Requirements

### Requirement: API Gateway forwards team-scoped target-reference operations only

API Gateway SHALL forward authenticated target-reference operations to State Registry with verified `operator_id`, immutable `team_id`, and `request_id`. It SHALL reject caller-authoritative team identity and SHALL NOT proxy LiteLLM Admin UI, management API, model inference, A2A, credentials, or sessions.

#### Scenario: Same-team target operation is forwarded

- **WHEN** an authenticated operator submits a target-reference operation
- **THEN** API Gateway replaces caller identity context with verified operator and team context before forwarding to State Registry

#### Scenario: Browser attempts LiteLLM proxying

- **WHEN** a browser requests a LiteLLM management, inference, A2A, or login path through API Gateway
- **THEN** API Gateway rejects the request without contacting LiteLLM
