## ADDED Requirements

### Requirement: Gateway resolves Keycloak groups before issuing operator-team bearer tokens

The API Gateway SHALL authenticate the user with Keycloak, validate the Keycloak issuer, signature, audience, expiry, and authorization flow state, and obtain canonical user group identifiers only from validated Keycloak token claims or UserInfo. It SHALL send those group identifiers to the trusted State Registry team-resolution API and SHALL return the resolved canonical team list to Web UI. After the user selects one resolved team, the Gateway SHALL issue a working bearer token containing one canonical `operator_id`, exactly one non-empty immutable `team_id`, the canonical display-only `team_name`, an intended API Gateway audience, and an expiry. The Gateway SHALL NOT trust group, team, or operator values supplied by the browser.

#### Scenario: Operator logs in with Keycloak groups

- **WHEN** Keycloak authenticates an operator and returns canonical groups that State Registry maps to one or more teams
- **THEN** the Gateway returns the deterministic resolved `{team_id, team_name}` list and issues a one-team working bearer only for a team selected from that list

#### Scenario: Keycloak groups resolve to no teams

- **WHEN** valid Keycloak identity has no group mapped to an active State Registry team
- **THEN** the Gateway returns an empty accessible-team list and issues no working bearer token

### Requirement: Gateway validates operator and team claims

The API Gateway SHALL validate working-bearer signature and issuer, intended audience, expiry, canonical `operator_id`, exactly one non-empty `team_id`, canonical `team_name`, and current Keycloak-group-to-State-Registry-team access on every protected REST request and WebSocket upgrade. It SHALL reject a missing, duplicated, invalid, expired, unmapped, unauthorized, or stale claim set before proxying.

#### Scenario: Token team assignment is stale

- **WHEN** a validly signed working token's `team_id` is no longer present in a fresh resolution of the operator's canonical Keycloak groups
- **THEN** the Gateway rejects the request before creating a State Registry child request

#### Scenario: Token has wrong audience or is expired

- **WHEN** a bearer token has a non-Gateway audience or an expired timestamp
- **THEN** the Gateway rejects the request before proxying or accepting a WebSocket upgrade

### Requirement: Each working token has one immutable active team

An authenticated working token SHALL have exactly one active `team_id` for its lifetime. The Gateway MAY accept a browser selection only when the selected `team_id` appears in the fresh State Registry result for the user's canonical Keycloak groups. It SHALL NOT silently change `team_id` during refresh or represent multiple active teams in one working token. Selecting another accessible team SHALL require a newly issued working token.

#### Scenario: Browser attempts to switch team during refresh

- **WHEN** a refresh request supplies a different team or the token team no longer appears in fresh group-to-team resolution
- **THEN** the Gateway rejects the refresh rather than changing the existing token's `team_id`

### Requirement: Auth remains gateway-local

The API Gateway SHALL NOT use State Registry as a credential or session store. It SHALL consume browser and Keycloak credentials at the Gateway boundary, SHALL send only canonical operator and group identifiers to the trusted team-resolution API, SHALL NOT send credentials to State Registry, and MAY write Gateway auth-audit data only to separate auth logs.

#### Scenario: Authenticated request is proxied

- **WHEN** the Gateway accepts a bearer-authenticated request
- **THEN** State Registry receives trusted downstream context but no browser `Authorization` header or bearer credential

#### Scenario: Auth audit is enabled

- **WHEN** auth-audit logging is enabled
- **THEN** the Gateway writes only authentication audit data to its own auth log and not to canonical platform state
