## ADDED Requirements

### Requirement: Web UI renders the operator surface

The Web UI SHALL render task lists, task detail views with live event streams, and configuration pages for automations, executors, routing rules, and secrets.

#### Scenario: Operator opens the dashboard

- **WHEN** an operator opens the Web UI in no-auth mode
- **THEN** the Web UI presents operator-facing task and configuration views without contacting backend services directly

### Requirement: API Gateway is the only backend

The Web UI SHALL send all backend HTTP and WebSocket traffic through the API Gateway and SHALL NOT configure any other backend service origin.

#### Scenario: Operator reads task history

- **WHEN** an operator opens task history in the Web UI
- **THEN** the Web UI sends the request to the API Gateway instead of the State Registry

### Requirement: Operator actions use no-auth gateway-mediated APIs

The Web UI SHALL use API Gateway routes for state reads, executor environment writes, secret writes, task intervention requests, and platform configuration changes without implementing login or token handling in this change.

#### Scenario: Operator stores executor environment data

- **WHEN** an operator submits executor environment or secret data from the Web UI
- **THEN** the Web UI sends the write through the API Gateway to the Env Registry

### Requirement: Client state is non-authoritative

The Web UI SHALL display state received through the API Gateway, keep only transient presentation state, and SHALL NOT persist canonical task, event, audit, routing, or secret data.

#### Scenario: UI refreshes task detail

- **WHEN** the task detail page is refreshed
- **THEN** the Web UI rebuilds the view from API Gateway responses rather than a local authoritative store
