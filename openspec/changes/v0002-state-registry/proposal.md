# Change: v0002-state-registry

## Why

FlowAI needs one durable, tenant-isolated source of truth before work can be executed. Tasks arriving from Jira listeners, webhooks, CI integrations, and future event listeners must be recorded once for the authorized team before an Executor from that same team can see them. The same service must preserve team-owned assignments, events, environment definitions, encrypted secrets, audit entries, and operator controls without separate Router or Env Registry services.

The State Registry owns the only mutable projection of task and Executor state. The authoritative state is reconstructed from an immutable, ordered event log so audit, replay, and external reconciliation stay deterministic across restarts and concurrent writers. Capacity belongs to the Executor: the Registry records `max_capacity` and `running_count` as observations only and never gates discovery or approval on capacity, so the platform does not couple scheduling correctness to runtime capacity bookkeeping.

State Registry persists teams, tasks, Executors, assignments, lifecycle events, team-owned environment definitions and secrets, encrypted secret versions, audit, and controls in a normalized third normal form (3NF) PostgreSQL schema. The immutable `team_id` is the canonical authorization and ownership boundary. `team_name` is display and audit context only and never participates in authorization. Each domain entity lives in its own table, non-key attributes depend on the whole key, and foreign keys express and enforce team ownership without transitive redundancy.

## What Changes

- Add State Registry as the canonical tenant-isolated task-ingestion, task-state, assignment, and history service.
- Require every listener submission to carry exactly one immutable `team_id`, and require State Registry to verify that `team_id` against the authenticated listener service identity before persistence or acknowledgement.
- Bind each listener and Executor service identity to exactly one authorized team; caller-supplied headers, payload fields, and `team_name` never establish authorization.
- Deduplicate listener retries by `(team_id, source, source_task_id)`, allowing the same source identity to use the same source task identifier independently in different teams.
- Define the task lifecycle as `created -> dispatched -> running -> finished | failed`, where every transition is an immutable task event with `occurred_at`, an `event_id`, and an `event_type`.
- Require State Registry to emit the `created` event on successful ingestion and the `dispatched` event on atomic approval; the assigned Executor SHALL emit `running`, `finished`, and `failed`.
- Require State Registry to persist canonical state and the immutable event log transactionally in PostgreSQL 3NF; current state is a projection derived deterministically from accepted ordered events.
- Require each task to declare exactly one required tag and exactly one immutable owning `team_id`.
- Require each Executor to register exactly one immutable owning `team_id`, exactly one authorized tag, identity, Executor type, runtime metadata, observed `max_capacity`, and observed `running_count` with the State Registry.
- Require discovery and approval to match both the authenticated Executor's `team_id` and its single registered tag; capacity remains read-only and non-reserving with respect to Registry decisions.
- Require State Registry to reject invalid lifecycle transitions and every new lifecycle event whose `(occurred_at, event_id)` tuple is not strictly greater than the latest accepted same-task tuple, without appending an event or changing projected state. No `accepted_sequence` recovery override is part of this contract.
- Guarantee deterministic read ordering of accepted events by `(occurred_at ASC, event_id ASC)`, with `event_id` as the stable monotonic tie-breaker and idempotency key.
- Require State Registry to store `max_capacity` and `running_count` only as observations; approval MUST succeed for an otherwise eligible same-team, same-tag Executor regardless of capacity and MUST return `200 approved` with a dispatched event.
- Guarantee that exactly one same-team, same-tag Executor receives approval for a task; competing approvals fail without starting duplicate work and without appending additional events.
- Persist task events and Executor self events as separate idempotent streams, with `executor_events.executor_id NOT NULL` for every Executor self event.
- Persist normalized team ownership: `teams` owns display metadata, `tasks`, `executors`, `environment_definitions`, and `secrets` each belong to one team, `secret_versions` reference `secrets`, and child rows inherit and enforce the owning team through foreign keys or equivalent composite constraints.
- Store team-owned Executor environment definitions and team-owned secrets with OpenBao-encrypted, immutable secret versions. Same-team operators manage them; project and task scope restrict applicability within the team and do not grant cross-team access.
- Open decrypted environment values only to the assigned same-team Executor presenting a valid team-bound scope token containing `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience`, `issued_at`, `expiry`, and `key_id`.
- Sign every open-environment scope token with an allow-listed HMAC algorithm (`HS256`, `HS384`, or `HS512` — the "HMAC-SHA-256 or a stronger HMAC" family) using a State Registry-controlled key selected by `key_id`. Bind each token to a protected-header `kid`, a payload `key_id` that MUST equal the header `kid`, an `issued_at` whose premature tolerance is `issued_at <= server_now + 30 seconds`, an `expiry` strictly later than `issued_at` and no greater than five minutes after `issued_at`, and the literal `audience` identifier `state-registry.environment.open`. Verify the `kid`/`key_id` equality before any MAC computation, recompute the signature under the declared allow-listed algorithm and the server-side key handle, and compare it with a constant-time comparison before any other check; accept only tokens whose `key_id` falls within a documented active key window for rotation; never log token plaintext, claims, or key material. The token is valid only with the authenticated assigned same-team Executor mTLS identity, only while the referenced task is in a non-terminal state, and only while the Executor remains the assigned Executor; replay by another identity, or by the assigned Executor after terminal state or unassignment, is denied before any OpenBao operation. A retry within the token TTL by the same assigned same-team Executor MAY be allowed, but it SHALL NOT bypass canonical claim, transition, or assignment checks.
- Accept operator reads and writes only through a trusted API Gateway service identity with verified `operator_id`, `team_id`, and `request_id` context; the optional display-only `team_name` is forwarded only when the verified operator team carries one. State Registry authorizes solely with `team_id`; `team_name` is display and audit context only and its absence never weakens authorization.
- Apply the authenticated `team_id` filter before pagination, totals, counts, aggregations, and WebSocket subscription or fan-out. WebSocket frames SHALL never cross team boundaries; this is a review-blocking isolation invariant.
- Return a non-revealing `404` for foreign-team task, Executor, environment, secret, control, event, or audit identifiers and perform no mutation, event append, audit disclosure, count change, or subscription side effect.
- Record audit entries with `team_id`, actor, action, resource, `request_id`, and outcome while excluding secret plaintext and other decrypted values.
- Remove Router from the planned architecture; no separate broker, queue service, or Router-owned state remains.
- Remove Env Registry from the planned architecture; all registry-owned durable state lives in State Registry.

## Tenant Assumptions

- Every task belongs to exactly one immutable team for its lifetime.
- Every Executor service identity belongs to exactly one immutable team and registers exactly one tag.
- Every authenticated operator request context represents exactly one `operator_id` and one `team_id` and may carry `team_name` only as optional non-authoritative display and audit context.
- Team provisioning and membership resolution occur outside State Registry. `v0006-web-ui` may bootstrap a single team initially, and `v0007-auth` is expected to supply verified operator team claims, but State Registry still validates trusted Gateway context and authorizes only by immutable `team_id`.
- Project and task scopes narrow the applicability of team-owned environments and secrets; they do not create a second authorization hierarchy.

## Impact

- Change type: development
- Affected specs: `specs/state-registry/spec.md`, `specs/executor/spec.md`
- Affected ADRs: `specs/adrs.md`
- Affected diagrams: `specs/diagrams/state-registry-c4-container.puml`, `specs/diagrams/state-registry-services-sequence.puml`, `specs/diagrams/state-registry-api-sequence.puml`, `specs/diagrams/state-registry-task-state-machine.puml`, `specs/diagrams/state-registry-executor-capacity-state-machine.puml`
- Affected test cases: `specs/test-cases/`
- Affected code: future `state-registry/` service and Executor clients
- Affected OpenAPI: `specs/openapi/state-registry.openapi.yaml`
- Superseded draft: the unimplemented standalone Env Registry proposal is preserved at `openspec/changes/archive/2026-07-14-v0003-env-registry/`; its delta specs were not synced because v0002 absorbs that ownership before implementation.
- This contract revision reconciles the proposal, design, capability deltas, proposed ADRs, diagrams, OpenAPI surface, E2E test definitions, and implementation tasks for the v0002-state-registry tenant model, including the allow-listed HMAC scope-token security requirement. Runtime implementation of the State Registry service and matching Executor clients remains future work.

## Non-Goals

- Execute tasks or control Executor runtimes.
- Implement Web UI, API Gateway, user authentication, team CRUD, or team-membership management UI in this change.
- Add project-level RBAC, project membership, cross-team sharing, cross-team task movement, or multi-team operator/Executor contexts.
- Treat `team_name`, untrusted client headers, or caller-selected tenant fields as authorization evidence.
- Add priorities, wildcard tags, tag expressions, automatic reassignment, or global scheduling optimization.
- Enforce, validate, or centrally optimize Executor capacity from State Registry; capacity remains a local Executor concern.

## Explicit diagram requests

- Type: state-machine
  Request: "Add task-state and Executor-capacity state-machine diagrams."
