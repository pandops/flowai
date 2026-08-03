## ADDED Requirements

### Requirement: Web UI renders the configured-team operator surface

The Web UI SHALL render task lists, task detail views with live event streams, and configuration pages for automations, executors, controls, environments, and secrets in configured single-team no-auth bootstrap mode. It SHALL display the canonical configured `team_name` as presentation context, SHALL NOT use `team_name` for authorization, and SHALL NOT provide a team selector, team CRUD, or team membership UI.

#### Scenario: Operator opens the configured-team dashboard

- **WHEN** an operator opens the Web UI in no-auth bootstrap mode
- **THEN** the Web UI presents operator-facing views labeled with the configured canonical team name and presents no control for selecting or administering another team

### Requirement: API Gateway is the only backend

The Web UI SHALL send all backend HTTP and WebSocket traffic through the API Gateway and SHALL NOT configure or call any State Registry, Executor, or other backend service origin.

#### Scenario: Operator reads task history

- **WHEN** an operator opens task history in the Web UI
- **THEN** the Web UI sends the request only to the API Gateway and makes no direct request to State Registry or an Executor

### Requirement: Operator actions use configured-team no-auth gateway-mediated APIs

The Web UI SHALL use API Gateway routes for state reads, executor environment writes, secret writes, task intervention requests, and platform configuration changes without implementing login or token handling in this change. Every operator action SHALL remain within the Gateway's deployment-configured team context.

#### Scenario: Operator stores executor environment data

- **WHEN** an operator submits executor environment or secret data from the Web UI
- **THEN** the Web UI sends the write through the API Gateway without selecting, supplying, or overriding team identity

### Requirement: Web UI does not assert internal identity context

The Web UI SHALL NOT generate, persist, rely on, display, supply, or select `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, `X-FlowAI-Team-Name`, `X-FlowAI-Request-ID`, or equivalent trusted downstream identity headers. In particular, the Web UI SHALL NOT display, supply, or otherwise assert the configured bootstrap `operator_id`; it SHALL rely on the API Gateway to establish trusted downstream context, including the configured bootstrap `operator_id`, on its behalf. Browser-visible team names SHALL remain display-only.

#### Scenario: Browser request is created

- **WHEN** the Web UI creates a REST request or WebSocket upgrade
- **THEN** it sends no internal identity/context headers, including no `X-FlowAI-Operator-ID`, and relies on the API Gateway to establish trusted downstream context

#### Scenario: Web UI never references the configured bootstrap operator

- **WHEN** the operator inspects the Web UI surface and source
- **THEN** the Web UI does not display, expose, log, or otherwise assert the configured bootstrap `operator_id` and offers no control for changing it

### Requirement: Client state is non-authoritative

The Web UI SHALL display state received through the API Gateway, keep only transient presentation state, and SHALL NOT persist canonical task, event, audit, team, routing, or secret data.

#### Scenario: UI refreshes task detail

- **WHEN** the task detail page is refreshed
- **THEN** the Web UI rebuilds the configured-team view from API Gateway responses rather than a local authoritative store
