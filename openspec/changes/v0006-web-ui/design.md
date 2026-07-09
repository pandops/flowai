# Design: Web UI without Auth

## Runtime Shape

- Web UI has one backend base URL: API Gateway.
- API Gateway in this change runs in no-auth mode.
- API Gateway proxies each request to exactly one allowed backend: State Registry or Env Registry.
- API Gateway proxies live event WebSockets to State Registry.

## Boundaries

- Web UI never calls Router, State Registry, Env Registry, or Executor directly.
- API Gateway never calls Router or Executor.
- Auth and bearer-token validation are added later in `v0007-auth`.

## Proposed Diagrams

- `specs/diagrams/01-operator-surface-no-auth.puml`
- `specs/diagrams/02-gateway-routing-no-auth.puml`
- `specs/diagrams/03-live-events-no-auth.puml`
- `specs/diagrams/04-env-secret-write-no-auth.puml`
- `specs/diagrams/05-control-request-no-auth.puml`
