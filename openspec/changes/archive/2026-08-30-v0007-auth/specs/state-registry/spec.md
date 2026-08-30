## MODIFIED Requirements

### Requirement: State Registry owns team registration through `/admin/teams`

The State Registry SHALL expose admin-bearer-only `GET /admin/teams/{team_id}` and credential-free, deployment-network-admitted `GET /internal/v1/teams/{team_id}` as distinct point reads of one canonical team, plus `POST /admin/teams` to create, `PATCH /admin/teams/{team_id}` to update display-only `team_name` and required `default_image`, and `POST /admin/teams/{team_id}/archive` to archive. Create, admin point read, update, and archive SHALL accept only an authenticated system-administrator identity. The internal point read SHALL accept only the existing network-admitted API Gateway backend path, SHALL require no replacement Gateway service credential, and SHALL expose no collection scan, preserving the accepted v0009 backend transport model. No route SHALL fall back from an invalid admin bearer to the credential-free internal branch. The State Registry SHALL persist a generated immutable `team_id`, a unique current display-only `team_name`, a required opaque digest-bearing `default_image`, an `ingested_at` timestamp, and nullable `archived_at`. Update SHALL never change `team_id`, clear `default_image`, or accept identity-provider fields. A successful rename SHALL release the previous `team_name` after commit so another create or update may use it; immutable audit history SHALL retain the old value without reserving it. Archive SHALL be idempotent, SHALL set `archived_at` once, and SHALL preserve the team row, all owned resources, immutable ownership references, history, and audit. An archived team SHALL remain available through authenticated operator REST and WebSocket reads and SHALL remain eligible for Gateway mapping, team listing, and token issue, but State Registry SHALL reject every authenticated source-input task ingestion through `POST /v1/tasks` for that team before persistence or acknowledgement. Archive and task ingestion SHALL serialize on the canonical team row in their transactions: an ingestion committed before archive may persist and continue normally, while an archive committed first causes the racing ingestion to reject; no new task row may commit after the team's `archived_at` becomes visible. Existing pending tasks SHALL remain discoverable and claimable, claimed tasks SHALL continue through allowed lifecycle events, and terminal tasks SHALL remain readable without reopening. State Registry SHALL expose no team unarchive or physical-delete endpoint and SHALL never reuse an archived `team_id`. It SHALL NOT store operator membership, OIDC membership, OIDC subject, issuer, or OIDC-team mapping.

#### Scenario: Administrator creates a canonical team

- **WHEN** an authenticated system administrator submits a unique `team_name` and a valid required `default_image`
- **THEN** State Registry creates and returns one active canonical team with immutable `team_id`, null `archived_at`, and no identity-provider mapping fields

#### Scenario: Administrator or network-admitted Gateway reads one canonical team

- **WHEN** an authenticated system administrator calls `GET /admin/teams/{team_id}` or API Gateway calls `GET /internal/v1/teams/{team_id}` over the deployment-admitted backend path without a service bearer for an existing active or archived team
- **THEN** State Registry returns exactly its current canonical `team_id`, `team_name`, `default_image`, `ingested_at`, and `archived_at` without exposing identity-provider membership, requiring replacement service authentication, or permitting collection enumeration

#### Scenario: Canonical team point read has invalid admin credentials, is missing, or is unavailable

- **WHEN** a caller presents invalid admin credentials, the requested `team_id` is missing, or the Registry dependency cannot complete the read
- **THEN** State Registry returns `401` or `403`, `404 canonical_team_not_found`, or a safe availability failure respectively without leaking another team or stale state

#### Scenario: Non-admin caller or invalid team input is rejected

- **WHEN** a non-admin calls the endpoint or an administrator submits a duplicate or missing `team_name` or missing `default_image`
- **THEN** State Registry rejects the request without creating or changing a team

#### Scenario: State Registry receives identity-provider mapping data

- **WHEN** any caller submits OIDC issuer, subject, team identifier, or operator-membership data to a State Registry team or operator endpoint
- **THEN** State Registry rejects the request with HTTP 400 before mutation and persists no team, OIDC identifier, operator membership, or membership mapping from that request

#### Scenario: Administrator updates mutable team metadata

- **WHEN** an authenticated system administrator changes `team_name` or `default_image` for an active or archived team through `PATCH /admin/teams/{team_id}`
- **THEN** State Registry atomically stores and returns the changed fields while preserving `team_id`, ownership references, resources, and history

#### Scenario: Team update attempts to change immutable or forbidden state

- **WHEN** any caller attempts to change `team_id`, clear `default_image`, reuse an existing active or archived `team_name`, submit identity-provider fields, or update a missing team
- **THEN** State Registry rejects the complete request without partial mutation

#### Scenario: Team-name writers race for one unique value

- **WHEN** concurrent create/create, create/update, or update/update operations for different teams attempt to commit the same `team_name`
- **THEN** exactly one operation commits that unique name, every loser returns `409 team_conflict`, and no team row contains a partial update or duplicate name

#### Scenario: Successful rename releases the previous team name

- **WHEN** an active or archived team successfully changes from one unique `team_name` to another and a later create or update requests the released old name
- **THEN** the later operation may commit the old name as its current unique value while audit history retains the first team's rename

#### Scenario: Administrator archives a team

- **WHEN** an authenticated system administrator calls `POST /admin/teams/{team_id}/archive` for an active team
- **THEN** State Registry sets `archived_at` once, preserves the row and every owned resource, keeps authenticated operator reads and existing task lifecycle available, and rejects subsequent ingestion of a new task for that team

#### Scenario: Team archive is retried or deletion is attempted

- **WHEN** the same archive request is retried or any caller attempts to unarchive or physically delete a team
- **THEN** archive returns the same canonical archived representation and no unarchive or delete operation is available

#### Scenario: Source input ingests a task for an archived team

- **WHEN** an authenticated source-input listener calls `POST /v1/tasks` with a new task whose canonical team is archived
- **THEN** State Registry returns `409 team_archived` before persistence and acknowledgement while existing team resources remain readable

#### Scenario: Source input retries an existing task for an archived team

- **WHEN** an authenticated source-input listener repeats `POST /v1/tasks` with a `(team_id, source_system_id, source_id)` already persisted before that team was archived
- **THEN** State Registry returns `409 team_archived` before dedupe lookup or acknowledgement and leaves the existing canonical task unchanged

#### Scenario: Archive races with source-input ingestion

- **WHEN** `POST /admin/teams/{team_id}/archive` and a previously unseen source-input `POST /v1/tasks` for the same active team execute concurrently
- **THEN** their transactions produce one linearizable order: ingestion committed first persists exactly one task that remains processable, or archive committed first rejects ingestion, and no task commits after visible `archived_at`

#### Scenario: Existing tasks continue after archive

- **WHEN** a team is archived while it has separate pending, claimed, and terminal tasks
- **THEN** the pending task remains claimable, the claimed task accepts its remaining valid lifecycle events, and the terminal task remains readable without any task being duplicated, reset, or reopened

#### Scenario: Archive races with claim of an existing pending task

- **WHEN** archive and a valid FIFO claim of a task already pending for that team execute concurrently
- **THEN** claim succeeds in either serial order, archive persists `archived_at`, exactly one Executor assignment and first `created` event commit, image resolution uses the canonical values visible to the claim transaction, and neither operation deadlocks or rolls back the other

### Requirement: State Registry requires a mutable team default image

State Registry SHALL require a provider-neutral OCI image reference consisting of a non-empty repository reference containing neither whitespace nor `@`, exactly one `@`, a digest algorithm matching `[A-Za-z][A-Za-z0-9]*(?:[+._-][A-Za-z][A-Za-z0-9]*)*`, `:`, and a non-empty encoded digest matching `[A-Za-z0-9=_-]+` when a team is created and whenever `default_image` is updated. It SHALL apply the same validation in its HTTP schema and server-side request validation, SHALL NOT restrict the contract to one digest algorithm, and SHALL reject tag-only references. An authenticated system administrator MAY replace an active or archived team's `default_image`; operators and all non-admin identities SHALL NOT edit or clear it. The changed value SHALL apply only to tasks claimed after the update whose image resolution reaches team-default precedence; already claimed tasks SHALL retain their persisted `resolved_image` and `image_source`.

#### Scenario: Administrator changes the team default image

- **WHEN** an authenticated system administrator replaces an active or archived team's valid digest-bearing `default_image`
- **THEN** later eligible claims using team-default precedence resolve the new image while already claimed tasks retain their persisted resolution

#### Scenario: Default-image update races with task claim

- **WHEN** an administrator update of `default_image` and a claim whose resolution reaches team-default precedence execute concurrently for the same team
- **THEN** the transactions produce one linearizable order: a claim committed first persists the old image, while an update committed first makes the claim persist the new image, and the claim never stores a mixed or uncommitted value

#### Scenario: Default-image update is unauthorized or invalid

- **WHEN** a non-admin caller changes `default_image`, or an administrator submits an empty, tag-only, or otherwise invalid non-digest reference
- **THEN** State Registry rejects the request without changing the team or any task image resolution

#### Scenario: Provider-neutral digest algorithms are accepted consistently

- **WHEN** an administrator submits syntactically valid OCI digest references using different digest algorithm names through team creation or update
- **THEN** the OpenAPI contract and State Registry server-side validator accept the same values and persist each complete reference verbatim

## ADDED Requirements

### Requirement: State Registry test coordination is isolated from production behavior

State Registry SHALL expose no transaction-control operation on its public business or administration listeners. When explicit E2E-only configuration enables test control with a dedicated bind address, a non-empty test-control bearer token, and required integer `barrier_timeout_ms` in the inclusive range `1000..60000`, the built service image SHALL expose the exact arm, bounded status-wait, release, and cancel protocol in `specs/state-registry/openapi/test-control.openapi.yaml` for named one-shot E2E transaction barriers. `barrier_timeout_ms` SHALL have no default while test control is enabled. Startup SHALL fail when enabled test control omits any required field or supplies timeout below `1000`, above `60000`, or of the wrong type. The test-control listener SHALL accept only that token, SHALL bind only to the configured E2E network address, SHALL support only an allowlisted finite set of transaction points, and SHALL be absent when disabled. A barrier SHALL pause exactly one matching real production handler after the named transaction point is reached, report that arrival to the harness, and resume it at most once when released, when its bounded safety timeout expires, when the request is cancelled, or during graceful shutdown. Every terminal path SHALL consume and remove the active barrier. Except when process shutdown removes all in-memory E2E state, its terminal observation record SHALL remain readable until one successful `DELETE /test-control/v1/barriers/{barrier_id}` removes it; that DELETE SHALL return `204`, and every later GET, release, or DELETE for the identifier SHALL return `404 barrier_not_found`. Test coordination SHALL NOT replace a public handler, bypass authentication, mutate business data directly, select a business result, capture a second matching request, or remain armed for a later request.

#### Scenario: Normal runtime has no test-control surface

- **WHEN** State Registry starts without explicit E2E test-control configuration
- **THEN** no test-control listener, route, or transaction barrier exists and public handler behavior is unchanged

#### Scenario: E2E harness controls a real transaction order

- **WHEN** an authorized E2E harness arms one allowlisted one-shot barrier, sends a request through the real public API, waits until the handler reaches the barrier, sends the competing public request, and releases the barrier
- **THEN** the first handler resumes exactly once, both requests complete through production logic, database assertions observe the deliberately selected serial order, and the terminal observation remains readable until one successful cleanup DELETE

#### Scenario: Test-control configuration or access is unsafe

- **WHEN** enabled test control lacks its dedicated address, token, or `barrier_timeout_ms`, supplies timeout below `1000`, above `60000`, or of the wrong type, a caller presents a wrong token, or a caller requests an unknown transaction point
- **THEN** startup fails for invalid configuration or the isolated listener rejects the request without arming, releasing, or changing any business transaction

### Requirement: State Registry authenticates team administrators without local accounts

For `POST /admin/teams`, `PATCH /admin/teams/{team_id}`, and `POST /admin/teams/{team_id}/archive`, State Registry SHALL validate the configured provider-neutral admin JWT issuer, audience, asymmetric algorithm allowlist, signature, expiry, JWKS, bounded clock skew, and RFC 6901 role-claim pointer, and SHALL require exact role `flowai-system-admin`. It SHALL reject at startup a missing trust field, non-positive `admin_jwks_timeout`, non-positive `admin_token_max_age`, or symmetric algorithm. A system administrator SHALL be provisioned only by assigning that role in the configured external admin identity provider. State Registry SHALL expose no local administrator-create or role-assignment API and SHALL persist no administrator account or role membership. Every accepted JWT SHALL have `iat` and `exp`, SHALL have age no greater than configured `admin_token_max_age`, and SHALL carry the role in that signed token. Removing the external role SHALL prevent authorization by every token issued after removal; a previously issued token remains usable only until the earlier of `exp` or its bounded maximum age. Missing or invalid authentication SHALL return `401 invalid_admin_token`; an authenticated identity without the exact role SHALL return `403 insufficient_admin_role` before reading or mutating team state. An unavailable or timed-out JWKS refresh for an unknown `kid` SHALL return `502 admin_identity_provider_unavailable` without reading or mutating team state and without accepting an unrelated cached key.

Authenticated system-admin `GET /admin/teams/{team_id}` SHALL use the same JWT validation and failure semantics; credential-free `GET /internal/v1/teams/{team_id}` SHALL not enter this admin-authentication branch.

#### Scenario: External role provisions team administration

- **WHEN** an otherwise valid external admin identity gains or loses exact role `flowai-system-admin` and obtains a newly issued admin JWT
- **THEN** State Registry authorizes the new token only while the signed role is present, bounds any previously issued token by `exp` and `admin_token_max_age`, and stores no local administrator account or assignment

#### Scenario: Operator or malformed admin token calls team administration

- **WHEN** an operator working token or admin JWT with invalid issuer, audience, algorithm, signature, key, expiry, or role calls a team-admin endpoint
- **THEN** State Registry returns `401` or `403` as applicable before reading or mutating a team

#### Scenario: Admin signing key cannot be refreshed

- **WHEN** an admin JWT carries unknown `kid` and the configured JWKS endpoint fails or exceeds `admin_jwks_timeout`
- **THEN** State Registry returns `502 admin_identity_provider_unavailable` and performs no team read or mutation
