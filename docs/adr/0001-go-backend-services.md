# ADR-0001: FlowAI backend services use Go

- **Status**: Accepted
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

## Context

FlowAI is a multi-service platform (Executor, Router, State Registry, Env Registry,
API Gateway, Web UI, future K8s Executor). The backend services share a need for:
long-running daemon behavior, HTTP API servers, Docker daemon interaction (for
executors), concurrency, structured logging, and container-friendly single-binary
deployment. The Web UI is a separate decision (likely TypeScript) and is out of
scope here.

## Decision

Use **Go** (version 1.22 or newer) for all FlowAI backend services. All Go services
SHALL use the platform service baseline:

- Repository layout follows the common `golang-standards/project-layout` shape,
  especially `cmd/`, `internal/`, `configs/`, `scripts/`, `deployments/`, and
  migration directories where applicable.
- HTTP servers use the standard `net/http` server with `chi` routing.
- Every service exposes platform probes `GET /v1/livez` and `GET /v1/readyz`.
- Logging uses the standard library `slog` with JSON output.
- Configuration is YAML-first, with environment variable overrides for
  deployment-specific values.
- PostgreSQL access uses `pgx` and generated query code from `sqlc`.
- Database migrations use `goose`.

## Rationale

- **Single static binary** matches the deployment model: one container per
  service, no runtime to install inside.
- **First-class Docker integration** via the official Docker Engine SDK for Go —
  the same library Docker itself uses internally.
- **Built-in concurrency** (goroutines, channels) fits the polling/observation
  patterns in v0001.
- **Strong static typing** and a mature test ecosystem.
- **Small memory footprint, predictable GC** — appropriate for long-running
  daemons.
- **Neighborhood fit**: every neighboring project (Docker, Kubernetes, Prometheus,
  Loki, Temporal, etcd) is Go.
- **Standard HTTP stack**: `net/http` plus `chi` keeps services idiomatic, small,
  and compatible with standard middleware.
- **Database repeatability**: `goose` migrations plus `pgx`/`sqlc` gives
  versioned schema changes and typed SQL without ORM behavior.
- **Operational consistency**: YAML config, JSON `slog`, and `/livez`/`/readyz`
  probes make all backend services observable and deployable the same way.

## Consequences

Positive:

- Consistent backend stack across Executor, Router, State Registry, Env Registry,
  API Gateway.
- Official Docker SDK is the best-maintained and most feature-complete client
  available.
- Single-binary deployment is easy to roll out, roll back, and scan for CVEs.
- Shared layout and dependencies reduce service-by-service decisions.

Negative:

- The Web UI is still TypeScript/Node; Go is not a fit for the frontend. Two
  languages in the repo.
- Go's error-handling verbosity (`if err != nil`) is real and accumulates.
- Goroutine leaks are easy to introduce; must enforce structured cancellation
  and context propagation.
- The service baseline is opinionated and may be heavier than a toy single-file
  Go service.

## Alternatives considered

- **Rust**: Similar performance/safety, weaker Docker SDK ecosystem, steeper
  learning curve.
- **TypeScript/Node.js**: Familiar but heavier runtime, weaker Docker
  integration, more complex production tuning.
- **Python**: Excellent for agent runtime and LLM ecosystem, but poor fit for
  long-running daemons with strict latency.
- **Java/Kotlin (JVM)**: Mature ecosystem, heavier memory, longer cold start,
  more operational complexity.

## References

- ADR-0002 (Docker client integration) depends on this decision.
- `golang-standards/project-layout` documents common `cmd/`, `internal/`,
  `configs/`, and supporting directories.
- `chi` documentation shows idiomatic `net/http` router and middleware
  composition.