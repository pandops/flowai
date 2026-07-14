# AGENTS.md — FlowAI Platform

> **Planning-phase architecture repository.** No runtime service is implemented
> yet. Current-state files describe only what exists now; planned behavior lives
> under active OpenSpec changes until implementation, verification, acceptance,
> and sync.
>
> **Service catalog index.** Per-service planned target descriptions (purpose,
> responsibilities, will-not-do) live in OpenSpec change artifacts under
> `openspec/changes/*/specs/` until accepted into real state. This file is the

## Repository layout convention: one directory per service, no shared code

Each runtime service in this repository MUST live under its own top-level
directory named after the service. The directory is fully self-contained:
every package, binary, config, migration, and test lives inside it. **No
shared code between services** — only third-party libraries. Cross-cutting
concerns (HTTP scaffolding, logging, wire types, persistence) are duplicated
per service so each service can evolve independently.

```
mocked-task-server/               # v0001 Mocked Task Server (a separate service)
├── cmd/mocked-task-server/
├── configs/mocked-task-server.yaml
├── internal/
│   ├── config/                   # YAML loader (this service only)
│   ├── httpapi/                  # chi scaffolding (this service only)
│   ├── logging/                  # slog setup (this service only)
│   ├── platform/                 # wire types (this service's view of the contract)
│   ├── server/                   # the HTTP server implementation
│   │   ├── server.go
│   │   └── server_test.go
│   └── store/                    # ephemeral in-memory mocked service state
└── test/                         # integration tests for this service

executor-docker/                  # v0001 Docker Executor
├── cmd/docker-executor/
├── configs/docker-executor.yaml
├── internal/
│   ├── config/                   # YAML loader (this service only)
│   ├── httpapi/                  # chi scaffolding (this service only)
│   ├── logging/                  # slog setup (this service only)
│   ├── platform/                 # wire types (this service's view of the contract)
│   ├── dockerclient/             # Docker REST client (executor-only)
│   ├── openhands/                # OpenHands REST client (executor-only)
│   ├── mockedclient/             # Mocked-task-server HTTP client (executor-only)
│   ├── executor/                 # Core Executor logic + state machine
│   │   ├── executor.go
│   │   ├── executor_unit_test.go # Pure unit tests (config validation, etc.)
│   │   └── ...
│   └── mocks/                    # Test fakes for executor-only interfaces
│       ├── docker/
│       └── openhands/
└── test/                         # integration tests for this service

autotest/                         # Cross-service Playwright e2e tests (root only)
└── (placeholder for v0006+; v0001 has no UI yet)

docs/adr/                         # Architectural Decision Records
```

Rules:

1. **No shared code between services.** Each service duplicates cross-cutting
   concerns (HTTP scaffolding, logging, wire types, persistence) inside its
   own `internal/`. Wire types are duplicated per service because each service
   owns its view of the contract — the OpenAPI specs are the single source of
   truth and each service defines Go types that match them.
2. **Per-service boundaries.** Service-specific code (interfaces that the
   service uses, clients to external systems, test fakes of those clients,
   database migrations) lives under that service's directory. Go's `internal/`
   rule means each service's internals can only be imported by packages
   sharing the same prefix — this is intentional and prevents cross-service
   coupling.
3. **Test fakes follow the package they fake.** A test fake for an
   executor-only interface (e.g. `dockerclient.Client`) goes under
   `executor-docker/internal/mocks/`. Test fakes for a shared component (e.g.
   the mocked task server, which is itself a service) live with that service.
4. **Per-service `test/` directory.** Unit and integration tests for a
   single service live under `<service>/test/`. These tests use only the
   service's own packages + stdlib + in-process fakes. They do NOT import
   packages from other services (Go's `internal/` rule prevents this anyway).
5. **Cross-service e2e tests live in `autotest/` at the root.** Tests that
   span more than one service — typically Playwright e2e tests — live at
   `autotest/` in the repo root, NOT inside any service directory. They
   drive running services via their public HTTP/CLI surfaces.
6. **One `go.mod` at the root.** The whole repository is a single Go module
   (`github.com/flowai/platform`); service directories are organization only.
7. **Future services follow the same pattern.** When
   v0005-executor-k8s and later services land, they each get their own
   top-level directory (for example, `executor-k8s/`). Each is fully
   self-contained. No Router service is planned; durable task intake,
   deduplication, tag-based discovery, start approvals, and assignments belong to State Registry.
8. **The v0001 mocked task server is stateless.** Its legacy task, state, and
   environment surfaces keep ephemeral in-memory data only. Durable
   PostgreSQL persistence belongs to the real State Registry introduced by
   `v0002-state-registry`, not to `mocked-task-server/`.
> cross-cutting view: connection matrix, state ownership, cross-cutting rules,
> and the diagram map.
>
> Last synced with architecture topology on this branch: yes (see git log).

## OpenSpec management rules

- `openspec/specs/` is the canonical real-state baseline for behavior that has
  been implemented, verified, accepted, and synced.
- `openspec/changes/*/specs/` contains proposed spec deltas.
- `openspec/changes/*/specs/diagrams/` contains proposed `.puml` diagrams for a
  change.
- `docs/adr/` contains accepted Architectural Decision Records: why current
  architecture choices were made.
- `docs/architecture/diagrams/` contains `.puml` diagrams for implemented
  current state only. It stays empty when no current implementation exists.
- `openspec/changes/*/tasks.md` contains implementation steps for a change.
- Do not treat a spec requirement as implemented merely because it is in the
  baseline; implementation status requires code and verification artifacts.
- Validate OpenSpec before implementation and before completion:
  `npx -y @fission-ai/openspec@1.5.0 validate <change-id>` for changes and
  `npx -y @fission-ai/openspec@1.5.0 validate --all` for the full baseline.
- Planned diagrams move with the change they describe. Sync diagrams to
  `docs/architecture/diagrams/` only after the change is implemented and
  accepted as current state.

## Target service catalog

Each planned target service has an OpenSpec file inside an active change. Click through for the full purpose / responsibilities / will-not-do.

- [**Docker Executor**](openspec/changes/v0001-executor-docker/specs/executor/spec.md) — first local worker type; controls only Docker container lifecycle for agent runtimes.
- [**State Registry**](openspec/changes/v0002-state-registry/specs/state-registry/spec.md) — main source of truth; immutable team ownership, durable listener task intake, team-scoped deduplication, same-team exact-tag discovery, atomic start approvals, assignments, strictly ordered events, team-owned environment definitions, logical secrets with encrypted immutable versions, controls, and audit.
- [**K8s Executor**](openspec/changes/v0005-executor-k8s/specs/executor/spec.md) — cluster worker type; controls only Kubernetes Pod lifecycle for agent runtimes.
- [**Web UI**](openspec/changes/v0006-web-ui/specs/web-ui/spec.md) — thin frontend, sole surface for human operators, initially without auth.
- [**API Gateway**](openspec/changes/v0006-web-ui/specs/api-gateway/spec.md) — Web UI's sole backend, initially no-auth; auth is added by [**Auth**](openspec/changes/v0007-auth/specs/auth/spec.md).

## Planned final connection matrix (who calls whom)

This matrix describes the target after the numbered Executor, registry,
Web UI, and Auth changes land. `v0001-executor-docker` uses a mocked task server
for its original bootstrap contract; v0002 replaces that task path with the
durable State Registry contract.

| Caller ↓ / Callee → | Web UI | API Gateway | State Registry | Executor | Automation/listeners |
|---|---|---|---|---|---|
| **Web UI** | — | HTTPS, WS; sole operator backend | ❌ no | ❌ no | n/a |
| **API Gateway** | via WS proxy | — | REST and WS with Gateway-verified `operator_id`, immutable `team_id`, and `request_id`; optional display-only `team_name` when the verified operator team carries one | ❌ no | n/a |
| **State Registry** | n/a | REST/WS responses to trusted proxied requests | — | REST responses for one-team registration, same-team discovery/approval/events/controls/open | REST responses for team-bound task ingestion |
| **Executor** | n/a | ❌ no | REST: register one immutable team + one tag; discover same-team exact-tag tasks; request approval; append task/self events; read assigned controls; open team-bound environments | — | n/a |
| **Automation/listeners** | n/a | ❌ no (auth only) | REST: persist externally sourced tasks with authorized immutable `team_id` before acknowledgement | n/a | — |

**Hard final topology and tenancy rules (encoded in active OpenSpec change files):**

- The **Web UI** talks ONLY to the **API Gateway**.
- The **API Gateway** talks ONLY to the **State Registry**. It checks the operator's canonical one-team membership and forwards trusted `operator_id`, immutable `team_id`, and `request_id`; the optional display-only `team_name` is forwarded only when the verified operator team carries one. State Registry authorizes with `team_id` only, never with `team_name`.
- No **Router** or separate task broker is planned.
- No **Env Registry** or separate environment/secret service is planned.
- **Executors** talk only to State Registry. Each Executor service identity and process belongs to exactly one immutable team and registers exactly one tag. Discovery is same-team plus exact-tag; approval, task/self events, assigned control reads, and open-environment access are same-team operations. Executors do not talk to API Gateway or Web UI.
- **Automation and event listeners** authenticate as one authorized team and persist tasks in State Registry before acknowledging ingestion. Dedupe uses `(team_id, source, source_task_id)`. Listeners do not talk to Web UI or Executors.

## Planned final state ownership matrix

| Concern | Owner | Notes |
|---|---|---|
| Team identity and display metadata | **State Registry** | `teams.team_id` is immutable canonical ownership; `team_name` is display-only and never authorizes |
| Incoming task records | **State Registry** | Each task belongs to exactly one immutable team; listeners are team-bound; persistence and `created` append precede acknowledgement; dedupe key is `(team_id, source, source_task_id)` |
| Created (discoverable) task list | **State Registry** | Only same-team `created` tasks with exact equality to the Executor's one registered tag; team/tag predicates precede pagination and counts; foreign-only matches yield `204` or empty without metadata |
| Dispatched and processing task state | **State Registry** | Same-team approval and canonical lifecycle (`created` -> `dispatched` -> `running` -> `finished` \| `failed`) |
| Executor registry | **State Registry** | Each Executor has one immutable `team_id`, one tag, `executor_type`, identity, and lifecycle; foreign point identifiers are non-revealing `404` |
| Executor `max_capacity` and `running_count` observations | **State Registry** | Stored as informational observations; the Registry SHALL NOT gate discovery or approval and SHALL NEVER emit capacity-based rejection |
| Local capacity enforcement | **Each Executor** | The Executor alone decides when to discover, request approval, and start a runtime/Pod based on local observation |
| Task assignment (which Executor runs which task) | **State Registry** | Atomic same-team, same-tag approval; exactly one winner; `tasks.executor_id` is set on dispatch and immutable thereafter; foreign task IDs are non-revealing `404` |
| Task records (canonical history) | **State Registry** | Immutable `team_id`, status, timestamps, parameters, assignment, and immutable event log |
| Event log (per-task events) | **State Registry** | Team-owned, historical, and replayable; accepted events are strictly ordered by `(occurred_at, event_id)` with no `accepted_sequence` override |
| Executor-emitted task events (`running`, `finished`, `failed`) | **Assigned Executor** | Accepted only from assigned same-team Executor; non-null `executor_id`; retries dedupe by `(task_id, event_id)` |
| Executor self events | **State Registry** | Team-owned `executor_events` rows have `executor_id NOT NULL`; latest capacity observations mirror onto the same-team Executor row transactionally |
| Team-filtered reads and WebSocket subscriptions | **State Registry** | Trusted `team_id` predicate applies before pagination, cursors, totals, counts, aggregates, replay, fan-out, and frame serialization |
| Audit trail | **State Registry** | Immutable, plaintext-free, and team-scoped; reads filter by trusted `team_id` before shaping |
| Operator control requests | **State Registry** | Trusted Gateway context and target task must share `team_id`; audit-backed requests are read only by the assigned same-team Executor |
| Environment definitions | **State Registry** | Operators register and manage non-secret definitions for their own team; project/task scope only narrows applicability within that team |
| Logical secrets and encrypted secret versions | **State Registry** | Team-owned `secrets` rows reference same-team environments; immutable ciphertext `secret_versions` rows reference logical secrets; operator writes are same-team only |
| Open-environment authorization | **State Registry** | State Registry is the sole issuer and verifier of open-environment scope tokens. It signs each token with an allow-listed HMAC algorithm (`HS256`, `HS384`, or `HS512` — the "HMAC-SHA-256 or a stronger HMAC" family) under a server-controlled rotating key identified by `key_id`. The compact three-part signed token `<header>.<payload>.<signature>` is carried only in the `X-FlowAI-Scope-Token` request header for `GET /v1/environments/{environment_id}/open?task_id={task_id}`; the protected header carries `alg` (allow-listed), `kid`, and `typ = scope-token+json`, and the payload carries `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry` (`expiry > issued_at`, `expiry - issued_at <= 5 minutes`), and `key_id`. State Registry verifies that the protected-header `kid` equals the payload `key_id` before any MAC computation, then recomputes the signature under the declared allow-listed algorithm using the server-side key handle and compares it under constant-time comparison, then the canonical claim shape, the `key_id` active window, the `issued_at <= server_now + 30s` and `expiry <= issued_at + 5 minutes` window, the expected `audience`, every claim, the authenticated Executor team, same-team assignment, project/task applicability, and non-terminal task state, before any OpenBao operation. Any invalid or unavailable condition — including missing token, tampered MAC, algorithm outside the allow-listed set, `kid` mismatch with payload `key_id`, `key_id` outside the active window, lifetime exceeding five minutes, expired or premature tokens, audience or canonical-claim mismatch, mismatched `team_id`, terminal task, or not-assigned caller — returns the same non-revealing `404 environment_unknown_or_unavailable` shape with zero OpenBao calls and no token plaintext, individual claim values beyond identifier-level metadata, MAC bytes, key material, or derived key bytes in logs, audit entries, or error responses. A same assigned identity retry within TTL succeeds only when every check still passes; it never bypasses canonical claim, transition, or assignment checks, never extends TTL, and never revives an expired token. |
| Bearer tokens, sessions, and operator team verification | **API Gateway** | Stateless per request; verifies one canonical operator team and forwards trusted `operator_id`, immutable `team_id`, and `request_id`; forwards optional display-only `team_name` only when the verified operator team carries one; never grants authority from `team_name` |
| UI state (UI prefs, sessions) | **Client-only** | The Web UI maintains its own; no server-side state |

**The State Registry is the main source of truth for durable task intake, discovery, start approvals, assignments, platform history, environment definitions, and encrypted secret versions.**
**The API Gateway is the auth-aware reverse proxy (no persistent state).**

## Cross-cutting rules

The platform treats team tenancy as the cross-cutting axis that bounds every interface below. Every service-level rule in this section is enforced by State Registry against canonical immutable `team_id`; no service-level rule is allowed to grant authority from `team_name`, caller-supplied tenant headers, or unauthenticated payload fields.

### What "the Web UI talks to the API Gateway only" means in practice

- The Web UI has a single base URL: the API Gateway. There is no other base URL configured.
- The Web UI's HTTP client refuses to send requests to any origin other than the API Gateway. (Defense in depth: even a bug in the UI cannot leak.)
- The Web UI does not use any background process that calls a backend service.

### What "the API Gateway forwards canonical team context" means

- Every operator request that reaches State Registry is forwarded by the API Gateway with a verified one-team context containing `operator_id`, immutable `team_id`, and `request_id`. The optional display-only `team_name` is forwarded only when the verified operator team carries one. The Gateway establishes the operator's canonical team from its bearer/session credential; the Gateway never grants authority from `team_name`, and the absence of `team_name` never weakens State Registry authorization anchored to `team_id`.
- An operator whose session does not correspond to exactly one canonical team is rejected at the Gateway. Multi-team or unknown operator contexts never reach State Registry.
- The Gateway is the sole origin State Registry trusts for operator reads, subscriptions, controls, environment writes, and secret writes; direct clients that copy operator or team headers without the Gateway service identity are rejected before any protected read or write.

### What "the Web UI talks to the API Gateway only" and "no Router" mean together

- The platform deploys no separate task broker, queue manager, or Router address.
- Listener ingestion, team-scoped task deduplication, same-team exact-tag discovery, and atomic same-team start approval are State Registry responsibilities; no Executor may start without approval.
- WebSocket frames from the UI that the Gateway proxies bind at upgrade to the verified immutable `team_id` and only emit frames for resources owned by that team. The Gateway's WS connection never forwards state for another team.

### What "there is no Env Registry" means

- State Registry persists environment definitions, encrypted secret versions, scope metadata, and access audits in its own PostgreSQL database using normalized team-owned tables.
- Operators register, read, update, and delete environment definitions and logical secrets only for their own team through the Web UI and API Gateway. The Gateway and State Registry authorize with `team_id` only; `team_name` is display-only and never widens access.
- Project and task scope metadata narrow the use of an environment or secret inside the same team and never grant cross-team access.
- Only the assigned same-team Executor may open task-scoped values through `GET /v1/environments/{environment_id}/open?task_id={task_id}` with the compact signed scope token carried in the `X-FlowAI-Scope-Token` request header. State Registry signs each token with an allow-listed HMAC algorithm (`HS256`, `HS384`, or `HS512`) under a server-controlled rotating key identified by `key_id`. The protected header carries `alg` (allow-listed), `kid`, and `typ = scope-token+json`; the token carries `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry` (`expiry > issued_at`, `expiry - issued_at <= 5 minutes`), and `key_id`. State Registry verifies that the protected-header `kid` equals the payload `key_id` before any MAC computation, then recomputes the signature under the declared allow-listed algorithm using the server-side key handle and compares it under constant-time comparison, then the canonical claim shape, the `key_id` active window, the `issued_at <= server_now + 30s` and `expiry <= issued_at + 5 minutes` window, the expected `audience`, every claim, the authenticated Executor team, same-team assignment, applicability, and non-terminal task state before any OpenBao operation. Any invalid or unavailable condition, including a terminal task, another identity, or a not-assigned caller, returns the same non-revealing `404 environment_unknown_or_unavailable` shape with zero OpenBao calls and no token plaintext, individual claim values beyond identifier-level metadata, MAC bytes, key material, or derived key bytes in logs, audit entries, or error responses. A same assigned identity retry within TTL succeeds only when every check still passes; it never bypasses canonical claim, transition, or assignment checks, never extends TTL, and never revives an expired token.
- Secret plaintext is encrypted through OpenBao Transit before persistence, is never logged, never persisted in plaintext, never returned to operators or listeners, and exists only in memory while serving an authorized open-environment response for the assigned same-team task.

### What "Executors belong to one immutable team" means

- Every Executor service identity and process is bound to exactly one immutable authorized `team_id` for its lifetime. The identity cannot omit or change `team_id` on registration, and State Registry rejects mismatched submissions.
- An Executor registers exactly one tag alongside its team. Zero-tag or multi-tag registrations are rejected without persistence.
- Same-team discovery applies both equality predicates (`task.team_id = executor.team_id` AND `required_tag = registered_tag`) before pagination, counts, and cursors. Foreign task identifiers are non-revealing `404`; collection discovery returns `204` or an empty collection when only foreign tasks match.
- Atomic approval runs in one transaction that sets immutable same-team `tasks.executor_id`, records `approved_at`, appends a Registry-owned `dispatched` event with `executor_id = NULL`, and removes the task from same-team discovery. Foreign task identifiers are non-revealing `404`; later eligible same-team competitors receive `409 task_already_dispatched`.
- Executor-emitted task events (`running`, `finished`, `failed`) are accepted only from the assigned same-team Executor, with non-null `executor_id`, and are appended once by `(task_id, event_id)`. Each fresh event's `(occurred_at, event_id)` tuple must be strictly greater than the latest accepted tuple; `accepted_sequence` and every recovery override are rejected.
- Executor self events (`executor_events.executor_id NOT NULL`) carry the Executor's same-team ownership and mirror the latest `max_capacity` and `running_count` observations transactionally. Observations are informational only; State Registry never gates discovery or approval on them and never emits capacity-based rejection. Local capacity enforcement lives only in the Executor.

### What "listeners persist team-bound tasks in State Registry" means

- Jira listeners, webhooks, CI, and other automation submit each external task to State Registry before acknowledging the source event.
- Every submission carries an immutable authorized `team_id`, a stable source identifier, and a stable source task identifier. Dedupe uses `(team_id, source, source_task_id)` so retries return the existing canonical task for that team without creating another task or resetting state/history.
- A listener identity is authorized for exactly one immutable `team_id`. A submission that names another team is rejected without persisting or acknowledging the task. An automation writer does not need to know about Web UI or any Executor.

### What "team-filtered reads precede result shaping" means

- State Registry applies the trusted `team_id` predicate before pagination, cursor creation, totals, counts, grouping, aggregation, and result serialization for tasks, Executors, events, controls, environments, secrets, versions, and audits. No page length, total, cursor, aggregate, or empty/non-empty distinction may incorporate rows from another team.
- WebSocket subscriptions accept upgrades only with one verified `team_id`; the connection binds to that team at upgrade and applies the team predicate before replay selection, cursor handling, subscription filters, live fan-out, and frame serialization. Every emitted frame belongs to the bound team; a cross-team frame, count, cursor, or existence signal is a contract violation.

### What "audit is plaintext-free and team-scoped" means

- Every authorized operator action, control, environment or secret mutation, and open-environment access produces an immutable audit entry containing `team_id`, actor identity and type, action, resource type and identifier, `request_id`, outcome, and timestamp.
- Audit entries, application logs, event payloads, and error responses contain no secret plaintext or decrypted environment values. Audit reads filter by trusted `team_id` before pagination or aggregation.

### What "service identities are team-bound" means

- Each listener and Executor identity is bound to exactly one immutable authorized `team_id`. Listener source identifiers and submitted `team_id` are bound to the authenticated listener. Executor registration, discovery, approval, task and self events, control reads, and environment opens are bound to the authenticated Executor identity.
- API Gateway-only reads, subscriptions, environment/secret writes, and controls require the trusted Gateway identity and verified operator context. Untrusted client headers never establish a team.

---

## How diagrams relate to this document

Proposed architecture diagrams (`.puml`) live under `openspec/changes/*/specs/diagrams/`. Current-state architecture diagrams live under `docs/architecture/diagrams/` only after implementation is accepted. If diagrams and specs disagree, **the active OpenSpec change files win for planned service behavior, baseline specs win for current behavior, and this file wins for cross-cutting topology**. Update the relevant spec first, then redraw.

The diagram ↔ catalog map:

| Diagram | What it visualizes |
|---|---|
| `openspec/changes/*/specs/diagrams/*.puml` | Proposed system/design diagrams for a change |
| `docs/architecture/diagrams/*.puml` | Implemented current-state system/design diagrams |
