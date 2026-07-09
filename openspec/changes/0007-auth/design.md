# Design: Auth

## Runtime Shape

- Web UI authenticates through API Gateway.
- API Gateway issues bearer tokens at login or refresh.
- API Gateway validates bearer tokens on every Web UI REST request and WebSocket upgrade.
- After validation, API Gateway proxies to State Registry or Env Registry exactly as in `0006-web-ui`.

## Boundaries

- API Gateway remains stateless per request except optional auth-audit logs.
- API Gateway still has no Router or Executor connection.

## Proposed Diagrams

- `specs/diagrams/01-auth-login.puml`
- `specs/diagrams/02-authenticated-request.puml`
- `specs/diagrams/03-authenticated-websocket.puml`
