# Change: v0007-auth

## Why

The configured single-team no-auth bootstrap introduced by `v0006-web-ui` cannot attribute operator actions or safely support authenticated team isolation. FlowAI needs bearer authentication whose canonical operator and team context is established at the API Gateway, cannot be spoofed by browser headers, and is propagated to State Registry without leaking the browser credential.

## What Changes

- Add login and refresh issuance of bearer tokens carrying canonical `operator_id`, exactly one immutable `team_id`, optional canonical display-only `team_name`, intended audience, and expiry.
- Require one active team per token/session; reject missing, ambiguous, or stale team context before any REST proxy request or WebSocket subscription is created.
- Update Web UI to authenticate only through API Gateway, attach bearer credentials only to the Gateway, display optional canonical team name, and provide no team selector or membership UI.
- Strip browser-supplied internal identity/context headers and inject trusted `X-FlowAI-Operator-ID`, `X-FlowAI-Team-ID`, optional `X-FlowAI-Team-Name`, and Gateway-generated `X-FlowAI-Request-ID` on State Registry REST and WebSocket child requests.
- Consume the browser bearer token at the Gateway boundary; never forward it to State Registry.
- Keep State Registry authoritative for resource ownership and audit persistence, using stable `team_id` for isolation and canonical operator/team/request context for attribution.
- Treat validation, credential containment, header replacement, and trusted security-context enrichment as Gateway transport security rather than platform business logic; backend responses remain unchanged.

## Impact

- Change type: development
- Affected specs: `specs/auth/spec.md`, `specs/web-ui/spec.md`, `specs/api-gateway/spec.md`
- Affected ADRs: none
- Affected diagrams: `specs/diagrams/01-auth-login.puml`, `specs/diagrams/02-authenticated-request.puml`, `specs/diagrams/03-authenticated-websocket.puml`
- Affected test cases: `specs/test-cases/v0007.*.md`
- Affected code: future `web-ui/`, `api-gateway/`, and root `qa-e2e/` implementation paths

## Out of Scope

- Team CRUD, membership-management APIs/UI, team selector UI, multi-team tokens/sessions, team switching within a session, or project RBAC.
- Using `team_name` for authorization, ownership, or isolation.
- Moving resource ownership or canonical platform audit state out of State Registry.
- API Gateway calls to Executors, Web UI calls to State Registry/Executors, or changes to listener task-ingestion topology.
- Forwarding browser bearer credentials to State Registry or making State Registry an auth session store.
