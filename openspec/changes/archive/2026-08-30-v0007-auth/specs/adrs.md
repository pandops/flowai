# Proposed ADRs for v0007-auth

## ADR: Keep identity membership in API Gateway-owned persistence

### Status

Proposed

### Context

OIDC authentication produces issuer-, subject-, and provider-team identifiers, while State Registry owns canonical platform teams and team-scoped resources. Persisting identity-provider membership in State Registry would couple canonical platform state to authentication protocol details and make State Registry an identity store.

### Decision

API Gateway owns OIDC sessions, `(issuer, oidc_team_id)` to canonical `team_id` mappings, derived operator-to-team access, and authentication audit. State Registry owns canonical teams, resource ownership, and platform audit, and stores no OIDC or operator-membership mapping. The services may share one PostgreSQL server instance but use separate schemas, roles, credentials, migration histories, and least-privilege grants.

### Consequences

System administrators register a canonical team in State Registry and separately register its external OIDC mapping in API Gateway. Cross-service consistency is checked through supported APIs rather than cross-schema foreign keys or direct table access. API Gateway implementation and E2E infrastructure require service-owned migrations and database configuration.

## ADR: Use provider adapters behind one canonical membership model

### Status

Proposed

### Context

OIDC Core standardizes authentication but not team, group, or organization membership claims. FlowAI must support common string-array and object-array claim shapes without making one provider the platform identity model.

### Decision

API Gateway validates standard OIDC flows first and then applies one explicitly configured provider-neutral adapter to one configured `id_token` or `userinfo` source. `string_array` and `object_array` use an RFC 6901 `claim_pointer` and configured fields and produce the same internal `{issuer, subject, oidc_team_ids}` representation, which is resolved only through Gateway-owned persistence. Provider-specific scopes, when needed, are explicit `additional_scopes` configuration rather than adapter behavior.

### Consequences

Unsupported or malformed adapter configuration fails closed. Provider-specific aliases, names, and group paths never become authorization keys, and State Registry receives no provider-specific values.

## ADR: Keep browser OIDC credentials in a server-side Gateway session

### Status

Proposed

### Context

Web UI needs to list accessible teams and request short-lived one-team working tokens without receiving reusable identity-provider credentials or owning refresh-token rotation.

### Decision

API Gateway stores OIDC credentials only in encrypted server-side session state and identifies the session with a `Secure`, `HttpOnly`, `SameSite=Lax` cookie. `POST /auth/v1/token` always issues a new asymmetric working bearer with a unique `jti`; Web UI keeps that bearer only in memory. FlowAI defines no separate browser refresh token.

The browser-facing origin is assembled by a separate ingress/reverse proxy. Root and static Web UI requests terminate at the Web UI service; `/auth`, `/admin`, `/ui`, and WebSocket upgrades terminate at API Gateway. This keeps both runtime images independently deployable while preserving same-origin cookies and browser API calls.

### Consequences

Logout removes the server session and prevents further token issue. Existing working tokens remain short-lived and subject to active Gateway-owned access checks. Signing-key rotation uses `kid` plus a verification-key ring, and provider credentials never cross the Gateway boundary.

## ADR: Preserve canonical teams through explicit archival

### Status

Proposed

### Context

Canonical `team_id` values own durable resources and audit history. Teams need mutable display metadata and an operational offboarding mechanism, but physical deletion or identifier reuse would break ownership history and Gateway mappings.

### Decision

State Registry keeps `team_id` immutable, permits system-admin-only changes to `team_name` and digest-bearing `default_image`, and archives through an idempotent dedicated endpoint that sets `archived_at` once. Archive preserves the team row and every owned resource and history; delete and unarchive endpoints do not exist. Gateway preserves mapping, listing, token issue, and authenticated operator reads for archived teams and exposes `archived_at`; State Registry alone rejects new task ingestion for an archived team before persistence or acknowledgement.

### Consequences

Archival does not revoke OIDC mapping or operator membership and does not hide the team from Web UI. Existing REST and WebSocket reads and existing task lifecycle remain available, while every new task submission is rejected. Updating `default_image` affects only later claims that reach team-default precedence; already claimed tasks retain their stored resolution.

## ADR: Preserve credential-free Gateway-to-Registry backend transport

### Status

Proposed

### Context

The accepted v0009 change removed backend mTLS and explicitly rejected replacement bearer credentials or request signing. Gateway now needs a point read of one canonical team to validate a mapping and refresh display metadata, but adding a Gateway service JWT only for this route would silently reverse that accepted transport decision and introduce a second trust lifecycle.

### Decision

State Registry exposes two formally distinct routes with the same one-team representation: admin-bearer-only `GET /admin/teams/{team_id}` and credential-free `GET /internal/v1/teams/{team_id}` for the existing Gateway backend path. State Registry does not authenticate Gateway transport; deployment network policy remains responsible for admitting the internal path, as in v0009. Neither route exposes a collection scan. Because admin and internal traffic use different route and middleware branches, an invalid admin bearer cannot fall back to credential-free treatment.

### Consequences

No Gateway service key, issuer, audience, JWKS, acquisition, or rotation mechanism is introduced. Deployments must preserve network isolation of State Registry. The point-read contract does not claim protection against a peer already admitted to the trusted backend network; that residual risk remains the accepted v0009 posture.
