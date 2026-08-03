# ADR: Consolidate durable team-owned platform state in State Registry

### Status

Accepted

### Context

Separate Router and Env Registry services would split canonical team-owned tasks, assignments, environment definitions, and encrypted secrets across avoidable network boundaries and conflicting ownership models. Team isolation needs one transaction and authorization boundary so ingestion, assignment, events, controls, environments, secrets, and audit cannot disagree about ownership.

### Decision

State Registry owns durable team-scoped task intake, deduplication, read-only FIFO task discovery, atomic start claims (replacing atomic approvals), assignments, task and Executor events, environment definitions, logical secrets, encrypted secret versions, audit, controls, admin team registration, admin source-system registration, and admin task-type registration. No Router or Env Registry service is deployed. No Executor or listener may create or update a team; team creation is exclusively an `/admin/teams` responsibility. Executors retain runtime lifecycle and local capacity only; API Gateway remains the sole operator backend. Every durable resource is authorized through immutable `team_id` ownership before domain-specific tag, assignment, project, or task applicability checks. Image strings are not team-owned resources, and image equality or reuse SHALL NOT grant authority.

### Consequences

State Registry has a larger persistence and authorization surface, but the platform has one canonical team-isolation and transaction boundary with no cross-registry consistency problem. This decision supersedes the never-accepted Router dispatch draft formerly tracked as `v0004-router` and the never-accepted, never-implemented standalone Env Registry draft preserved at `openspec/changes/archive/2026-07-14-v0003-env-registry/`; it does not claim that accepted ADRs 0001, 0003, or 0004 established those ownership boundaries. Tenant-isolation defects in this service have a large blast radius, so point reads, collections, counts, aggregations, controls, environment access, audit, and WebSocket delivery all require explicit same-team verification. Team creation is centralized: only authenticated system administrators may create teams, source systems, and task types through `/admin/*` endpoints.

## More Information

Supersedes: `v0002-state-registry`
