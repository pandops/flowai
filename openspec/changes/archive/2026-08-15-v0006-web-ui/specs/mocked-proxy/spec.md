## ADDED Requirements

### Requirement: Mocked proxy supplies deterministic UI transport

The v0006 E2E harness SHALL provide a test-only mocked proxy as the Web UI's
single HTTP and WebSocket origin. It SHALL expose the documented UI adapter
contract, forward designated integration routes to the real State Registry,
and serve deterministic fixtures for isolated presentation tests. It SHALL NOT
be shipped as a production API Gateway, authenticate users, inspect Keycloak
groups, derive memberships, authorize team access, or own canonical platform
state.

#### Scenario: Browser uses the mocked proxy

- **WHEN** a v0006 browser test performs a backend request or opens a live subscription
- **THEN** the browser contacts only the mocked-proxy origin and never directly contacts State Registry or an Executor

### Requirement: Mocked proxy supplies an externally prepared team list

The mocked proxy SHALL implement
`specs/mocked-proxy/openapi/ui-adapter.openapi.yaml` and expose
`GET /ui/v1/teams` returning the deterministic
list of `team_id` and `team_name` pairs prepared by the test. It SHALL preserve
the list and identifiers exactly. It SHALL NOT discover Keycloak groups,
translate a group into a team, query user membership, or claim that the list is
authorized. An empty list SHALL be valid.

#### Scenario: Test supplies multiple teams

- **WHEN** the fixture configures two distinct teams
- **THEN** `GET /ui/v1/teams` returns both exact identifiers and display names without selecting or authorizing either one

### Requirement: Mocked proxy forwards the selected team identifier

Every team-scoped UI adapter REST operation SHALL carry `team_id` as the
required query or body field defined by the v0006 UI API contract. Every live
subscription SHALL carry `team_id` in its subscription request. The mocked
proxy SHALL forward the currently supplied value to its real State Registry
adapter and SHALL NOT replace it with the first configured team, cached team,
or deployment default. It SHALL cancel the old upstream subscription when the
browser changes teams.

#### Scenario: Operator switches teams

- **WHEN** the browser changes from `team-alpha` to `team-beta`
- **THEN** all later forwarded reads and writes contain `team-beta`, the old live subscription is closed, and the replacement subscription receives only `team-beta` frames

### Requirement: Mocked proxy remains non-authoritative

For real-integration routes, the mocked proxy SHALL return State Registry
status, headers, and body without storing tasks, Executors, environments,
secrets, revisions, controls, logs, or audit entries itself. Test fixtures MAY
serve deterministic isolated UI data only when the test explicitly declares
fixture mode.

#### Scenario: UI mutates launch parameters in integration mode

- **WHEN** the browser creates, updates, or deletes launch parameters or secrets through the mocked proxy
- **THEN** the mutation is performed by State Registry and direct Registry verification observes the canonical result

### Requirement: Mocked proxy excludes administration and ingestion

The mocked proxy SHALL expose no `/admin/*` route and no listener task-ingestion
route to the browser. Tests SHALL create teams, source systems, task types, and
tasks through their existing authorized setup surfaces outside the UI journey.

#### Scenario: Browser attempts an excluded route

- **WHEN** browser traffic targets an admin or task-ingestion operation
- **THEN** the mocked proxy rejects it without forwarding a State Registry request
