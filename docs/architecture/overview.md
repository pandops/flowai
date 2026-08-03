# Current architecture overview

FlowAI currently implements two runtime services.

`state-registry` is the durable PostgreSQL-backed source of truth and the
only service allowed to decide task ownership, FIFO assignment, canonical
lifecycle, tenant authorization, environment access, and audit history.

`executor_docker_opehands` is a bounded worker. It claims work from State
Registry, uses the Registry-resolved image and environment, controls the
Docker/OpenHands lifecycle, and reports ordered lifecycle events back.

```text
listeners/admin/Gateway ──mTLS──> State Registry <──mTLS── Docker Executor
                                      │                       │
                                      ▼                       ▼
                                  PostgreSQL              Docker/OpenHands
```

There is no Router, mocked task server, or separate Env Registry. API Gateway
is the planned operator-facing caller and forwards verified context without
owning durable state. Immutable `team_id`, never `team_name`, is tenant
authority.

Normative behavior is in `openspec/specs/`; operational entry points are in
the service READMEs.
