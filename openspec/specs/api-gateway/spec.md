# api-gateway Specification

## Purpose

Provide the Web UI's sole authenticated backend, resolving provider-neutral OIDC identities and canonical teams, issuing one-team working credentials, containing browser credentials, and proxying trusted team-scoped REST and WebSocket traffic to State Registry without assuming platform business ownership.

## Requirements

### Requirement: Gateway requires authenticated proxy mode

The API Gateway SHALL disable the configured-team no-auth operator proxy mode from v0006, SHALL authenticate users with a configured OIDC provider, SHALL resolve validated OIDC team memberships through its own database, and SHALL require a valid one-team working bearer on every protected Web UI REST request and WebSocket upgrade. It SHALL establish exactly one canonical active operator/team context and reject the request before proxying when authentication or team context is unusable.

#### Scenario: Authenticated proxy request

- **WHEN** a Web UI request carries a valid bearer token with canonical current operator and team claims
- **THEN** the Gateway may create a team-scoped State Registry child request

#### Scenario: Missing bearer token

- **WHEN** a protected Web UI request has no valid bearer token
- **THEN** the Gateway rejects it before creating any backend request or subscription

### Requirement: Gateway establishes trusted canonical-team context

For every authenticated team-scoped REST request and WebSocket subscription, the API Gateway SHALL remove all client-supplied OIDC team and internal identity/context headers, validate the working bearer's canonical `operator_id` and exactly one immutable active `team_id` against a fresh Gateway-owned membership resolution, and inject `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, and a Gateway-generated `X-FlowAI-Request-ID` into the State Registry child request. It SHALL inject display-only `X-FlowAI-Team-Name` only when canonical `team_name` is present and SHALL never use it for authorization.

#### Scenario: Browser spoofs authenticated context headers

- **WHEN** a validly authenticated browser supplies forged operator, team, team-name, or request-ID headers
- **THEN** the Gateway discards them and State Registry receives only canonical token/auth-state operator and team context plus a Gateway-generated request ID

#### Scenario: Authenticated WebSocket subscription is created

- **WHEN** the Gateway accepts a protected WebSocket upgrade
- **THEN** it establishes the same canonical operator/team/request context on the State Registry child subscription as on REST child requests

### Requirement: State Registry remains authoritative for resource ownership

The API Gateway SHALL pass canonical authenticated `operator_id`, stable `team_id`, optional display-only `team_name`, and request ID to State Registry but SHALL NOT decide whether a resource belongs to that team. State Registry SHALL remain authoritative for task, event, control, environment, and secret ownership and SHALL use stable `team_id`, never `team_name`, for isolation.

#### Scenario: Authenticated team requests another team's resource

- **WHEN** a valid token-bound team requests a resource owned by a different `team_id`
- **THEN** State Registry denies access using resource ownership and the Gateway returns that denial unchanged

### Requirement: Gateway remains business-logic-free

The API Gateway SHALL return State Registry REST response status, headers, and body and WebSocket frames without platform-state mutation, aggregation, task validation, queueing, caching, business-policy enforcement, or response transformation. Authentication validation, bearer containment, spoofed-header removal, trusted security-context injection, request-ID generation, and allowed-target enforcement SHALL be transport security behavior and SHALL NOT be considered platform business logic.

#### Scenario: Authenticated State Registry response returns

- **WHEN** State Registry returns a success or ownership denial after evaluating canonical team context
- **THEN** the Gateway forwards the response status, headers, and body unchanged and adds no platform fields

### Requirement: Gateway rejects unusable authenticated team context

The API Gateway SHALL reject a protected REST request or WebSocket upgrade before proxying when `operator_id` or `team_id` is missing, when team identity is duplicated, when the token team is not resolved from the operator's current canonical OIDC teams in the Gateway database, or when the OIDC team membership or Gateway-owned team mapping is inactive or stale.

#### Scenario: Team claim is missing or ambiguous

- **WHEN** a working bearer lacks `team_id`, represents more than one team identity, or names a team absent from fresh OIDC team resolution
- **THEN** the Gateway rejects it before State Registry receives a child request

#### Scenario: Team claim is stale

- **WHEN** the token's immutable `team_id` no longer appears in the fresh Gateway-owned resolution of canonical OIDC teams
- **THEN** the Gateway rejects both REST and WebSocket access before proxying

### Requirement: Gateway contains browser bearer credentials

The API Gateway SHALL remove the browser `Authorization` header and SHALL NOT forward the bearer token, refresh token, login credential, cookie credential, or equivalent browser authentication secret to State Registry on REST or WebSocket child requests.

#### Scenario: Valid browser bearer is consumed

- **WHEN** the Gateway validates a browser bearer token and creates a State Registry child request
- **THEN** State Registry receives trusted identity context headers and no browser credential

### Requirement: Gateway obtains canonical OIDC teams from OIDC provider

The API Gateway SHALL implement the provider-neutral OIDC authorization-code flow with PKCE, SHALL use OIDC discovery and the provider's published signing keys, and SHALL validate authorization response state and nonce, token issuer, allowed asymmetric algorithm, signature, audience, expiry, and bounded clock skew. Team-claim configuration SHALL select exactly one source from `id_token` or `userinfo`, with no fallback, and SHALL contain a required RFC 6901 `claim_pointer`. The `string_array` adapter SHALL require an array of non-empty strings. The `object_array` adapter SHALL require an array of objects and a configured `id_field` containing a non-empty string in every object; optional `name_field` SHALL be display-only. Missing, `null`, wrong-type, or partly malformed claims SHALL fail authentication closed; a valid empty array SHALL produce zero accessible teams. Gateway SHALL trim and deduplicate non-empty configured `additional_scopes`, require `openid`, reject invalid configuration at startup, and SHALL NOT let adapters add scopes implicitly. It SHALL derive canonical `operator_id` from validated `iss` and `sub`, deduplicate normalized team identifiers, ignore browser assertions, and send no provider membership data to State Registry.

#### Scenario: Generic OIDC array claim is normalized

- **WHEN** configured `claim_pointer` resolves in the selected validated ID Token or UserInfo response to a string array
- **THEN** the Gateway deduplicates its non-empty opaque values and resolves only those normalized OIDC team identifiers in its own database

#### Scenario: Generic object-array claim is normalized

- **WHEN** configured `claim_pointer` resolves to an object array whose every element carries a non-empty string at configured `id_field`
- **THEN** the `object_array` adapter resolves only those identifiers in its own database and never authorizes by configured `name_field` or any other object field

#### Scenario: Team-claim adapter or claim shape is unusable

- **WHEN** adapter configuration is absent or unsupported at startup, or the validated identity contains a configured claim with the wrong shape
- **THEN** the Gateway fails startup or authentication respectively and performs no membership lookup or State Registry child request

#### Scenario: Browser forges OIDC team membership

- **WHEN** the browser supplies OIDC team or platform-team values that differ from validated OIDC identity state
- **THEN** the Gateway ignores the browser values and resolves only provider-validated OIDC team identifiers in its own database

#### Scenario: OIDC authorization response or ID Token is invalid

- **WHEN** authorization response `state` or ID Token `nonce`, issuer, algorithm, signature, audience, expiry, or bounded clock-skew validation fails
- **THEN** the Gateway returns `401 invalid_oidc_identity`, creates no browser session or derived access, and performs no State Registry request

#### Scenario: OIDC endpoint is unavailable

- **WHEN** a required discovery, token, JWKS, or UserInfo request fails or exceeds configured positive `endpoint_timeout`
- **THEN** the Gateway fails startup or the current authentication operation closed as applicable, returns no token, creates no State Registry child request, and exposes `502 oidc_provider_unavailable` rather than provider internals

### Requirement: Gateway enforces deterministic membership freshness

The API Gateway SHALL record each successful membership observation time, refresh membership at login, and refresh before `POST /auth/v1/token` whenever the observation age exceeds configured non-negative `membership_max_age`. A zero value SHALL require refresh for every token issue. When a required refresh fails, the Gateway SHALL return `502 oidc_provider_unavailable`, SHALL NOT use stale membership, and SHALL NOT issue a token. Protected REST requests and WebSocket upgrades SHALL validate the token team against active Gateway-owned derived access and mapping state.

#### Scenario: Required membership refresh fails

- **WHEN** stored membership is stale and the configured provider cannot supply a fresh observation
- **THEN** token issue fails closed and neither stale membership nor an old mapping authorizes a new token

### Requirement: Gateway owns OIDC-to-canonical-team mappings

The API Gateway SHALL own a database mapping `(issuer, oidc_team_id)` to an immutable State Registry-owned `team_id` and optional canonical display-only `team_name`. It SHALL accept mappings only for its one configured operator OIDC issuer and SHALL return `400 invalid_request` for any other issuer before reading State Registry or Gateway persistence. Before creating an accepted mapping, it SHALL read the canonical team through the State Registry admin API. `POST /admin/v1/oidc-team-mappings` SHALL return `404 canonical_team_not_found` when that API reports no team and `503 state_registry_unavailable` when the dependency is unavailable or times out. It SHALL atomically enforce one mapping per `(issuer, oidc_team_id)` and one mapping per canonical `team_id`, return `201` for creation, return `200` with the same representation for an identical retry, and return `409 mapping_conflict` for conflicting external or canonical ownership without partial persistence. After successful OIDC authentication, Gateway SHALL resolve only known membership through this mapping, persist derived `(operator_id, team_id)` access with observation time, refresh canonical `team_name` and `archived_at` from State Registry for `GET /auth/v1/teams`, and return a deterministic `{team_id, team_name?, archived_at?}` list ordered by `(team_name ASC NULLS LAST, team_id ASC)`. If that canonical presentation read is unavailable, it SHALL return `503 state_registry_unavailable` rather than stale display state. Archived canonical teams SHALL remain mapped, visible, selectable, and eligible for token issue; archival status is display and task-ingestion policy, not operator membership revocation. Unknown well-formed OIDC identifiers SHALL be omitted and SHALL NOT be auto-created. State Registry SHALL receive no OIDC identifier or operator-membership record.

For Gateway calls in this requirement, the canonical-team read SHALL be credential-free State Registry `GET /internal/v1/teams/{team_id}` over the deployment-admitted backend network, and Gateway SHALL send no `Authorization` or replacement service credential; the phrase State Registry admin API names the administrative capability, not the admin-bearer route.

#### Scenario: User belongs to several mapped OIDC teams

- **WHEN** the Gateway database resolves the user's canonical OIDC teams to several active teams
- **THEN** the Gateway returns all resolved teams to Web UI and issues a one-team working bearer only for a selected entry from that result

#### Scenario: Browser selects an unresolved team

- **WHEN** the browser requests a working bearer for a `team_id` absent from the fresh Gateway-owned resolution
- **THEN** the Gateway rejects the selection and issues no working bearer

#### Scenario: Administrator registers an external team mapping

- **WHEN** an authenticated system administrator maps one configured issuer and stable OIDC team identifier to an existing canonical `team_id`
- **THEN** API Gateway stores the mapping in its own database and does not write identity-provider or membership data to State Registry

#### Scenario: Administrator supplies another issuer

- **WHEN** mapping registration names an issuer other than the configured operator OIDC issuer
- **THEN** Gateway returns `400 invalid_request` before any mapping read/write or State Registry request

#### Scenario: Mapping references an unavailable canonical team

- **WHEN** State Registry reports that the requested canonical team does not exist or cannot be reached before `endpoint_timeout`
- **THEN** Gateway returns `404 canonical_team_not_found` or `503 state_registry_unavailable` respectively and stores no mapping

#### Scenario: Mapping or access references an archived canonical team

- **WHEN** mapping registration, team listing, token issue, protected REST, or WebSocket access resolves to a canonical team whose State Registry record is archived
- **THEN** Gateway preserves the mapping, returns the team with `archived_at`, may issue its one-team token, and preserves authenticated REST and WebSocket access while State Registry enforces the prohibition on new task ingestion

#### Scenario: Concurrent mapping requests conflict

- **WHEN** identical or conflicting mapping-registration requests execute concurrently
- **THEN** exactly one logical mapping is stored, identical retries return the same mapping, conflicts return `409`, and no partial row is visible

### Requirement: Gateway exposes versioned auth and mapping APIs

The API Gateway SHALL expose `GET /auth/v1/login`, `GET /auth/v1/callback`, `GET /auth/v1/teams`, `POST /auth/v1/token`, `DELETE /auth/v1/session`, and `POST /admin/v1/oidc-team-mappings` according to `specs/api-gateway/openapi/auth.openapi.yaml`. The callback SHALL accept exactly one of a successful `code` or standard provider `error` result together with matching `state`; provider `error_description` SHALL be treated as untrusted and SHALL NOT be returned. Error responses SHALL contain one OpenAPI-enumerated stable `code`, safe `message`, and `request_id`; validation errors SHALL use `400`, unauthenticated requests `401`, unauthorized authenticated requests `403`, absent canonical teams `404`, mapping conflicts `409`, contacted OIDC/admin-JWKS outages `502`, and State Registry outages `503`.

#### Scenario: API error is returned

- **WHEN** a versioned auth or mapping request fails validation, authentication, authorization, conflict, or provider availability checks
- **THEN** Gateway returns the specified status and stable error envelope without stack traces, credentials, claims, or provider response bodies

### Requirement: Gateway authenticates mapping administrators without provider coupling

For `POST /admin/v1/oidc-team-mappings`, the API Gateway SHALL validate a separately configured admin JWT issuer, audience, asymmetric algorithm allowlist, signature, `iat`, expiry, bounded clock skew, and JWKS; resolve a configured RFC 6901 role-claim pointer; and require exact configured role value `flowai-system-admin`. It SHALL require positive `admin_jwks_timeout` and `admin_token_max_age` and SHALL reject invalid trust configuration at startup. A FlowAI system administrator SHALL be provisioned only by assigning this exact role to an identity in the configured external admin identity provider; Gateway and State Registry SHALL expose no local administrator-create, role-assignment, or administrator-membership store. Removing the external role SHALL prevent authorization by tokens issued after removal; a token issued before removal remains usable only until the earlier of its `exp` or configured `admin_token_max_age`. An unavailable or timed-out admin JWKS refresh SHALL return `502 admin_identity_provider_unavailable` without falling back to a key for an unknown `kid`. A Gateway operator working token SHALL NOT satisfy the admin audience. Missing or invalid authentication SHALL return `401 invalid_admin_token`; an authenticated identity without the required role SHALL return `403 insufficient_admin_role` before reading a mapping or calling State Registry.

#### Scenario: Operator token calls mapping administration

- **WHEN** a valid operator working token or an admin JWT without `flowai-system-admin` calls the mapping-registration API
- **THEN** Gateway rejects it before any mapping read or write and before any State Registry request

#### Scenario: Admin signing key cannot be refreshed

- **WHEN** an admin JWT carries unknown `kid` and the separately configured admin JWKS endpoint fails or exceeds `admin_jwks_timeout`
- **THEN** Gateway returns `502 admin_identity_provider_unavailable` and performs no mapping read/write or State Registry request

#### Scenario: External administrator role is assigned or removed

- **WHEN** the configured admin identity provider issues an otherwise valid admin JWT after assigning or removing exact role `flowai-system-admin`
- **THEN** Gateway authorizes newly issued tokens only while the exact role is present, bounds every older token by `exp` and `admin_token_max_age`, and persists no local administrator account or role assignment

### Requirement: Gateway isolates its persistence from State Registry

The API Gateway SHALL own its auth sessions, OIDC-to-canonical-team mappings, derived operator-team access, auth audit, schema, database role, migrations, and connection configuration. It MAY use the same PostgreSQL server instance as State Registry, but SHALL NOT share tables, a schema owner, migration history, or write privileges with State Registry and SHALL NOT read or write State Registry tables directly.

#### Scenario: Services share one PostgreSQL instance

- **WHEN** API Gateway and State Registry are configured on the same PostgreSQL server
- **THEN** each service migrates and accesses only its own schema through its own least-privilege database role

### Requirement: Gateway provides canonical audit attribution context

For every accepted operator mutation, the API Gateway SHALL provide State Registry with canonical `operator_id`, stable `team_id`, and Gateway-generated request ID so State Registry can persist the canonical platform audit record. The optional `team_name` SHALL be display-only and SHALL NOT determine audit ownership.

#### Scenario: Authenticated operator creates a control request

- **WHEN** an authenticated operator submits a control request for a task owned by the token's `team_id`
- **THEN** State Registry records the mutation and audit attribution using canonical operator, stable team, and request identifiers supplied by the Gateway
