# Change: v0007-auth

## Why

The configured single-team no-auth bootstrap introduced by `v0006-web-ui` cannot attribute operator actions or safely support authenticated team isolation. FlowAI needs OIDC authentication whose canonical operator identity and OIDC team memberships are consumed and resolved by API Gateway through its own database, cannot be spoofed by browser headers, and produce a one-team working token without leaking browser or OIDC credentials downstream.

## What Changes

- Add provider-neutral OIDC login through API Gateway and require Gateway to obtain the authenticated user's canonical OIDC team identifiers from a validated ID Token or UserInfo response through an explicitly configured claim adapter.
- Keep authentication on standard OIDC discovery, authorization-code-with-PKCE, token validation, and UserInfo contracts while treating team membership as a configurable claim shape; ship provider-neutral `string_array` and `object_array` adapters without naming or depending on an identity-provider product.
- Give API Gateway a service-owned database that maps one configured OIDC issuer's stable team identifiers to State Registry-owned canonical `team_id` values and records the authenticated operator-to-team access derived from validated OIDC membership. API Gateway MAY share a PostgreSQL instance with State Registry, but SHALL use a separate schema, database role, migrations, and connection configuration.
- Return the resolved team list to Web UI, render it in the authenticated interface, and allow the user to select one accessible team when more than one is available.
- Issue working bearer tokens carrying canonical `operator_id`, exactly one immutable selected `team_id`, canonical display-only `team_name`, intended audience, and expiry; a team switch requires a new token validated against a fresh Gateway-owned membership resolution.
- Define versioned login, callback, team-list, token-issue, logout, and mapping-registration HTTP contracts in `specs/api-gateway/openapi/auth.openapi.yaml`; keep provider credentials in a Gateway-owned server session and working bearers in browser memory only.
- Make membership parsing provider-neutral through one configured `id_token` or `userinfo` source, RFC 6901 JSON Pointer, `string_array` or `object_array` shape, explicit scopes, deterministic freshness, endpoint timeouts, and fail-closed provider-outage behavior.
- Authenticate mapping administration with a separately configured provider-neutral admin JWT trust and exact `flowai-system-admin` role rather than an identity-provider product integration.
- Provision system administrators only through exact `flowai-system-admin` role assignment in the configured external admin identity provider; FlowAI stores no local administrator accounts or role assignments.
- Extend State Registry team administration so a system administrator may change an active team's display-only `team_name` and required digest-bearing `default_image`, or archive the team without deleting its row, resources, ownership references, history, or audit.
- Define `default_image` with one provider-neutral OCI digest-reference grammar shared by OpenAPI and server validation; accept algorithm-qualified digests without pinning one algorithm and reject tag-only or malformed references.
- Add a canonical-team point read for authenticated system administrators and for Gateway's network-admitted, unauthenticated backend call, preserving v0009's no-replacement-service-credential decision, so Gateway can validate mappings and refresh current `team_name` and `archived_at` without direct database access; a successful rename releases the previous display name for later reuse.
- Add an explicitly enabled, isolated, token-protected E2E test-control listener with finite one-shot transaction barriers so concurrency cases exercise both commit orders through real handlers; no control surface exists in normal runtime configuration.
- Return archived canonical teams in the authenticated team list with `archived_at`, preserve their mapping and operator read access, and make State Registry reject every archived-team `POST /v1/tasks`, including retry of an existing dedupe key, with the existing task row unchanged.
- Strip browser-supplied internal identity/context headers and inject trusted `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, optional `X-FlowAI-Team-Name`, and Gateway-generated `X-FlowAI-Request-ID` on State Registry REST and WebSocket child requests.
- Consume the browser bearer token at the Gateway boundary; never forward it to State Registry.
- Keep State Registry authoritative for canonical team resources, resource ownership, and audit persistence, using stable `team_id` for isolation and canonical operator/team/request context for attribution. State Registry stores no operator membership, OIDC membership, or OIDC-team mapping.
- Treat validation, credential containment, header replacement, and trusted security-context enrichment as Gateway transport security rather than platform business logic; backend responses remain unchanged.

## Adversarial verification matrix

### Axes

- OIDC evidence: valid ID Token, valid UserInfo response, invalid state/nonce, expired token, wrong audience/issuer/algorithm/signature/key, malformed or absent team claim, standard provider error callback, and unavailable provider.
- Team-claim adapter: `string_array`, `object_array`, unsupported adapter, and valid adapter with the wrong claim shape.
- OIDC callback shape: successful `code` only, provider `error` only, both present, or neither present; every form carries `state` whose match is independently varied.
- Membership cardinality and mapping: zero, one, or several OIDC teams; none, some, or all memberships mapped to canonical teams.
- Selected-team lifecycle: initial selection, refresh with the same team, explicit switch to another accessible team, browser-selected inaccessible team, and mapping or membership revoked after token issue.
- Canonical-team lifecycle: active, renamed, default image changed, archived once, archive retried, update after archive, and attempted deletion or unarchive.
- Administrator lifecycle: exact external `flowai-system-admin` role absent, assigned, removed, or represented only by an operator token.
- Transport: REST or WebSocket; valid bearer, missing bearer, forged internal headers, replay after reconnect, and downstream response/error propagation.
- Persistence boundary: separate PostgreSQL instance or shared instance; correct Gateway role/schema, State Registry role attempting Gateway access, and Gateway role attempting State Registry access.
- Downstream authority and attribution: same-team resource, foreign-team resource, canonical `operator_id`/`team_id`/optional `team_name`, and browser bearer present or absent at State Registry.
- Team update: active or archived team; `team_name` only, `default_image` only, both, empty, forbidden, or missing-team; unique-name writers are create/create, create/update, and update/update across teams; concurrency includes update/update, update/archive, and default-image update/claim in both commit orders.
- Archive/ingestion order: ingestion or dedupe retry commits first, archive commits first, concurrent archive retry; source identity is new, accepted before archive, or rejected after archive.
- Existing task state at archive: `pending`, `claimed/running`, `finished`, or `failed`, with claim, event, control, REST, and WebSocket consumers.
- Claimant scope for every pre-existing pending-task claim and image-resolution race: team-owned Executor or system-owned Executor.
- Admin consumer and operation: API Gateway mapping administration or State Registry team create, update, and archive.
- Admin startup configuration: valid, required trust field missing, non-positive `admin_jwks_timeout`, non-positive `admin_token_max_age`, or symmetric algorithm; runtime JWKS is cached, rotated, unavailable, or timed out.
- E2E transaction coordination: disabled normal runtime; valid isolated listener; missing address/token/`barrier_timeout_ms`; timeout wrong type, `999`, `1000`, `60000`, or `60001`; wrong token; unknown, duplicate, expired, reused, cancelled, or shutdown-interrupted one-shot barrier; zero, one, or several matching requests; each allowlisted transaction point.
- Default-image syntax: valid `sha256` and non-`sha256` algorithm-qualified OCI references, tag-only reference, missing repository/algorithm/encoded digest, whitespace, repeated `@`, and malformed separators.

### Meaningful cells and planned evidence

| Mechanism and combination | Planned evidence | Coverage |
| --- | --- | --- |
| Valid `string_array` claim × one mapped team × initial login | `v0007.1`, `v0007.5`, `v0007.16`, `v0007.17` | Proposed E2E |
| Valid `object_array` claim × several mapped teams × explicit accessible selection | `v0007.3`, `v0007.16`, `v0007.17` | Proposed E2E |
| Valid membership × zero mapped teams, or selected team is unmapped/inaccessible | `v0007.12`, `v0007.19` | Proposed E2E |
| Several memberships × only some mapped × selection from mapped subset | `v0007.22` | Proposed E2E plus exact Gateway DB row assertions |
| Unsupported adapter or malformed/absent configured claim | `v0007.12`, `v0007.16` | Proposed E2E |
| OIDC provider unavailable during discovery, token exchange, JWKS refresh, or UserInfo lookup | `v0007.23` | Proposed parameterized E2E |
| Invalid OIDC state/nonce, issuer/audience/algorithm/signature/key/expiry/skew, or provider error callback | `v0007.27` | Proposed E2E plus exact no-authority DB assertions |
| OIDC callback × exactly one of `code`/`error`, both, or neither | `v0007.27` | Proposed schema and E2E validation |
| Invalid or stale Gateway working bearer | `v0007.2`, `v0007.12` | Proposed E2E |
| Token renewal with unchanged membership × same selected team | `v0007.24` | Proposed E2E plus session/token DB assertions |
| Refresh or switch × accessible new team × new one-team token | `v0007.3`, `v0007.5` | Proposed E2E |
| Token issue or switch × revoked membership/mapping × old or requested team | `v0007.2`, `v0007.3`, `v0007.19`, `v0007.24` | Proposed E2E under explicit `membership_max_age` semantics |
| Concurrent mapping registration × same issuer/team identifier × same or conflicting canonical team | `v0007.18`, `v0007.25` | Proposed E2E plus unique-constraint and transaction DB assertions |
| Mapping registration × missing canonical team or unavailable/timed-out State Registry | `v0007.18` | Proposed E2E plus exact no-row DB assertions |
| Mapping registration × issuer differs from the one configured operator issuer | `v0007.18` | Proposed `400 invalid_request` E2E plus no-read/write observations |
| Admin JWT × invalid credentials/role or unavailable/timed-out admin JWKS | `v0007.25` | Proposed E2E plus no-read/write observations |
| External admin identity × exact role assigned then removed | `v0007.30` | Proposed E2E plus absence of local administrator rows |
| Active canonical team × name/default-image update × immutable ownership | `v0007.28` | Proposed E2E plus exact State Registry DB assertions |
| Active or archived canonical team × archive/retry/update/delete/unarchive | `v0007.29` | Proposed E2E plus exact State Registry and Gateway DB assertions |
| Archived canonical team × mapping/list/token/REST/WebSocket × new or dedupe-retry `POST /v1/tasks` | `v0007.29` | Proposed E2E proving preserved operator reads, `409 team_archived`, and unchanged existing task row |
| Archive transaction × concurrent new/retried source-input ingestion × either lock order | `v0007.31` | Proposed controlled-race E2E plus exact commit/row assertions |
| Archived team × pre-existing pending/claimed/finished/failed tasks | `v0007.32` | Proposed E2E covering claim, finish/fail, controls, events, REST and WS history |
| State Registry admin JWT × invalid trust/claim/age/key × JWKS cached/rotated/unavailable/timeout | `v0007.33` | Proposed E2E plus no-read/write observations |
| Gateway or State Registry startup × valid or invalid admin trust configuration | `v0007.34` | Proposed container E2E with listener/readiness oracle |
| Concurrent unique-name writers × create/create, create/update, update/update | `v0007.35` | Proposed controlled-race E2E plus exact row/constraint assertions |
| Team default-image update × concurrent team-default claim × active/archived team × team/system Executor × either commit order | `v0007.36` | Proposed controlled-race E2E plus exact `resolved_image` assertions |
| Archive × existing dedupe retry × either commit order | `v0007.37` | Proposed controlled-race E2E proving `200` before archive or `409` after archive with unchanged task row |
| Gateway admin JWT × invalid issuer/alg/time/age × cached/rotated JWKS | `v0007.38` | Proposed runtime E2E plus no-read/write observations |
| Archive × claim of pre-existing pending task × team/system Executor × either commit order | `v0007.39` | Proposed controlled-race E2E plus assignment/event/image assertions |
| Canonical team point read × admin/Gateway/unauthorized identity × active/archived/missing/outage | `v0007.40` | Proposed E2E and OpenAPI contract assertions |
| Active/archived rename × released old name × create/update race | `v0007.41` | Proposed E2E plus unique-row and audit assertions |
| Test-control lifecycle × disabled/enabled/invalid config × every allowlisted barrier | `v0007.42` | Proposed container E2E proving isolation, authentication, one-shot use, timeout, and real-handler coordination |
| Team create/update × provider-neutral OCI digest grammar × valid/invalid reference | `v0007.43` | Proposed API/OpenAPI E2E plus exact database-value assertions |
| Keycloak 26.6.3 × authorization code with PKCE × ID Token/UserInfo/JWKS team claims | `v0007.45` | Containerized compatibility E2E; production integration remains provider-neutral |
| Canonical team point read × admin bearer or credential-free Gateway backend transport × invalid presented bearer | `v0007.44` | Proposed E2E proving v0009-compatible transport and absence of a replacement service credential |
| REST × valid bearer × browser-forged identity/request headers | `v0007.7`, `v0007.9`, `v0007.10`, `v0007.13`, `v0007.14` | Proposed E2E |
| REST × missing/invalid bearer × no downstream request | `v0007.2`, `v0007.8`, `v0007.12` | Proposed E2E |
| REST × same-team or foreign-team identifier × unchanged backend response semantics | `v0007.10`, `v0007.11` | Proposed E2E |
| WebSocket × valid bearer × same-team replay/live frames | `v0007.15` | Proposed E2E |
| WebSocket × missing/invalid bearer, forged team context, foreign replay/live frames, token expiry, or reconnect | `v0007.15`, `v0007.26` | Proposed E2E |
| Gateway forwards canonical operator/team/request context × optional display name absent or present | `v0007.1`, `v0007.9`, `v0007.14` | Proposed E2E |
| Browser bearer reaches Gateway × State Registry receives no bearer/OIDC credential | `v0007.4`, `v0007.6`, `v0007.13` | Proposed E2E |
| Gateway and State Registry share PostgreSQL × separate roles/schemas/migrations × normal service operation | `v0007.21` exercises both production services through supported APIs before and after restart; Stage 1 checks migrations and consumption paths | Proposed E2E plus Stage 1 evidence |
| Either service role attempts cross-schema read/write | Stage 2 uses a positive own-schema sentinel followed by cross-schema `SELECT`, `INSERT`, `UPDATE`, and migration-DDL failure oracles, then rechecks both public APIs | Stage 2 evidence required |
| State Registry API receives OIDC mapping or membership data | `v0007.20` requires HTTP 400 with no mutation, then proves Gateway mapping leaves the State Registry API representation unchanged; Stage 1 checks schema and consumers | Proposed E2E plus Stage 1 evidence |

### Discarded combinations

- A working token containing several active teams is impossible by contract: selection always issues a new token with exactly one immutable `team_id`.
- A browser-authenticated request directly to State Registry is outside the allowed topology and is represented by Gateway-only routing and credential-containment negative tests, not as a supported success path.
- OIDC provider team creation, rename, deletion, or membership mutation is outside FlowAI ownership and is not exercised as a platform behavior; tests may only vary provider responses or pre-provisioned fixture state.
- `team_name` changing authorization is impossible by contract because only immutable `team_id` participates in authorization; `team_name` is tested only as optional display context.
- Fallback from `userinfo` to `id_token` or conversely is impossible by contract: exactly one configured source is authoritative for one Gateway deployment.

## Impact

- Change type: development
- Affected specs: `specs/auth/spec.md`, `specs/web-ui/spec.md`, `specs/api-gateway/spec.md`, `specs/api-gateway/openapi/auth.openapi.yaml`, `specs/state-registry/spec.md`, `specs/state-registry/openapi/team-admin.openapi.yaml`, `specs/state-registry/openapi/test-control.openapi.yaml`
- Affected ADRs: `specs/adrs.md`
- Affected diagrams: `specs/diagrams/auth-login-sequence.puml`, `specs/diagrams/authenticated-request-sequence.puml`, `specs/diagrams/authenticated-websocket-sequence.puml`
- Affected test cases: `specs/test-cases/v0007.*.md`
- Affected code: future `svc/web-ui/` and `svc/api-gateway/`, current `svc/state-registry/`, and root `qa-e2e/` implementation paths

## Out of Scope

- Physical team deletion, team unarchive, changing immutable `team_id`, membership mutation in FlowAI, multi-team working tokens, silent team switching within a token, or project RBAC.
- Using `team_name` for authorization, ownership, or isolation.
- Moving resource ownership or canonical platform audit state out of State Registry.
- API Gateway calls to services other than the configured OIDC provider for authentication and State Registry for platform APIs; Web UI calls to State Registry/Executors; or changes to listener task-ingestion topology.
- Creating, renaming, deleting, or changing membership of teams, groups, or organizations in an OIDC provider; provider administration remains an external administrator responsibility.
- Forwarding browser bearer credentials to State Registry or making State Registry an auth session store.
