# ADR-0002: Docker Executor integrates via the official Docker Engine SDK for Go

- **Status**: Accepted
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

## Context

The Docker Executor must: pull OCI images, create/start/stop/remove containers
with labels, observe container state (running, exited, dead), list containers
filtered by label (`flowai.executor_id=<id>`), and stream container events.

## Decision

Use the **official Docker Engine SDK for Go** (`github.com/docker/docker/client`)
over the Docker daemon UNIX socket. The socket path is configurable via
`DOCKER_SOCKET_PATH` (default `/var/run/docker.sock`). All Docker calls go
through a thin internal interface so the SDK can be mocked in unit tests.

## Rationale

- **Official library**: same library that Docker itself ships and uses internally.
- **Type-safe Go bindings**: no JSON parsing at the call site; compile-time checks.
- **Native streaming**: the events API streams over the same connection.
- **Label-based filtering**: `--filter label=key=value` is a first-class API in the
  SDK.
- **Configurable transport**: works over UNIX socket OR TCP; remote Docker hosts
  become possible later without code changes.
- **Testability**: wrap the SDK behind an internal interface; mock it in unit
  tests; use real Docker only in integration tests.

## Consequences

Positive:

- No shell-out, no string parsing, no `exec.Command("docker", ...)`.
- Native streaming for container events.
- Same library that Docker itself ships.
- Mockable behind an internal interface, keeping unit tests fast and
  deterministic.

Negative:

- Tightly coupled to Go (acceptable — ADR-0001 chose Go).
- Library API surface is large; the internal interface must be designed
  carefully to avoid leaking SDK types into business logic.
- SDK follows Docker daemon versioning; pinning the daemon version in test
  environments is advisable.

## Alternatives considered

- **CLI shell-out (`docker run`, `docker ps`)**: Trivially simple, but slow,
  error-prone, awkward for streaming, untestable without a real binary.
- **Direct HTTP REST**: Lower-level control, but reinvents what the SDK already
  provides and loses type safety.
- **Containerd client**: Lower-level than Docker; aligned with Kubernetes. Not
  needed for v0001.

## References

- ADR-0001 (Go) is the precondition for this decision.
- v0001-executor-docker design.md "Components" and "Container Lifecycle"
  sections.