## ADDED Requirements

### Requirement: Gateway issues canonical operator-team bearer tokens

The API Gateway SHALL issue bearer tokens at login or refresh from canonical authentication state. Each token SHALL contain one canonical `operator_id`, exactly one non-empty immutable `team_id`, an optional canonical `team_name`, an intended API Gateway audience, and an expiry. `team_name` SHALL be display-only and SHALL NOT authorize access.

#### Scenario: Operator logs in with one active team

- **WHEN** valid credentials resolve to one canonical operator and exactly one active team
- **THEN** the Gateway returns a bearer token containing that `operator_id`, immutable `team_id`, optional canonical display-only `team_name`, intended audience, and expiry

#### Scenario: Login has ambiguous team assignment

- **WHEN** valid credentials resolve to no active team or more than one active team
- **THEN** the Gateway rejects login without issuing a bearer token

### Requirement: Gateway validates operator and team claims

The API Gateway SHALL validate bearer signature and issuer, intended audience, expiry, canonical `operator_id`, exactly one non-empty `team_id`, optional canonical `team_name`, and active/non-stale team assignment on every protected REST request and WebSocket upgrade. It SHALL reject a missing, duplicated, ambiguous, invalid, expired, or stale claim set before proxying.

#### Scenario: Token team assignment is stale

- **WHEN** a validly signed token's `team_id` is no longer the operator's active canonical team assignment
- **THEN** the Gateway rejects the request before creating a State Registry child request

#### Scenario: Token has wrong audience or is expired

- **WHEN** a bearer token has a non-Gateway audience or an expired timestamp
- **THEN** the Gateway rejects the request before proxying or accepting a WebSocket upgrade

### Requirement: Auth session has one immutable active team

An authenticated Web UI token/session SHALL have exactly one active `team_id` for its lifetime. The Gateway SHALL NOT accept a browser-selected team, silently change `team_id` during refresh, or represent multiple active teams in one token/session. A canonical assignment change SHALL require a new authenticated session.

#### Scenario: Browser attempts to switch team during refresh

- **WHEN** a refresh request supplies a different team or the canonical team assignment changed since token issuance
- **THEN** the Gateway rejects the refresh rather than changing the existing session's `team_id`

### Requirement: Auth remains gateway-local

The API Gateway SHALL NOT use State Registry as a credential or session store. It SHALL consume browser bearer credentials at the Gateway boundary, SHALL NOT send them to State Registry, and MAY write Gateway auth-audit data only to separate auth logs.

#### Scenario: Authenticated request is proxied

- **WHEN** the Gateway accepts a bearer-authenticated request
- **THEN** State Registry receives trusted downstream context but no browser `Authorization` header or bearer credential

#### Scenario: Auth audit is enabled

- **WHEN** auth-audit logging is enabled
- **THEN** the Gateway writes only authentication audit data to its own auth log and not to canonical platform state
