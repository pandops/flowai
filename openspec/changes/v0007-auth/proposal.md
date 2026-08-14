# Change: v0007-auth

## Why

The configured single-team no-auth bootstrap introduced by `v0006-web-ui` cannot attribute operator actions or safely support authenticated team isolation. FlowAI needs Keycloak authentication whose canonical operator and group memberships are consumed by API Gateway, resolved by State Registry to canonical teams, cannot be spoofed by browser headers, and produce a one-team working token without leaking browser or Keycloak credentials downstream.

## What Changes

- Add Keycloak OIDC login through API Gateway and require Gateway to obtain the authenticated user's canonical group identifiers from a validated Keycloak token or UserInfo response.
- Add a trusted Gateway-only State Registry team-resolution API that maps Keycloak group identifiers to canonical `team_id` and display-only `team_name` values without accepting browser-supplied groups.
- Return the resolved team list to Web UI, render it in the authenticated interface, and allow the user to select one accessible team when more than one is available.
- Issue working bearer tokens carrying canonical `operator_id`, exactly one immutable selected `team_id`, optional canonical display-only `team_name`, intended audience, and expiry; a team switch requires a new token validated against a fresh State Registry resolution.
- Strip browser-supplied internal identity/context headers and inject trusted `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, optional `X-FlowAI-Team-Name`, and Gateway-generated `X-FlowAI-Request-ID` on State Registry REST and WebSocket child requests.
- Consume the browser bearer token at the Gateway boundary; never forward it to State Registry.
- Keep State Registry authoritative for resource ownership and audit persistence, using stable `team_id` for isolation and canonical operator/team/request context for attribution.
- Treat validation, credential containment, header replacement, and trusted security-context enrichment as Gateway transport security rather than platform business logic; backend responses remain unchanged.

## Impact

- Change type: development
- Affected specs: `specs/auth/spec.md`, `specs/web-ui/spec.md`, `specs/api-gateway/spec.md`, `specs/state-registry/spec.md`
- Affected ADRs: none
- Affected diagrams: `specs/diagrams/01-auth-login.puml`, `specs/diagrams/02-authenticated-request.puml`, `specs/diagrams/03-authenticated-websocket.puml`
- Affected test cases: `specs/test-cases/v0007.*.md`
- Affected code: future `web-ui/`, `api-gateway/`, current `svc/state-registry/`, and root `qa-e2e/` implementation paths

## Out of Scope

- Team update/delete, membership mutation in FlowAI, multi-team working tokens, silent team switching within a token, or project RBAC.
- Using `team_name` for authorization, ownership, or isolation.
- Moving resource ownership or canonical platform audit state out of State Registry.
- API Gateway calls to services other than Keycloak for authentication and State Registry for platform APIs; Web UI calls to State Registry/Executors; or changes to listener task-ingestion topology.
- Forwarding browser bearer credentials to State Registry or making State Registry an auth session store.
