# State Registry

State Registry is FlowAI's durable control plane and source of truth. It owns
team-scoped task ingestion, FIFO discovery and atomic claims, assignments,
ordered lifecycle history, controls, environments, encrypted secret versions,
audit records, and administrator projections.

## Responsibilities

- Create teams, source systems, and task types through authenticated
  `/admin/*` endpoints.
- Persist listener tasks before acknowledgement and deduplicate by
  `(team_id, source_system_id, source_id)`.
- Register team-owned and system-owned Executors with exactly one authorized
  tag.
- Discover pending work in `(ingested_at, task_id)` FIFO order and atomically
  claim only the oldest eligible task.
- Project `pending → created → running → finished | failed`.
- Resolve images at claim using task, task-type, source-system, then required
  team-default precedence.
- Store environments and immutable AES-256-GCM-encrypted secret versions and
  open them only for the assigned Executor with a valid scope token.
- Apply immutable `team_id` isolation before reads, pagination, WebSocket
  fan-out, mutation, audit shaping, or decryption.

It does not execute tasks, manage Docker, enforce Executor capacity,
authenticate human sessions, or provide Router/Env Registry fallbacks.

## Runtime configuration

Production requires `STATE_REGISTRY_POSTGRES_URL`, 32-byte hex
`STATE_REGISTRY_AES_KEY_HEX`, scope-token and cursor signing key IDs/hex
keys, server certificate/private key/client CA,
`STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT=true`, and PostgreSQL CA with
`STATE_REGISTRY_POSTGRES_TLS_MODE=verify-full`. Startup fails closed when
mandatory cryptographic or TLS configuration is incomplete. Migrations are
applied automatically before serving traffic.

Build and test:

```bash
env -u GOROOT go build ./svc/state-registry/cmd/state-registry
env -u GOROOT go test ./svc/state-registry/...
env -u GOROOT go test -tags=integration ./svc/state-registry/...
```

Health endpoints are `GET /v1/livez` and `GET /v1/readyz`. Exact HTTP
schemas are defined by the
[OpenAPI contract](../../openspec/specs/state-registry/openapi/state-registry.openapi.yaml).
