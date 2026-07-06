## Purpose

Define the Web UI as FlowAI's single-page thin frontend and the only surface a human operator touches directly. It gives operators live task visibility, intervention controls, and platform configuration screens while leaving all backend work to backend services.

## Requirements

### Requirement: Web UI renders the operator surface
The Web UI SHALL render task lists, task detail views with live event streams, and configuration pages for automations, executors, routing rules, and secrets.

#### Scenario: Operator opens the dashboard
- **WHEN** an operator opens the Web UI
- **THEN** the Web UI presents operator-facing task and configuration views without contacting backend services directly

### Requirement: API Gateway is the only backend
The Web UI SHALL send all backend HTTP and WebSocket traffic through the API Gateway and SHALL NOT configure any other backend service origin.

#### Scenario: Operator reads task history
- **WHEN** an operator opens task history in the Web UI
- **THEN** the Web UI sends the request to the API Gateway instead of the State Store

#### Scenario: Operator watches live events
- **WHEN** an operator opens a live task view
- **THEN** the Web UI opens its WebSocket connection to the API Gateway instead of any backend service directly

### Requirement: Operator actions use gateway-mediated APIs
The Web UI SHALL use API Gateway routes for operator authentication, state reads, secret writes, task intervention requests, and platform configuration changes.

#### Scenario: Operator stores a secret
- **WHEN** an operator submits a secret from the Web UI
- **THEN** the Web UI sends the write through the API Gateway

#### Scenario: Operator authenticates
- **WHEN** an operator signs in through the Web UI
- **THEN** the Web UI authenticates through the API Gateway and uses the returned bearer token on subsequent requests

### Requirement: Client state is non-authoritative
The Web UI SHALL display state received through the API Gateway, keep only transient presentation state, and SHALL NOT persist canonical task, event, audit, routing, or secret data.

#### Scenario: UI refreshes task detail
- **WHEN** the task detail page is refreshed
- **THEN** the Web UI rebuilds the view from API Gateway responses rather than a local authoritative store

### Requirement: Web UI does not make backend decisions or execute work
The Web UI SHALL NOT make business decisions, execute tasks, talk to Executors, or bypass the API Gateway for backend access.

#### Scenario: Operator starts an intervention
- **WHEN** an operator requests a task intervention from the Web UI
- **THEN** the Web UI sends the request through the API Gateway instead of deciding execution behavior locally
