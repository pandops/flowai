# Design: Authenticated Canonical Team Context

## Runtime Shape

- Web UI starts OIDC authentication through API Gateway and sends credentials or authorization responses only to the Gateway.
- API Gateway completes standard OIDC with the configured provider, validates issuer/signature/audience/expiry, obtains canonical `operator_id` from validated `sub` within that issuer, and normalizes provider-specific team claims into canonical opaque OIDC team identifiers.
- API Gateway resolves canonical OIDC team identifiers through its own database. The Gateway mapping references State Registry-owned immutable `team_id` values and carries the canonical display-only `team_name` needed for the login surface.
- API Gateway returns the resolved team list to Web UI. If more than one team is available, Web UI displays the list and asks the operator to select one.
- API Gateway creates an opaque server-side OIDC session and a `Secure`, `HttpOnly`, `SameSite=Lax` session cookie after the callback. It issues a signed working bearer token only after the selected `team_id` is confirmed by a fresh OIDC membership check and Gateway-owned mapping lookup. Each working token binds one canonical `operator_id` to exactly one immutable active `team_id`; `team_name` is display-only.
- Working tokens use an explicitly configured asymmetric algorithm allowlist and carry `kid`, `iss`, `aud`, `sub`, `operator_id`, exactly one `team_id`, optional display-only `team_name`, `iat`, `exp`, and unique `jti`. Gateway validates the allowlist, verification-key ring, and bounded clock skew.
- API Gateway validates bearer and canonical team freshness on every Web UI REST request and WebSocket upgrade before creating a State Registry child request.
- API Gateway remains disconnected from Executors and State Registry remains the only platform backend.

## Canonical Identity and Team State

- OIDC provider is authoritative for authentication and user-to-OIDC-team membership. API Gateway owns the mapping from `(issuer, oidc_team_id)` to State Registry-owned canonical `team_id` and owns its derived operator-to-team access records. State Registry owns canonical team identity and resource ownership but stores no operator or OIDC membership mapping.
- API Gateway accepts and stores mappings only for its one configured operator OIDC issuer, with one unique mapping per `oidc_team_id` and one active mapping per canonical `team_id`. OIDC team display names are not authorization keys.
- A working token cannot contain multiple team IDs or silently switch its team ID. `POST /auth/v1/token` always creates a new token with a new `jti`; renewing the same team and selecting another accessible team both require fresh-enough membership resolution. There is no browser-visible OIDC token or separate FlowAI refresh token.
- `team_name` is copied from State Registry for display and is never an authorization or ownership key.
- State Registry permits system-admin-only updates to active or archived-team `team_name` and digest-bearing `default_image`, and idempotent archival through dedicated admin endpoints. `team_id`, ownership links, resources, history, and audit remain immutable or preserved; physical deletion and unarchive do not exist.
- An archived team remains mapped, selectable, token-eligible, and available for authenticated operator REST and WebSocket reads. State Registry rejects every `POST /v1/tasks` for it before persistence, dedupe lookup, or acknowledgement, including retry of an existing dedupe key; the existing task row remains unchanged, while pending, claimed, and terminal task lifecycle continues.
- API Gateway owns auth sessions, membership snapshots, mapping migrations, and auth audit. State Registry is not an auth session or membership store.
- API Gateway and State Registry MAY use the same PostgreSQL server instance, but use separate schemas, database roles, migration histories, connection configuration, and least-privilege grants. Neither service reads or writes the other's tables.

## OIDC Core and Team-Claim Adapters

- OIDC discovery, authorization-code flow with PKCE, ID Token validation, and UserInfo are the provider-neutral authentication boundary.
- OIDC Core does not define team, group, or organization membership. Gateway therefore requires an explicit team-claim adapter and fails startup for an absent or unsupported adapter configuration.
- Team-claim configuration selects exactly one source from `id_token` or `userinfo`; Gateway never falls back between them. Both adapters resolve a required RFC 6901 JSON Pointer `claim_pointer`. A missing or `null` target, or a target of the wrong type, fails authentication closed; a correctly typed empty array produces zero accessible teams.
- The `string_array` adapter requires every array element to be a non-empty string. The `object_array` adapter requires every element to be an object and extracts a non-empty opaque string from required configured `id_field`. Optional configured `name_field` is display metadata only and never authority. One malformed element fails the complete authentication attempt closed.
- Provider-specific scopes are configuration data, not adapter behavior. Gateway trims and deduplicates configured non-empty `additional_scopes`, requires `openid`, rejects structurally invalid scope configuration at startup, and never lets an adapter inject a scope.
- Gateway refreshes provider membership at login and before token issue whenever the stored observation is older than configured non-negative `membership_max_age`. Zero means every token issue requires a provider refresh. A required refresh failure issues no token and never falls back to stale membership. Every provider endpoint uses configured positive `endpoint_timeout`.
- Both adapters produce the same internal `{issuer, subject, oidc_team_ids}` representation. Gateway derives `operator_id` from the configured issuer and subject, resolves identifiers in its own database, and sends no provider-specific identity data to State Registry.
- OIDC-provider administrators create teams or organizations and manage membership outside FlowAI. A FlowAI system administrator separately registers the external-to-canonical mapping in API Gateway after the canonical team exists in State Registry; login never auto-creates either record.
- A FlowAI system administrator is provisioned outside FlowAI by assigning exact role `flowai-system-admin` in the configured external admin identity provider. Tokens issued after role removal are unauthorized; previously issued stateless JWTs remain bounded by the earlier of `exp` or configured positive `admin_token_max_age`. Neither Gateway nor State Registry owns an administrator directory.

## Trusted Downstream Context

1. Browser sends the OIDC provider authorization response or Gateway working bearer to API Gateway and may attempt to add forged OIDC team values or `X-FlowAI-*` headers.
2. Gateway validates OIDC state and normalizes the configured claim shape into canonical OIDC team identifiers, or validates the working bearer and its one non-empty `team_id`.
3. For login, team listing, token issue, and freshness checks, Gateway obtains membership from the configured identity source and resolves it through its own database. Unknown but well-formed OIDC team IDs are omitted without failing the login. It accepts a selected active or archived team when it appears in the resolved result and exposes `archived_at` for display.
4. Gateway rejects missing, invalid, expired, unmapped, unauthorized, or stale identity/team context before proxying or opening a child WebSocket; archival alone does not revoke operator access.
5. Gateway removes browser-supplied OIDC team values and internal identity/context headers.
6. Gateway creates a request ID and injects canonical `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, optional display-only `X-FlowAI-Team-Name`, and `X-FlowAI-Request-ID` on every team-scoped REST and WebSocket child request.
7. Gateway removes `Authorization` and does not forward browser or OIDC credentials to State Registry.
8. State Registry uses trusted `team_id` for resource ownership/isolation and the canonical operator/team/request identifiers for audit attribution without persisting operator membership.

## Web UI Session Behavior

- Web UI keeps the working bearer only in memory and uses it only with API Gateway. Provider credentials and OIDC tokens remain server-side and are never exposed to browser JavaScript.
- Web UI displays the State Registry-resolved team list and canonical `team_name` values without inferring authorization from names.
- Web UI may select one resolved team for a new working token. It offers no arbitrary team input, team CRUD, or membership management.
- Web UI continues to use a single API Gateway base URL and never calls State Registry or an Executor directly.
- A separate deployment ingress/reverse proxy owns the browser origin. It routes `/` and static Web UI assets to the Web UI service, and routes `/auth`, `/admin`, `/ui`, including WebSocket upgrades, to API Gateway. Neither Web UI nor API Gateway embeds or directly serves the other service.
- Logout deletes the server-side OIDC session and expires the session cookie. Existing short-lived bearers remain bounded by `exp` and current Gateway-owned access checks; the closed session cannot issue another token.

## Public Auth and Administration Surface

- `GET /auth/v1/login` starts authorization-code with PKCE; `GET /auth/v1/callback` validates the response and creates the server-side session cookie.
- `GET /auth/v1/teams` returns the current session's ordered resolved teams. `POST /auth/v1/token` accepts exactly one `team_id` and returns a newly issued working bearer. `DELETE /auth/v1/session` logs out.
- `POST /admin/v1/oidc-team-mappings` rejects a non-configured issuer before touching either persistence boundary, then registers one mapping after reading the canonical team through State Registry's admin API. An identical retry is idempotent; conflicting external or canonical ownership is rejected atomically; missing team and unavailable dependency are distinct failures.
- `PATCH /admin/teams/{team_id}` changes only `team_name` or `default_image` for an active or archived team; `POST /admin/teams/{team_id}/archive` archives idempotently. Both are State Registry APIs specified by `specs/state-registry/openapi/team-admin.openapi.yaml`; there is no delete or unarchive endpoint.
- Admin-bearer-only `GET /admin/teams/{team_id}` and credential-free internal `GET /internal/v1/teams/{team_id}` return the same one-team representation through distinct route and middleware branches. Consistent with accepted v0009, Gateway sends no replacement service credential on the internal route, which deployment network policy protects. Gateway uses it for mapping validation and current `team_name`/`archived_at` presentation; no team collection scan is added, and an invalid admin bearer cannot fall back to the internal branch.
- A committed rename releases the old display name for later create or update while immutable audit retains the historical value.
- Team create and update validate `default_image` against one provider-neutral OCI digest-reference grammar in both OpenAPI and server code: `<repository>@<algorithm>:<encoded-digest>`. The algorithm is not pinned to `sha256`; tag-only and malformed values are rejected before mutation.
- Gateway and State Registry admin authentication is provider-neutral: each validates configured admin JWT issuer, audience, asymmetric algorithm allowlist, JWKS, positive `admin_jwks_timeout`, positive `admin_token_max_age`, and bounded clock skew, resolves a configured RFC 6901 role-claim pointer, and requires exact role value `flowai-system-admin`. Assignment happens in that external identity provider, not in FlowAI. Operator working tokens cannot call an admin API.
- Errors use `{code, message, request_id}` and one finite OpenAPI-enumerated code. Authentication failures use `401`, authenticated but unauthorized actions use `403`, missing canonical teams use `404`, mapping conflicts use `409`, contacted identity endpoints use `502`, and unavailable State Registry uses `503`.

## Deterministic E2E Transaction Coordination

- Concurrency cases use real public handlers and real PostgreSQL transactions. They do not infer transaction order from request start time.
- The State Registry production image contains an inert test-control component. It starts no listener and installs no barriers while disabled. When E2E configuration supplies `enabled=true`, it must also supply a dedicated bind address, a non-empty bearer token, and integer `barrier_timeout_ms` in inclusive range `1000..60000`; timeout has no enabled-mode default. With valid configuration the image always starts the separate listener and exposes the complete test-control contract; missing, wrong-type, lower, or upper-bound violations fail startup and readiness.
- The dedicated listener is reachable only on the isolated E2E network and accepts only the configured bearer. It is never mounted on the public or admin router.
- The finite barrier allowlist is `team_archive_after_lock`, `task_ingest_after_team_lock`, `task_claim_after_team_lock`, `team_default_image_update_after_lock`, and `team_name_write_after_constraint_before_commit`. The last point occurs after the database has accepted the unique-name write but before commit, so create/create, create/update, and update/update can be ordered without an existing target row.
- `specs/state-registry/openapi/test-control.openapi.yaml` is the complete wire contract. The harness arms with `POST /test-control/v1/barriers`, observes `armed` or `reached` through bounded long-poll `GET /test-control/v1/barriers/{barrier_id}?wait_ms=...`, releases only a reached barrier with `POST /test-control/v1/barriers/{barrier_id}/release`, and cancels cleanup with `DELETE /test-control/v1/barriers/{barrier_id}`. Exact status and error codes are finite in that document.
- The harness arms one named one-shot barrier, calls the real public endpoint, waits for an authenticated `reached` observation, starts the competing public request, and releases the barrier. The barrier is consumed by one matching transaction and cannot leak into another test.
- Release, request cancellation, safety timeout, and graceful shutdown resume or terminate the paused handler at most once and consume the active barrier. A second matching transaction is never captured by the same barrier. Except across process shutdown, the terminal observation remains readable until one successful DELETE removes it; subsequent operations return `404 barrier_not_found`. Timeout is a test failure signal, not a selected business outcome; production code continues to determine commit, conflict, and response behavior.
- Invalid control configuration fails startup. A missing listener, wrong token, unknown name, duplicate arm, release before arrival, second release, cancelled public request, or shutdown cannot alter unrelated business data, leak the barrier to another request, or bypass normal endpoint authentication.

## Business-Logic Boundary

- Authentication checks, bearer containment, spoofed-header removal, trusted security-context injection, request-ID generation, and allowed-target enforcement are Gateway transport security, not platform business logic.
- Gateway does not decide resource ownership or rewrite State Registry results.
- REST response status, headers, and body and WebSocket event frames remain unchanged across the Gateway after successful authentication/context establishment.

## Proposed Diagrams

- `specs/diagrams/auth-login-sequence.puml`
- `specs/diagrams/authenticated-request-sequence.puml`
- `specs/diagrams/authenticated-websocket-sequence.puml`
