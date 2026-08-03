# Architecture Decision Records

This directory contains accepted ADRs: the reasons why current architecture
decisions exist.

Accepted ADRs:

- [ADR-0001: FlowAI backend services use Go](0001-go-backend-services.md)
- [ADR-0002: Docker Executor integrates via the official Docker Engine SDK for Go](0002-docker-engine-sdk.md)
- [ADR-0003: v0001 Executor runs the OpenHands agent runtime](0003-openhands-runtime.md)
- [ADR-0004: Executor process runs bounded concurrent containers](0004-bounded-capacity.md)

Planned ADR creation belongs in an OpenSpec change until implemented, verified,
accepted, and synced into this directory.

## State Registry

- [0005-consolidate-durable-team-state-in-state-registry.md](0005-consolidate-durable-team-state-in-state-registry.md)
- [0006-use-immutable-team-identifiers-as-tenant-authority.md](0006-use-immutable-team-identifiers-as-tenant-authority.md)
- [0007-use-event-sourced-task-lifecycle-with-3nf-postgresql.md](0007-use-event-sourced-task-lifecycle-with-3nf-postgresql.md)
- [0008-use-fifo-claim-by-oldest-eligible-task.md](0008-use-fifo-claim-by-oldest-eligible-task.md)
- [0009-use-admin-only-platform-registration.md](0009-use-admin-only-platform-registration.md)
- [0010-use-local-aes-256-gcm-for-secret-values.md](0010-use-local-aes-256-gcm-for-secret-values.md)
- [0011-sign-scope-tokens-with-rotating-hmac-keys.md](0011-sign-scope-tokens-with-rotating-hmac-keys.md)
- [0012-resolve-task-image-with-four-level-precedence.md](0012-resolve-task-image-with-four-level-precedence.md)
- [0013-make-executor-scope-a-registration-time-choice.md](0013-make-executor-scope-a-registration-time-choice.md)
