## ADDED Requirements

### Requirement: Gateway resolves OIDC teams in its own database before issuing operator-team bearer tokens

The API Gateway SHALL authenticate the user with a configured OIDC provider, validate the provider issuer, signature, audience, expiry, and authorization flow state, derive canonical `operator_id` from validated `iss` and `sub`, and normalize OIDC team identifiers through the configured provider-neutral `string_array` or `object_array` team-claim adapter. It SHALL resolve known identifiers through its own service-owned mapping database, omit unknown well-formed identifiers without failing the login, and return the resolved canonical team list to Web UI. After the user selects one resolved team, the Gateway SHALL issue a working bearer token containing `kid`, `iss`, `aud`, `sub`, canonical `operator_id`, exactly one non-empty immutable `team_id`, optional canonical display-only `team_name`, `iat`, `exp`, and unique `jti`. The Gateway SHALL NOT trust identity or team values supplied by the browser, SHALL NOT send OIDC membership to State Registry, and SHALL NOT auto-create a FlowAI team or mapping from an unknown provider value.

#### Scenario: Operator logs in with OIDC teams

- **WHEN** OIDC provider authenticates an operator and returns canonical OIDC teams that the Gateway database maps to one or more canonical teams
- **THEN** the Gateway returns the deterministic resolved `{team_id, team_name}` list and issues a one-team working bearer only for a team selected from that list

#### Scenario: OIDC teams resolve to no teams

- **WHEN** a valid OIDC identity has no OIDC team mapped to an active canonical team in the Gateway database
- **THEN** the Gateway returns an empty accessible-team list and issues no working bearer token

#### Scenario: Only some OIDC teams are mapped

- **WHEN** a valid OIDC identity contains mapped and unknown well-formed OIDC team identifiers
- **THEN** the Gateway returns only mapped canonical teams, permits token issue for each returned team, and neither returns nor auto-creates the unknown identifiers

### Requirement: Gateway validates operator and team claims

The API Gateway SHALL validate the configured asymmetric algorithm allowlist, known `kid`, signature and issuer, intended audience, bounded clock skew, expiry, canonical `operator_id`, exactly one non-empty `team_id`, optional display-only `team_name`, and current OIDC-to-canonical-team access in its own database on every protected REST request and WebSocket upgrade. It SHALL reject a missing, duplicated, invalid, expired, unmapped, unauthorized, or stale claim set before proxying. Archival SHALL NOT revoke mapped membership or block authenticated REST reads, WebSocket reads, or token issue for the archived team. A WebSocket connection SHALL close with application close code `4401` and reason `working_token_expired` when its working token reaches `exp`; reconnect SHALL perform a new complete upgrade validation.

#### Scenario: Token team assignment is stale

- **WHEN** a validly signed working token's `team_id` is no longer present in a fresh Gateway-owned resolution of the operator's canonical OIDC teams
- **THEN** the Gateway rejects the request before creating a State Registry child request

#### Scenario: Token has wrong audience or is expired

- **WHEN** a bearer token has a non-Gateway audience or an expired timestamp
- **THEN** the Gateway rejects the request before proxying or accepting a WebSocket upgrade

#### Scenario: Token signature context is invalid

- **WHEN** a bearer token has a wrong issuer, disallowed algorithm, invalid signature, or unknown `kid`
- **THEN** the Gateway returns `401 invalid_working_token` and creates no State Registry child request

#### Scenario: Token team is archived

- **WHEN** a valid token names a mapped canonical team whose State Registry record is archived
- **THEN** Gateway preserves authenticated REST and WebSocket read access and State Registry independently rejects only new task ingestion for that team

### Requirement: Each working token has one immutable active team

An authenticated working token SHALL have exactly one active `team_id` for its lifetime. `POST /auth/v1/token` SHALL accept one selected `team_id` only when it appears in the fresh-enough mapping result for the current server-side OIDC session and SHALL always issue a new working token with a new `jti`, including when renewing the same team. It SHALL NOT mutate an existing token, represent multiple active teams, expose an OIDC token, or issue a separate FlowAI refresh token. Selecting another accessible team SHALL require another newly issued working token.

#### Scenario: Browser renews or switches the selected team

- **WHEN** the browser requests another token for the same accessible team or a different accessible team
- **THEN** the Gateway issues a distinct token with a new `jti` and the requested one-team context without changing any existing token

#### Scenario: Browser requests a stale team during token renewal

- **WHEN** the requested team no longer appears in the fresh-enough OIDC-team-to-platform-team resolution
- **THEN** the Gateway returns `403 team_not_accessible` and issues no token

### Requirement: Gateway owns secure browser session lifecycle

The API Gateway SHALL keep OIDC access and refresh credentials only in its service-owned encrypted session state, identify the browser session with a `Secure`, `HttpOnly`, `SameSite=Lax` cookie, and SHALL NOT expose provider credentials to browser JavaScript. Web UI SHALL keep a working bearer only in memory. `DELETE /auth/v1/session` SHALL delete the server-side session and expire the cookie; a deleted or expired session SHALL NOT list teams or issue another working token. Existing working tokens SHALL remain limited by their short `exp` and the Gateway's current access check.

#### Scenario: Operator logs out

- **WHEN** the browser deletes its authenticated session through `DELETE /auth/v1/session`
- **THEN** the Gateway removes the server-side session, expires the cookie, and rejects subsequent team-list and token-issue requests for that session

### Requirement: Auth remains gateway-local

The API Gateway SHALL NOT use State Registry as a credential, session, membership, or OIDC-team-mapping store. It SHALL consume browser and OIDC credentials at the Gateway boundary, persist only required auth session and derived membership state in its own database, SHALL send no OIDC identifiers or credentials to State Registry, and MAY write Gateway auth-audit data only to its own database or separate auth logs.

#### Scenario: Authenticated request is proxied

- **WHEN** the Gateway accepts a bearer-authenticated request
- **THEN** State Registry receives trusted downstream context but no browser `Authorization` header or bearer credential

#### Scenario: Auth audit is enabled

- **WHEN** auth-audit logging is enabled
- **THEN** the Gateway writes only authentication audit data to its own auth log and not to canonical platform state
