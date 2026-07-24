# Proposed ADRs for v0002-state-registry

## ADR: Consolidate durable team-owned platform state in State Registry

### Status

Proposed

### Context

Separate Router and Env Registry services would split canonical team-owned tasks, assignments, environment definitions, and encrypted secrets across avoidable network boundaries and conflicting ownership models. Team isolation needs one transaction and authorization boundary so ingestion, assignment, events, controls, environments, secrets, and audit cannot disagree about ownership.

### Decision

State Registry owns durable team-scoped task intake, deduplication, read-only FIFO task discovery, atomic start claims (replacing atomic approvals), assignments, task and Executor events, environment definitions, logical secrets, encrypted secret versions, audit, controls, admin team registration, admin source-system registration, and admin task-type registration. No Router or Env Registry service is deployed. No Executor or listener may create or update a team; team creation is exclusively an `/admin/teams` responsibility. Executors retain runtime lifecycle and local capacity only; API Gateway remains the sole operator backend. Every durable resource is authorized through immutable `team_id` ownership before domain-specific tag, assignment, project, or task applicability checks. Image strings are not team-owned resources, and image equality or reuse SHALL NOT grant authority.

### Consequences

State Registry has a larger persistence and authorization surface, but the platform has one canonical team-isolation and transaction boundary with no cross-registry consistency problem. This decision supersedes the never-accepted Router dispatch draft formerly tracked as `v0004-router` and the never-accepted, never-implemented standalone Env Registry draft preserved at `openspec/changes/archive/2026-07-14-v0003-env-registry/`; it does not claim that accepted ADRs 0001, 0003, or 0004 established those ownership boundaries. Tenant-isolation defects in this service have a large blast radius, so point reads, collections, counts, aggregations, controls, environment access, audit, and WebSocket delivery all require explicit same-team verification. Team creation is centralized: only authenticated system administrators may create teams, source systems, and task types through `/admin/*` endpoints.

## ADR: Use immutable team identifiers as the tenant authority

### Status

Proposed

### Context

Listeners submit tasks for teams, Executors consume work for teams, and operators manage team-wide environments and secrets through API Gateway. Human-readable team names can change and can collide. Caller-selected headers and payload fields are untrusted. If authorization uses `team_name`, a client-provided tenant hint, or a resource lookup before tenant filtering, the Registry can expose another team's resource existence, counts, history, or secret metadata.

The platform needs one tenant authority that is stable across display-name changes and is carried consistently through ingestion, service identity, assignment, Gateway requests, storage relationships, scope tokens, audit, pagination, aggregation, and WebSocket delivery.

### Decision

`team_id` SHALL be the canonical immutable tenant identifier and authorization authority. `team_name` SHALL be display and audit context only and SHALL NOT grant, select, or broaden access. Each task and team-owned Executor SHALL belong to exactly one team for its lifetime. Each authenticated operator request context SHALL represent exactly one `operator_id` and one `team_id`. Each system-owned Executor (`scope = system`) SHALL have no team binding; it SHALL use the parent task's immutable `team_id` as the team predicate in per-task checks.

Each listener service identity SHALL be bound to exactly one authorized `team_id` and to exactly one authorized `source_system_id`. Every listener task submission SHALL carry that `team_id`, that immutable `source_system_id`, that immutable `task_type_id`, and the external `source_id`, and State Registry SHALL verify them against the authenticated listener before persistence or acknowledgement. Listener deduplication SHALL use `(team_id, source_system_id, source_id)`. A source-system identity SHALL belong to exactly one team; cross-team claims against one source-system identity are rejected without persistence. The opaque listener identity reference SHALL be globally unique across the `source_systems` table so that one listener principal maps to exactly one `(team_id, source_system_id)` pair; a reuse of the same `listener_identity` value under the same team, a different team, or any other variation SHALL be rejected without mutation. The Registry SHALL enforce `(tasks.task_type_id, tasks.team_id) = (task_types.task_type_id, task_types.team_id)` as a database composite foreign key on every task row so a foreign-team task type cannot be cited.

Each Executor service identity SHALL be bound to either exactly one authorized `team_id` (`scope = team`) or to no `team_id` at all (`scope = system`); this choice is registration-time, immutable from registration onward, and recorded on the Executor row. A team-owned Executor SHALL submit its `team_id` on registration, discovery, claim, task events, and self events; State Registry SHALL verify the submitted `team_id` against the authenticated Executor identity, SHALL reject a registration whose `team_id` does not reference an existing team, and SHALL reject a re-registration that omits the team, supplies a different team, or changes the scope. A system-owned Executor SHALL submit `team_id = null` on registration and SHALL set the envelope `team_id` field on every task event and self event to the parent task's `team_id` (the envelope is still required and non-null per task event; the Executor's own `team_id` is null by construction). State Registry SHALL verify the system-owned Executor's task-event envelope `team_id` against the parent task's `team_id` rather than against an Executor's immutable service binding (which is null), and SHALL reject an envelope with a non-null `team_id` for a self event from a system-owned Executor.

Every Executor-emitted task event (`running`, `finished`, `failed`) and Executor self event envelope SHALL carry a non-null `team_id`. State Registry SHALL reject any event whose envelope `team_id` differs from the authenticated Executor's immutable `team_id` with `403 team_mismatch`, SHALL persist the verified `team_id` on the event row alongside the authenticated `executor_id`, and SHALL reject a same-team Executor that is not the current `tasks.executor_id` with `403 not_assigned`. A foreign task or Executor point identifier SHALL receive the same non-revealing `404` shape used for an unknown identifier. The Registry-emitted first `created` event on a successful claim SHALL inherit the parent task's immutable `team_id` and SHALL be appended in the same transaction that sets `tasks.executor_id` and `tasks.owner_command_id`; its `executor_id` SHALL be non-null and equal to the claiming Executor, and the Executor SHALL NOT separately emit a `created` event.

Operator State Registry requests SHALL require a trusted API Gateway service identity and Gateway-verified context containing `operator_id`, `team_id`, and `request_id`. The optional display-only `team_name` SHALL be forwarded only when the verified operator team carries one. State Registry SHALL authorize only with `team_id`; the absence of `team_name` SHALL NOT weaken authorization anchored to `team_id`. Direct clients and caller-supplied tenant headers SHALL NOT establish operator context. Admin endpoints under `/admin/*` SHALL require an authenticated system-administrator identity and SHALL NEVER be reachable through a listener, Executor, or operator path; the Registry does not define the mechanism for that authentication beyond requiring the identity.

The `teams` table SHALL own immutable `team_id`, a REQUIRED opaque `default_image` (set at registration and immutable thereafter through any admin or operator path), and display metadata. `tasks`, `executors`, `environment_definitions`, `secrets`, `source_systems`, and `task_types` SHALL each reference one team when team-owned. Child records SHALL inherit and enforce parent team ownership through matching composite foreign keys or an equivalent database-enforced relationship when `team_id` is non-null. `secret_versions` SHALL reference `secrets`; it SHALL NOT duplicate logical secret identity or permit cross-team reparenting.

Environments and secrets SHALL be managed by same-team operators. Project and task scopes SHALL narrow applicability only within the owning team and SHALL NOT act as project RBAC or cross-team authority. A scope token used by an assigned Executor SHALL bind `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry`, and `key_id`, and every claim SHALL be checked against canonical records before any decrypt operation.

Every team-scoped collection or history query SHALL apply the trusted `team_id` predicate before pagination, cursor construction, totals, counts, grouping, aggregation, or serialization. Every WebSocket connection SHALL be bound to one trusted Gateway `team_id` at upgrade; replay, filtering, live fan-out, and every frame SHALL remain in that team. Any cross-team WebSocket frame, count, cursor, or existence signal SHALL block review and release. Every cursor emitted for a Gateway paginated read or an admin paginated read SHALL be integrity-protected under a State Registry-controlled HMAC keyed by `key_id`, SHALL be bound to the originating endpoint, the authenticated authorization scope at the time of issue (the trusted Gateway `team_id` plus verified `operator_id` for Gateway cursors; the authenticated system-administrator role for admin cursors), the complete active filter tuple, the deterministic resource-appropriate ordering rule, and the forward page direction. State Registry SHALL verify the cursor integrity and SHALL reject tampered, cross-endpoint, cross-scope, changed-filter, or wrong-direction cursors with the same non-revealing `400 invalid_pagination` response shape before any protected query runs. `limit` values below 1, above the documented maximum (200), non-integer, or otherwise malformed SHALL be rejected with the same `400 invalid_pagination` shape before any protected query runs.

An authenticated caller that names a task, Executor, event, control, environment, secret, secret version, audit, source system, or task type resource owned by another team SHALL receive the same non-revealing `404` used for an unknown identifier. The request SHALL append no domain event, mutate no canonical resource, create no control, disclose no audit row, and perform no decrypt operation.

Audit entries SHALL include `team_id`, actor identity and type, action, resource type and identifier, `request_id`, outcome, and timestamp without plaintext secret or decrypted environment values, without nonce bytes, without ciphertext bytes, without authentication tag bytes, without key material, without derived key bytes, and without individual claim values beyond identifier-level metadata.

Team provisioning and membership resolution SHALL be split between admin and runtime. Only authenticated system administrators SHALL create teams, source systems, or task types through `/admin/*`; no other path creates or updates these records. This change SHALL NOT add team CRUD, a membership-management UI, multi-team Executor or operator contexts, cross-team sharing, task movement between teams, or project-level RBAC. `v0006-web-ui` may bootstrap one team initially, and `v0007-auth` may supply operator team claims, without weakening State Registry's requirement to validate trusted context and authorize by immutable `team_id`.

Open-environment scope tokens SHALL be signed and verified per the "Sign scope tokens with an allow-listed HMAC and rotate server-controlled keys" ADR. State Registry SHALL be the sole issuer and verifier of those tokens, SHALL verify that the protected-header `kid` equals the payload `key_id` before any MAC computation, SHALL recompute the signature under the declared allow-listed HMAC algorithm, SHALL compare it under constant-time comparison, SHALL reject tokens outside the documented active `key_id` window, SHALL reject tokens whose `issued_at` is premature beyond 30 seconds of clock skew, whose `expiry` is not strictly later than `issued_at`, whose `expiry - issued_at` exceeds five minutes, or whose `audience` does not match the expected literal identifier, and SHALL deny tokens presented by an Executor other than the currently assigned Executor for a non-terminal referenced task before any decrypt operation.

### Consequences

Positive consequences:

- Display-name changes cannot change tenant authority or ownership.
- Listener retries deduplicate inside one team without colliding with another team's source identifiers.
- The same team invariant protects REST point reads, collections, audit, controls, environments, secrets, and WebSocket frames.
- Same-tag Executors in different teams cannot discover or claim each other's tasks.
- Database-enforced parent/child ownership reduces the chance that an application query creates a cross-team association.
- Team-bound scope tokens prevent a valid token for one team, task, environment, or Executor from being replayed in another context.
- Required `default_image` at team registration guarantees image resolution at claim.

Negative consequences:

- Every tenant-owned query and relationship must carry or derive `team_id`, which adds composite indexes, foreign-key constraints, and test cases.
- Non-revealing `404` semantics reduce diagnostic detail for callers; trusted operational logs and plaintext-free audit context must carry enough request information for support.
- Team, source-system, and task-type CRUD are exclusively admin endpoints; runtime cannot create these records. Admin onboarding requires system-administrator credentials, which the contract requires but does not specify how to obtain.
- WebSocket isolation requires connection-bound authorization and per-frame safeguards, not only an upgrade-time check.
- Shared image strings across teams are allowed but explicitly carry no authority, so application code must not interpret image equality as team ownership.

## ADR: Use an event-sourced task lifecycle with normalized 3NF PostgreSQL persistence

### Status

Proposed

### Context

The platform needs one authoritative record of every task transition and Executor observation so audit, replay, and external reconciliation stay deterministic across restarts and concurrent writers. Mutable projection rows alone hide the source of truth. Accepting caller-selected recovery sequence values for older lifecycle events would allow an Executor to reorder history or bypass transition validation without a clearly defined authority model.

State Registry must persist team-owned platform state in a schema that survives concurrent appends, supports external read-back in one deterministic accepted order, rejects stale transitions safely, and avoids update anomalies. The durable surface must express ownership cleanly so migrations are straightforward and individual domain entities do not blur into each other.

### Decision

State Registry SHALL define the canonical task lifecycle as `pending -> created -> running -> finished | failed`. The `pending` state SHALL exist as the projected unclaimed state at ingestion with NO lifecycle event appended at that moment; the durable unclaimed task row carries `current_state = pending` and `tasks.owner_command_id = NULL` and `tasks.executor_id = NULL`. A successful claim SHALL append the FIRST lifecycle event `created` with `executor_id = claiming_executor` and a payload meaning `task <task_id> loaded by <executor_id>`; the task SHALL then transition through `created`, `running`, and exactly one of `finished` or `failed`. The `dispatched` lifecycle state SHALL NOT exist; no `created`/`pending` event of name `dispatched` SHALL be appended at any time. The assigned Executor SHALL emit `running`, `finished`, and `failed` events. No other event type belongs to the canonical lifecycle.

`event_id` SHALL be a stable monotonic identifier and the idempotency key. A retry with the same event identifier SHALL return the original acceptance without appending a duplicate. After idempotent retry resolution, every new event's `(occurred_at, event_id)` tuple SHALL be strictly greater than the task's latest accepted tuple. State Registry SHALL reject an older or equal new tuple without appending or changing projected state. The contract SHALL have no `accepted_sequence` field or caller-supplied recovery override. Accepted events SHALL be read by `(occurred_at ASC, event_id ASC)`.

`task_id` SHALL be globally unique. The idempotency key `(task_id, event_id)` SHALL be safe for retry deduplication because `task_id` does not collide with team ownership; `team_id` SHALL also be persisted on every event row and verified against the authenticated Executor before append. `tasks.owner_command_id` SHALL be set once at successful claim and SHALL be immutable from claim onward.

State Registry SHALL reject any event submission or claim whose target transition is not permitted from current state and SHALL append no event and change no projection on rejection. Executor-emitted task events SHALL also require the authenticated assigned Executor and same-team ownership.

State Registry SHALL persist its durable surface in a third normal form PostgreSQL schema. Every domain entity SHALL live in its own table, every non-key attribute SHALL depend on the whole primary key, and transitive dependencies SHALL be removed. Ownership SHALL be expressed through foreign keys without denormalizing mutable display data into child rows.

The Registry SHALL own at minimum `teams`, `executors`, `tasks`, `task_events`, `executor_events`, `environment_definitions`, `secrets`, `secret_versions`, `audit_entries`, `task_control_requests`, `source_systems`, and `task_types`. Normalized relationships SHALL include `teams 1:N executors` (team-owned only), `teams 1:N tasks`, `teams 1:N environment_definitions`, `teams 1:N secrets`, `teams 1:N source_systems`, `teams 1:N task_types`, `executors 1:N executor_events`, `tasks 1:N task_events`, `tasks 1:N task_control_requests`, `source_systems 1:N tasks`, `task_types 1:N tasks`, `environment_definitions 1:N secrets`, and `secrets 1:N secret_versions`.

`tasks.executor_id` SHALL be nullable before claim and immutable from claim onward. `tasks.owner_command_id` SHALL be nullable before claim and immutable from claim onward. `tasks.team_id` SHALL be immutable for the lifetime of the row regardless of Executor scope. `task_events.executor_id` SHALL be non-null for every accepted task event row, including the first Registry-emitted `created` event on claim (whose `executor_id` identifies the claiming Executor but is set by the Registry in the same transaction); `task_events.executor_id` SHALL be non-null for every Executor-emitted `running`, `finished`, or `failed` event; `task_events.team_id` SHALL be non-null on every row and SHALL equal the parent task's immutable `team_id`. The Executor SHALL emit only `running`, `finished`, and `failed` after claim. `executor_events.executor_id` SHALL be `NOT NULL`; `executor_events.team_id` SHALL be `NOT NULL` when `scope = team` and SHALL be `NULL` when `scope = system`. Child rows SHALL inherit and enforce the parent team's ownership through matching composite foreign keys or equivalent database constraints when `team_id` is non-null. The Registry SHALL NOT publish an ER diagram as part of this contract.

Each task SHALL declare exactly one required tag. Each Executor SHALL register exactly one authorized tag. The Registry SHALL store the latest Executor's local observations on `executors`, refreshed by the latest Executor self event in the same transaction that appends to `executor_events`. The Registry SHALL NOT read, compare, evaluate, or enforce capacity during discovery or claim.

### Consequences

Positive consequences:

- The immutable accepted `task_events` log is the authoritative lifecycle record used by audit, replay, projection verification, and external reconciliation.
- `pending` exists at ingestion with no event, matching the FIFO semantics at claim time.
- The first `created` event's `executor_id` is non-null, encoding "loaded by <executor_id>".
- `dispatched` is removed; the canonical lifecycle has no event of that name.
- Invalid or out-of-order lifecycle transitions are rejected without corrupting history.
- Removing `accepted_sequence` eliminates a caller-controlled ordering bypass and keeps recovery outside the normal event-ingestion contract.
- 3NF normalization and same-team foreign-key ownership prevent transitive update anomalies and cross-team child associations.
- A logical `secrets` parent gives immutable `secret_versions` a stable ownership and lifecycle anchor.
- `executor_events.executor_id NOT NULL` makes every self observation attributable to an Executor and its inherited team.
- Exactly-one-tag registration simplifies discovery and eligibility filters.
- Removing capacity from discovery and claim decouples scheduling correctness from runtime bookkeeping.

Negative consequences:

- Producers must generate monotonic event identifiers and submit new lifecycle events in order; stale new events are rejected rather than inserted retroactively.
- Recovery from a lost or delayed transition requires an explicit future recovery design instead of an `accepted_sequence` escape hatch.
- The Registry must enforce `tasks.executor_id` and `tasks.owner_command_id` immutability explicitly because ordinary foreign keys do not provide set-once behavior.
- 3NF and tenant-enforcing relationships require more joins and composite indexes than a denormalized task row.
- Read paths require indexes including `(team_id, task_id)`, `(current_state, required_tag, ingested_at, task_id)`, `(task_id, occurred_at, event_id)`, `(team_id, executor_id, authorized_tag)`, and unique `(team_id, source_system_id, source_id)`.
- Exactly-one-tag registration requires separate Executor processes for different tags.

## ADR: Use FIFO claim-by-oldest-eligible-task dispatch

### Status

Proposed

### Context

Multiple eligible Executors in one team, or several system-owned Executors across teams, may discover `pending` tasks concurrently. Capacity enforcement belongs to each Executor; State Registry must not couple claim correctness to observed capacity. A claim request SHALL target a specific `task_id`, not a class of tasks; it SHALL be idempotent under a `(task_id, command_id)` retry by the same Executor; and the Registry SHALL guarantee that the claiming Executor receives the oldest eligible task to ensure deterministic ordering and to allow Executors to track their last known head of the eligible queue.

If State Registry allowed any eligibility match (for example, "any `pending` task with the right tag"), concurrent Executors could claim different tasks out of insertion order, producing nondeterministic queue heads and forcing every Executor to re-discover to find the head. By enforcing oldest-eligible-task-per-Executor ordering inside the claim transaction, the Registry gives every Executor a single deterministic FIFO view.

### Decision

Task discovery SHALL be a read-only snapshot filtered first by the authenticated Executor's eligibility predicate (`scope = team`: `tasks.team_id = executor.team_id AND required_tag = registered_tag`; `scope = system`: `required_tag = registered_tag` only), then ordered `(ingested_at ASC, task_id ASC)` with `ingested_at` immutable and `task_id` as the deterministic tie-breaker. Eligibility precedes ordering; ordering precedes pagination; counts apply only after both filters. Discovery SHALL NOT reserve, hide, or assign a task, SHALL NOT depend on observed capacity, and SHALL NOT permit any image-based authority from image equality.

Before starting work, an Executor SHALL ask State Registry to claim a specific `task_id` with an immutable `command_id` using `POST /v1/executors/{executor_id}/claim`. State Registry SHALL claim only when the task is `pending`, the task's required tag equals the Executor's single registered tag, the eligibility predicate holds for the Executor's scope, AND the requested task is the oldest currently eligible `pending` task for that authenticated Executor inside the claim transaction. In one transaction, claim SHALL set immutable `tasks.owner_command_id` (from the request body), immutable `tasks.executor_id`, persist `tasks.resolved_image` and `image_source`, set `tasks.claimed_at`, append the FIRST lifecycle event `created` with `executor_id = claiming_executor` and a payload meaning `task <task_id> loaded by <executor_id>`, and remove the task from the requesting Executor's scope of discovery.

The successful requester SHALL receive `200 claimed` with the canonical task, environment reference, team-bound scope token containing `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `key_id`, `issued_at`, and `expiry` with `expiry > issued_at` and `expiry - issued_at <= 5 minutes` and `issued_at <= server_now + 30 seconds`. A retry of the same `(task_id, command_id)` by the same Executor SHALL return the original `200 claimed` response with no new event. A later eligible competitor SHALL receive `409 task_already_claimed`. An Executor that names an older eligible task than the one it requests SHALL receive `409 older_task_must_be_claimed_first` with no mutation and no event. An authenticated Executor naming a foreign-team task SHALL receive the same `404` as an unknown task. The Registry SHALL never return a capacity-related rejection. No error response may change assignment or append an event.

### Consequences

Positive consequences:

- Exactly one eligible Executor claims a task at a time.
- Duplicate execution is prevented even when eligible Executors race on the same discovery snapshot.
- Same tags can be reused safely in different teams without cross-team claim.
- Foreign identifiers reveal no task existence or state.
- Discovery remains non-locking and scales independently from the claim transaction.
- Capacity remains local, so stale observations cannot produce Registry-side scheduling failures.
- The `(task_id, command_id)` retry is idempotent at the protocol level.

Negative consequences:

- Every task start requires an additional State Registry round trip.
- Executors must handle stale results, FIFO `409 older_task_must_be_claimed_first`, normal same-team `409 task_already_claimed`, and non-revealing `404` responses.
- Claim and tenant-filtered FIFO discovery are correctness-critical and require concurrent and cross-team isolation tests.
- An Executor that wins claim while locally full may delay runtime start; local capacity management remains its responsibility.

## ADR: Use admin-only team, source-system, and task-type registration

### Status

Proposed

### Context

Every durable record in State Registry carries an immutable `team_id`. Until now, teams were created implicitly through the first listener or Executor that referenced a `team_id` value. That model conflates identity creation with runtime use, allows callers to choose a `team_id`, and leaves source systems and task types without a registration path entirely. Image resolution at claim time depends on `default_image` being set on each referenced team AND on each task type and source system the claim references; without required `default_image` at registration, the Registry would need to fall back to Executor local configuration, which violates the principle that image selection belongs to the Registry.

The platform needs a single, exclusive path to mint immutable team-owned platform identities — teams, source systems, and task types — that no listener, Executor, or Gateway may invoke.

### Decision

State Registry SHALL expose three admin endpoints under `/admin/*`, each accepting only an authenticated system-administrator identity:

- `POST /admin/teams`: required unique `team_name` (display-only, never authorization), required opaque `default_image`, returns generated immutable `team_id`. The `team_name` field is unique across teams; the `default_image` is required and cannot be cleared later. Operators SHALL NOT clear or change `default_image` through any admin or operator path; the only way to change the team's `default_image` is to register a new team.

- `POST /admin/source-systems`: required `team_id` (which SHALL reference an existing team), required listener identity reference, immutable server-assigned `source_system_id`, optional `default_image`. The source system SHALL belong to exactly one team; cross-team reattachment SHALL be rejected without persistence. The opaque listener identity reference SHALL be constrained by a database-enforced global uniqueness index so one listener principal maps to exactly one `(team_id, source_system_id)` pair; an attempt to register a new source system with the same `listener_identity` value under the same team, a different team, or any other variation SHALL be rejected without mutation, so one principal cannot be reused across teams or re-registered under the same team.

- `POST /admin/task-types`: required `team_id` (which SHALL reference an existing team), immutable server-assigned `task_type_id`, required `execution_tag` (the immutable registered tag Executors must declare to be eligible for tasks of this type), optional `default_image`.

The Registry SHALL reject all `/admin/*` calls without an authenticated system-administrator identity. The Registry SHALL NOT describe the mechanism for that authentication beyond requiring the identity. The Registry SHALL reject listener or Executor registrations that supply a `team_id` not referencing an existing team. The Registry SHALL NOT allow Executor or listener bodies to create or update teams, source systems, or task types; an Executor registration is never a substitute for `POST /admin/teams`. The image returned to a claimed task uses the documented four-level precedence (`tasks.image` -> `task_types.default_image` -> `source_systems.default_image` -> `teams.default_image`) and SHALL always resolve because the team default is required at registration.

### Consequences

Positive consequences:

- One exclusive path creates every team-owned platform identity; no runtime service can mint an unknown team.
- Required `default_image` at team registration guarantees image resolution at claim, removing Executor local-image fallback semantics.
- Listener dedupe uses immutable `source_system_id` rather than free-form `source`; the cross-team reuse wording is corrected so a source-system identity belongs to one team.
- System-administrator authentication is required but its mechanism is outside this contract, decoupling admin onboarding from the Registry's contract surface.

Negative consequences:

- Operational onboarding must include provisioning a system-administrator identity and creating teams, source systems, and task types before any Executor or listener can use the platform. This added step blocks typical first-run flows until admin setup completes.
- Team `default_image` is immutable through operator and admin paths; corrections require a new team, which loses history tied to the old `team_id`.
- The contract deliberately refuses to specify how the system-administrator identity is authenticated, leaving operational decisions to deployment.
- Image resolution relies on the team default; removing or clearing that value post-creation is impossible.

## ADR: Use local AES-256-GCM authenticated encryption for at-rest secret values

### Status

Proposed

### Context

State Registry must store immutable secret versions while preventing plaintext persistence, logging, operator reads, cross-team access, and access by unassigned Executors. The encryption-key service must support centralized policy, rotation, auditability, and free self-hosting without binding FlowAI to one public cloud.

Earlier drafts of this contract used OpenBao Transit as the cryptographic key service. The revised contract replaces that provider with local AES-256-GCM authenticated encryption backed by a configuration-provided 256-bit key, while keeping the wire formats and the public HMAC scope-token model unchanged. The change allows the platform to ship and test without depending on an external cryptography service, while leaving a future change free to replace the local provider behind the same opaque `key_id` / `key_version` envelope.

FlowAI needs at-rest secret encryption that operates without a network dependency, keeps ciphertext and key material on the same trust boundary as the canonical database, and survives key rotation through the same opaque identifier pair that consumers of the Registry already see.

### Considered Options

#### Local AES-256-GCM authenticated encryption

The Registry encrypts each secret value with AES-256-GCM using a configuration-provided 256-bit data key, a fresh random 96-bit nonce per encryption, and a 128-bit authentication tag. The authenticated associated data binds at minimum the team identifier, the logical secret identifier, and the secret version. The opaque `key_id` and `key_version` recorded on each `secret_versions` row allow rotation; the consumer code path remains unchanged when the provider changes.

Advantages:

- No external cryptography service; the Registry and the key live on the same trust boundary.
- AES-256-GCM is a standard, well-analyzed authenticated encryption mode.
- Configuration-provided key rotation through opaque identifiers allows the platform to replace the provider later without changing the contract.
- The authenticated associated data binding prevents silent misuse of a ciphertext outside its team / logical secret / version.

Disadvantages:

- Database compromise exposes the key alongside ciphertext unless additional separation is added later.
- Application-level key handling increases the chance of custom cryptographic mistakes; mitigation requires audited code paths and reference tests.
- The Registry fails closed on startup when the key is missing.

#### OpenBao Transit (previously selected)

Strong cryptographic separation from PostgreSQL, durable multi-node storage, and audit devices at the cost of an extra network service.

Rejected for v0002 implementation because the platform is not yet ready to deploy an external key service in this change. A future change MAY re-introduce a dedicated provider behind the same `key_id` / `key_version` envelope.

### Decision

State Registry SHALL encrypt every secret value with local AES-256-GCM authenticated encryption. The data key SHALL be a configuration-provided 256-bit secret loaded from a secure configuration source, never logged, never persisted alongside ciphertext, and rotated through opaque `key_id` / `key_version` envelopes recorded on each immutable `secret_versions` row. Each encryption call SHALL generate a fresh random 96-bit nonce and SHALL compute a 128-bit authentication tag. The authenticated associated data SHALL bind at minimum the team identifier, the logical secret identifier, and the secret version; missing or mismatched binding SHALL cause decryption to fail before any plaintext leaves the Registry.

The Registry SHALL refuse decryption when the configured key is missing, the key identifier is outside the documented active window, the associated data binding does not match canonical records, the nonce or tag is invalid, or the decrypted plaintext fails any canonical authorization check. Startup SHALL fail closed when the AES-256-GCM key is missing; thereafter, secret writes and open-environment reads SHALL fail closed on every error. Application logs, audit records, and error responses SHALL NOT contain plaintext secret values, key material, key bytes, nonce bytes, ciphertext bytes, authentication tag bytes, individual associated-data values beyond identifier-level metadata, or any derived key bytes. The State Registry SHALL NOT depend on an external cryptography service to encrypt or decrypt secret values. A future change MAY replace the local provider behind the same opaque `key_id` / `key_version` envelope without altering this contract.

### Consequences

Positive consequences:

- One well-known authenticated encryption mode provides confidentiality, integrity, and binding of ciphertext to team/secret/version.
- No external cryptography dependency means the contract is implementable in a single trust boundary.
- Fail-closed behavior on missing key, bad associated data, or invalid tag prevents silent fallback to plaintext.
- The opaque `key_id` / `key_version` envelope allows a future migration to an external provider without a contract change.

Negative consequences:

- Compromise of the configuration that holds the AES key exposes all ciphertexts; this is the trade-off accepted by removing an external cryptography service.
- The Registry must produce authenticated associated data on every encryption call and validate it on every decryption call, increasing per-operation cost.
- Startup and decrypt failures must be carefully logged to avoid disclosing key material, nonce, ciphertext, or plaintext while still allowing operators to diagnose outages.
- Future rotation requires loading multiple keys by `key_id`; the Registry must manage that active window correctly.

Failure isolation:

- When the configured key is missing or invalid, the Registry SHALL refuse secret writes and open-environment reads closed without falling back to plaintext.
- All logs and audit entries SHALL contain only permitted identifier metadata, decision, and outcome; never plaintext, nonce, ciphertext, authentication tag, key material, or derived key bytes.

Implementation follow-up:

- Define the configuration source for the 256-bit AES-GCM key, the active key window, and rotation procedure before production deployment.
- Implement a typed Go AES-GCM provider with constant-time tag verification, a cryptographically secure random nonce source, key rotation compatibility, bounded retries, and no plaintext or key logging.
- Add integration tests proving encryption and decryption, key rotation compatibility, fail-closed behavior, same-team authorization, foreign-team denial before any decrypt operation, and recovery of older ciphertext versions.

## ADR: Sign scope tokens with an allow-listed HMAC and rotate server-controlled keys

### Status

Proposed

### Context

State Registry issues short-lived scope tokens that authorized Executors present at `GET /v1/environments/{environment_id}/open?task_id={task_id}`. Those tokens gate decrypted environment and secret access through the local AES-256-GCM provider, so a token forgery, replay, or post-terminal reuse would directly expose secret plaintext. The tokens must remain verifiable without round trips to an external service, must be bound to the authenticated assigned Executor, must resist replay after terminal task state or unassignment, and must allow graceful key rotation without weakening audit or operational logging safety.

The platform needs a token security model that is compact, deterministic, and auditable, with explicit guarantees for algorithm strength, key control, claim verification, comparison safety, replay handling, rotation, and logging.

### Decision

State Registry SHALL be the sole issuer and verifier of open-environment scope tokens. Each token SHALL be signed using an allow-listed HMAC algorithm from the set `HS256`/`HS384`/`HS512` (the "HMAC-SHA-256 or a stronger HMAC" family), keyed with a State Registry-controlled key selected by a `key_id` claim included in the payload. The compact three-part wire format is `<header>.<payload>.<signature>`; the protected header SHALL carry `alg` (allow-listed), `kid`, and `typ`; the payload SHALL carry `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry`, and `key_id`. The protected-header `kid` SHALL equal the payload `key_id`. `issued_at` SHALL satisfy `issued_at <= server_now + 30 seconds`, `expiry` SHALL be strictly later than `issued_at`, and `expiry - issued_at` SHALL NOT exceed five minutes.

State Registry SHALL verify that the protected-header `kid` equals the payload `key_id` before any MAC computation, SHALL recompute the signature under the declared allow-listed algorithm using the server-side key handle, SHALL compare the result with a constant-time comparison before any other check, SHALL accept only `key_id` values inside the documented active key window for HMAC rotation, SHALL require an `audience` claim that matches the documented literal identifier `state-registry.environment.open`, SHALL perform canonical claim verification (presence, type, encoding, and allowed values for every claim, including that `project_id` is required but nullable only when the canonical environment has no project scope) before any team, assignment, applicability, or decrypt operation, and SHALL never log token plaintext, individual claim values, MAC bytes, key material, or derived key bytes in application logs, audit records, or error responses.

A token SHALL be accepted only when the calling authenticated Executor identity is the currently assigned Executor for the referenced task and the referenced task is in a non-terminal state. State Registry SHALL reject any other identity, a terminal task state, or an unassigned Executor before any decrypt operation. State Registry MAY allow a retry of the same token within its TTL by the same assigned Executor; the retry SHALL NOT bypass canonical claim, transition, or assignment checks, SHALL NOT extend TTL, and SHALL NOT revive an expired token.

Key material SHALL be loaded from a secure configuration source. State Registry SHALL document an active key window: each `key_id` maps to a server-controlled key, retired keys SHALL be accepted only during a documented overlap period for rotation, and key material SHALL NOT be persisted alongside token material or logs. Tokens SHALL be issued on atomic successful claim; re-issuance after a transition that invalidates a token SHALL require a fresh successful claim and SHALL NOT reuse the prior `key_id`-signed envelope.

### Consequences

Positive consequences:

- Allow-listed HMAC (`HS256`/`HS384`/`HS512`) with a constant-time comparison gives a stable, well-analyzed algorithm family and removes timing-side-channel exposure during verification.
- Binding every token to a `key_id` with a documented active window lets State Registry rotate keys without breaking in-flight tokens and without admitting forged or retired-key tokens.
- The five-minute expiry ceiling and required `audience` claim minimize replay opportunity and let multiple cooperating services reject tokens minted for a different verifier.
- Canonical claim verification before any team, assignment, applicability, or local AES-256-GCM decrypt step ensures malformed or unexpected tokens cannot trigger downstream state reads or decryption.
- Denying tokens for terminal task state, unassigned Executors, or different Executor identities before any decrypt operation keeps secret access strictly within authorized task windows.
- Banning token plaintext, claim values, MAC bytes, and key material from logs and audit records keeps the most sensitive token and key data out of long-lived observability surfaces.
- Allowing same-identity retry within TTL without bypassing canonical checks improves Executor robustness against transient network or restart failures without weakening security.

Negative consequences:

- State Registry becomes the sole token verifier and must own the documented active key window, the configuration source for keys, and the rotation policy.
- The five-minute TTL requires Executors to refresh tokens promptly after a successful claim and may complicate retries that span task reassignment or restart; explicit recovery flows remain necessary.
- Storing key material separately from token data and ensuring no logging of sensitive material adds configuration, deployment, and CI hygiene requirements.
- A documented active key window for rotation adds operational coordination between key provisioning, deployment, and Registry restarts.
- Constant-time MAC verification and canonical claim checking add small per-request CPU costs compared with non-verifying paths, but these costs are acceptable for open-environment traffic.

Failure isolation:

- When State Registry cannot verify a token (expired, foreign team, foreign identity, terminal task state, retired `key_id`, malformed claim, or constant-time MAC failure), it SHALL reject the request closed without any decrypt operation and SHALL append a plaintext-free audit entry.
- When the AES-256-GCM provider fails (missing key, bad associated data, invalid tag), the Registry SHALL still perform token verification and the audit append; only the decrypt step depends on provider availability.
- Logs and audit records SHALL contain only permitted identifier metadata, decision, and outcome; never token plaintext, individual claim values, MAC bytes, key material, or derived key bytes.

## ADR: Resolve task image through a four-level precedence with required team default

### Status

Proposed

### Context

Operators currently configure the runtime image per Executor process, which means the choice of image lives outside the canonical State Registry. This is hard to audit, hard to replay, and forces operators to deploy one Executor per image. Operators need both team-wide defaults and per-task, per-task-type, and per-source-system overrides so that a single team can have a team-default image, a task-type default that reflects typical workloads of that type, a per-source-system default that reflects typical tasks from that source, and a one-off per-task override for an unusual job. Image strings are not team-owned resources, and image equality across teams carries no authority.

The four-level precedence is `tasks.image` -> `task_types.default_image` -> `source_systems.default_image` -> `teams.default_image`. Because `teams.default_image` is required at registration, resolution always succeeds; the Executor never maintains or needs a local fallback image.

The platform needs image resolution to be deterministic, recorded in the same transaction as claim, replayable across restarts, and consistent between what State Registry returns and what the assigned Executor actually starts. Image references must not influence authorization, scheduling, priority, or capacity, and must not leak between teams.

### Considered Options

#### Resolve in State Registry at claim with four-level precedence, persist on the canonical task row, eliminate Executor local fallback

State Registry stores an OPTIONAL `image` override per `tasks` row, an OPTIONAL `default_image` per `task_types` row, an OPTIONAL `default_image` per `source_systems` row, and a REQUIRED `default_image` per `teams` row set at `POST /admin/teams`. On atomic claim, State Registry computes the effective image using the precedence listed above, persists `tasks.resolved_image` and `tasks.image_source` in the same transaction that appends the first `created` event, and includes `resolved_image` and `image_source` in the claim response. The Executor uses `resolved_image` verbatim and SHALL NOT consult any other source.

Advantages:

- Image selection lives in the same canonical service that owns tasks, assignments, and audit.
- `tasks.resolved_image` is durable across restarts and is the single source of truth for what the Executor runs.
- The claim response carries the authoritative image, removing any chance of Executor and Registry disagreement.
- Image references never affect authorization, scheduling, priority, or capacity; they participate only in deterministic resolution and the audit trail.
- Cross-team leakage is impossible because resolution runs inside the same team-scoped transaction that already authorizes the claim.
- Image strings carry no team authority because they are not team-owned resources; two teams MAY reference the same image without granting each other authority.
- Removing Executor local fallback removes a class of integration tests and config-drift failures.

Disadvantages:

- Operators must keep `teams.default_image` accurate for typical workloads because the team default is required and immutable through operator paths.
- Four image-bearing columns (`tasks.image`, `task_types.default_image`, `source_systems.default_image`, `tasks.resolved_image`, plus `teams.default_image`) add to schema complexity.
- Operators cannot selectively override only future claims through the admin path because `default_image` is immutable after registration; corrections require a new team.

#### Resolve entirely in the Executor from a separate image service

A dedicated image-configuration service would expose per-team defaults and per-task overrides; the Executor would query it at claim time or alongside discovery.

Rejected because it would split image configuration across two services, reintroduce a network call on the hot path of every claim, and put authoritative image selection outside the canonical audit and event log.

#### Hard-code the image in the listener payload only

Listeners submit `image` per task and the Executor uses that value at run time, with no team-wide default.

Rejected because it removes the operator-wide defaults that teams need, forces every listener to know the image, and prevents the team-default / task-type-default / source-system-default layering.

#### Keep three-level precedence with optional team default and Executor local fallback

Earlier drafts allowed optional `teams.default_image` and Executor local fallback when no override applied.

Rejected because a missing team default would force Executors to ship local fallback images, which violates the principle that image selection belongs to the Registry. With required `teams.default_image`, that fallback is unnecessary.

### Decision

State Registry SHALL be the authoritative resolver of task image at claim time. The image sources are opaque container image references consisting of a registry repository path and an immutable digest; the Registry SHALL NOT resolve, pull, verify, mutate, or interpret the digest. Image resolution SHALL use this precedence on atomic claim:

1. The canonical task's stored `image` override if non-NULL.
2. Otherwise, the referenced task type's `default_image` if non-NULL.
3. Otherwise, the referenced source system's `default_image` if non-NULL.
4. Otherwise, the parent team's stored `default_image` (always present because it is required at registration).

State Registry SHALL persist the resolved image as `tasks.resolved_image` and the source as `tasks.image_source` in the same transaction that appends the first lifecycle event `created`. The image_source SHALL be one of `task_override`, `task_type_default`, `source_system_default`, or `team_default`. The Registry SHALL include `resolved_image` and `image_source` in the claim response; the Executor SHALL use `resolved_image` verbatim and SHALL NOT substitute a local image or consult any other source. Image strings are not team-owned resources: the same image MAY be referenced across teams without granting or broadening authority, and image equality SHALL NOT establish tenant ownership.

Operators SHALL set `teams.default_image` through `POST /admin/teams`; the value SHALL be REQUIRED at registration and SHALL NOT be cleared or edited through any admin or operator path. Listeners MAY include `image` on `TaskIngestionRequest`; once set on the canonical task, `image` SHALL be immutable for the task's lifetime, so a later listener retry of the same `(team_id, source_system_id, source_id)` SHALL NOT replace or clear it. `tasks.resolved_image` SHALL be set atomically at claim and SHALL be immutable from claim onward; later edits to `teams.default_image` (only possible by registering a new team) SHALL affect future task claims only and SHALL NOT change any claimed task's `resolved_image`.

Image references SHALL NOT participate in tenant authorization. They SHALL NOT be used to select, prioritize, schedule, or reject work; they SHALL NOT gate discovery, claim, or lifecycle-event acceptance; and they SHALL NOT alter Executor capacity observations. State Registry SHALL accept any opaque digest string on `tasks.image`, `task_types.default_image`, `source_systems.default_image`, and `teams.default_image` (subject to documented format constraints in the OpenAPI schema) and SHALL NOT validate, parse, or otherwise interpret it.

A new C4/sequence diagram `state-registry-image-resolution-sequence.puml` SHALL document the four-level resolution flow and the handoff of `resolved_image` and `image_source` to the claiming Executor.

### Consequences

Positive consequences:

- Image configuration is part of the canonical platform surface, with audit entries for every accepted image write and for every image resolved at claim.
- The claim response carries the authoritative image, so the Executor and the Registry never disagree about what to run.
- `tasks.resolved_image` and `tasks.image_source` are durable across restarts and are the single source of truth for replay.
- Team-wide, task-type-wide, source-system-wide, and per-task scopes coexist without privilege escalation; image data is opaque, not team-owned, and never used to grant access.
- Removing Executor local fallback simplifies Executor configuration and removes a class of integration tests.
- Required `teams.default_image` makes resolution always succeed.

Negative consequences:

- Five image-bearing columns (`tasks.image`, `tasks.resolved_image`, `task_types.default_image`, `source_systems.default_image`, `teams.default_image`) add to schema and surface to the Registry.
- Operators cannot modify `teams.default_image` after registration; corrections require a new team, which loses history tied to the old `team_id`.
- Image references carry no integrity check beyond the registry digest itself; State Registry does not validate that the digest is well-formed, that the registry exists, or that the image is pullable. Operators retain responsibility for those checks at their container registry.
- Application code MUST NOT interpret image equality as team ownership; the four-level precedence and the no-cross-team-authority rule both require explicit, careful application logic.

## ADR: Make Executor ownership scope a registration-time choice between team-owned and system-owned

### Status

Proposed

### Context

The platform originally required every Executor service identity to belong to exactly one immutable `team_id`. That model gives strong tenant isolation: an Executor discovers only same-team tasks, claims only same-team tasks, and the cross-team Executor surface does not exist. It also forces every team to deploy its own Executor processes, which is operationally heavy when many teams need the same agent runtime, and prevents shared cluster capacity from serving tasks across tenants.

A second model is to make Executor ownership optional at registration time. A team-owned Executor keeps the original semantics: it discovers and claims only same-team tasks with its registered tag. A system-owned Executor has no team binding: it discovers every `pending` task with its registered tag across all teams, and may claim any such task in FIFO order. The two modes share the same per-task invariants (every task still belongs to exactly one team, every claim is atomic, every scope token still binds to the parent task's `team_id`); the difference is the team predicate in discovery and claim, and which identity is compared against the envelope `team_id` in task-event and self-event writes.

The platform needs to choose between forcing every Executor into team-owned mode and offering both modes, without weakening the per-task invariants that protect tenant isolation.

### Considered Options

#### Force every Executor to be team-owned (status quo)

Keep the original invariant: every Executor belongs to exactly one team. Cross-team claim is impossible.

Rejected because it forces every team to run its own Executor fleet and prevents shared cluster capacity, even when teams want to share infrastructure for the same runtime.

#### Make `team_id` optional on registration without a scope field

Allow `executors.team_id` to be NULL when the Executor registers. A NULL `team_id` means the Executor is system-owned; a non-NULL `team_id` means team-owned.

Rejected because the meaning of NULL is implicit and a future maintenance change could quietly break the rule. An explicit `scope` field makes the contract self-documenting and lets State Registry reject ambiguous registrations (`scope = system` with non-null `team_id`, `scope = team` with null `team_id`, or `scope = team` with a `team_id` not referencing an existing team) with a precise error rather than inferring intent from a column value.

#### Add an explicit `scope` field with `team` and `system` values

State Registry SHALL require every Executor registration to include a `scope` field that is one of the documented enum values. `scope = team` requires a non-null `team_id` that references an existing team; `scope = system` requires a null `team_id`; the combinations SHALL be rejected. The Executor's scope SHALL be persisted on the `executors` row, SHALL be immutable from registration onward, and SHALL drive every team predicate in discovery, claim, task events, and self events. Audit entries SHALL record `executor_scope` so operators can attribute cross-team actions to the system-owned Executor that performed them. The parent task's `team_id` remains immutable for the row's lifetime regardless of Executor scope, and the `executors.team_id` NULL value for system-owned Executors does not change `tasks.team_id` ownership rules.

### Decision

State Registry SHALL make Executor ownership scope a registration-time choice between `team` and `system`, persisted on the `executors` row, and SHALL derive every team predicate from the recorded scope. A team-owned Executor SHALL be bound to exactly one immutable `team_id` that references an existing team; the Registry SHALL verify the submitted `team_id` against the authenticated Executor service identity and SHALL reject a re-registration that omits, changes, or widens the team. A system-owned Executor SHALL be bound to no team; the Registry SHALL persist `team_id = NULL` and SHALL NOT verify a body `team_id`.

Discovery, claim, task-event writes, and self-event writes SHALL apply the team predicate according to the Executor's scope:

- Discovery SHALL match `tasks.required_tag = executors.authorized_tag` and SHALL order results `(ingested_at ASC, task_id ASC)`. When `scope = team`, the predicate SHALL additionally require `tasks.team_id = executors.team_id`. When `scope = system`, the team predicate SHALL be omitted and the discovery MAY return summaries for tasks from any team.
- Claim SHALL match the tag equality. When `scope = team`, claim SHALL additionally require `tasks.team_id = executors.team_id`. When `scope = system`, the team predicate SHALL be omitted and any `pending` task with the matching tag is eligible. Atomicity guarantees exactly one winner; the FIFO ordering rule SHALL be applied per Executor inside the eligibility filter.
- Task-event envelope verification SHALL verify that the envelope `team_id` equals the parent task's `team_id`. When `scope = team`, the Registry SHALL additionally verify that the envelope `team_id` equals the authenticated Executor's `team_id`. When `scope = system`, the Executor's own `team_id` is NULL by construction and the second check is replaced by a structural check that the envelope `team_id` matches the parent task's `team_id` and the executor is the recorded `tasks.executor_id`.
- Self-event envelope verification SHALL verify that the envelope `team_id` matches the Executor's recorded scope. When `scope = team`, the envelope `team_id` SHALL be non-null and SHALL equal the authenticated Executor's `team_id`. When `scope = system`, the envelope `team_id` SHALL be null or absent and SHALL match the Executor's NULL `team_id`.

Audit entries for every accepted task event and self event SHALL include the Executor scope so operators can attribute actions to a system-owned Executor when a cross-team action occurs. Operators SHALL NOT be able to register a system-owned Executor with a non-null `team_id`, register a team-owned Executor with a null `team_id`, register a team-owned Executor with a `team_id` that does not reference an existing team, or change an Executor's scope from one registration to the next; each of those attempts SHALL be rejected without persistence.

### Consequences

Positive consequences:

- Teams that want strict isolation can deploy a team-owned Executor per team with no cross-team surface.
- Teams that want shared infrastructure can deploy a system-owned Executor with a single tag and let it pick up tasks across tenants with the same tag, without per-team Executor processes.
- Per-task invariants are preserved: every task still belongs to one team, every claim is atomic, every scope token still binds to the parent task's `team_id`, every event envelope still carries the parent task's `team_id`.
- The Registry's authorization surface stays the same for team-owned Executors; the only new surface is the explicit `scope` field and the cross-team discovery branch for system-owned Executors.

Negative consequences:

- A system-owned Executor can see metadata for tasks across many teams in one discovery response: `payload`, `required_tag`, `project_id`, `environment_id`, `image`, `current_state`, `ingested_at`, `source_system_id`, `source_id`, `task_type_id`. Operators that enable cross-team dispatch accept that one Executor process sees that metadata; this is the documented trade-off and is recorded in the audit entry's `executor_scope`.
- A compromised system-owned Executor can attempt to claim tasks across teams; the atomic FIFO ordering rule still prevents duplicate execution, but the visibility is wider than the team-owned case.
- The implementation must add a conditional team predicate to discovery, claim, task-event envelope verification, and self-event envelope verification. Each branch must be covered by E2E tests.
- An Executor that registers with one scope cannot re-register with a different scope; deployments that want to change scope must register a new Executor identity, which adds a small operational step.
- The contract documents two modes and the security implications; reviewers and operators must read both. The contract forbids cross-team claim at the task layer (every task still belongs to one team) but allows it at the Executor dispatch layer.
