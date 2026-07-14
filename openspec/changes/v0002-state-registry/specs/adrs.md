# Proposed ADRs for v0002-state-registry

## ADR: Consolidate durable team-owned platform state in State Registry

### Status
Proposed

### Context
Separate Router and Env Registry services would split canonical team-owned tasks, assignments, environment definitions, and encrypted secrets across avoidable network boundaries and conflicting ownership models. Team isolation needs one transaction and authorization boundary so ingestion, assignment, events, controls, environments, secrets, and audit cannot disagree about ownership.

### Decision
State Registry owns durable team-scoped task intake, deduplication, read-only task discovery, atomic start approvals, assignments, task and Executor events, environment definitions, logical secrets, encrypted secret versions, audit, and controls. No Router or Env Registry service is deployed. Executors retain runtime lifecycle and local capacity only; API Gateway remains the sole operator backend. Every durable resource is authorized through immutable `team_id` ownership before domain-specific tag, assignment, project, or task applicability checks.

### Consequences
State Registry has a larger persistence and authorization surface, but the platform has one canonical team-isolation and transaction boundary with no cross-registry consistency problem. This decision supersedes the never-accepted Router dispatch draft formerly tracked as `v0004-router` and the never-accepted, never-implemented standalone Env Registry draft preserved at `openspec/changes/archive/2026-07-14-v0003-env-registry/`; it does not claim that accepted ADRs 0001, 0003, or 0004 established those ownership boundaries. Tenant-isolation defects in this service have a large blast radius, so point reads, collections, counts, aggregations, controls, environment access, audit, and WebSocket delivery all require explicit same-team verification.

## ADR: Use immutable team identifiers as the tenant authority

### Status
Proposed

### Context
Listeners submit tasks for teams, Executors consume work for teams, and operators manage team-wide environments and secrets through API Gateway. Human-readable team names can change and can collide. Caller-selected headers and payload fields are untrusted. If authorization uses `team_name`, a client-provided tenant hint, or a resource lookup before tenant filtering, the Registry can expose another team's resource existence, counts, history, or secret metadata.

The platform needs one tenant authority that is stable across display-name changes and is carried consistently through ingestion, service identity, assignment, Gateway requests, storage relationships, scope tokens, audit, pagination, aggregation, and WebSocket delivery.

### Decision
`team_id` SHALL be the canonical immutable tenant identifier and authorization authority. `team_name` SHALL be display and audit context only and SHALL NOT grant, select, or broaden access. Each task and Executor SHALL belong to exactly one team for its lifetime. Each authenticated operator request context SHALL represent exactly one `operator_id` and one `team_id`.

Each listener and Executor service identity SHALL be bound to exactly one authorized `team_id`. Every listener task submission SHALL carry that `team_id`, and State Registry SHALL verify it against the authenticated listener before persistence or acknowledgement. Listener deduplication SHALL use `(team_id, source, source_task_id)`.

Every Executor-emitted task event and Executor self event envelope SHALL carry a non-null `team_id`. State Registry SHALL reject any event whose envelope `team_id` differs from the authenticated Executor's immutable `team_id` with `403 team_mismatch`, SHALL persist the verified `team_id` on the event row alongside the authenticated `executor_id`, and SHALL reject a same-team Executor that is not the current `tasks.executor_id` with `403 not_assigned`. A foreign task or Executor point identifier SHALL receive the same non-revealing `404` shape used for an unknown identifier. Registry-emitted `created` and `dispatched` events inherit the parent task's immutable `team_id` without an envelope verification step; their `executor_id` remains `NULL`.

Operator State Registry requests SHALL require a trusted API Gateway service identity and Gateway-verified context containing `operator_id`, `team_id`, and `request_id`. The optional display-only `team_name` SHALL be forwarded only when the verified operator team carries one. State Registry SHALL authorize only with `team_id`; the absence of `team_name` SHALL NOT weaken authorization anchored to `team_id`. Direct clients and caller-supplied tenant headers SHALL NOT establish operator context.

The `teams` table SHALL own immutable `team_id` and display metadata. `tasks`, `executors`, `environment_definitions`, and `secrets` SHALL each reference one team. Child records SHALL inherit and enforce parent team ownership through matching composite foreign keys or an equivalent database-enforced relationship. `secret_versions` SHALL reference `secrets`; it SHALL NOT duplicate logical secret identity or permit cross-team reparenting.

Environments and secrets SHALL be managed by same-team operators. Project and task scopes SHALL narrow applicability only within the owning team and SHALL NOT act as project RBAC or cross-team authority. A scope token used by an assigned Executor SHALL bind `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry`, and `key_id`, and every claim SHALL be checked against canonical records before decryption.

Every team-scoped collection or history query SHALL apply the trusted `team_id` predicate before pagination, cursor construction, totals, counts, grouping, aggregation, or serialization. Every WebSocket connection SHALL be bound to one trusted Gateway `team_id` at upgrade; replay, filtering, live fan-out, and every frame SHALL remain in that team. Any cross-team WebSocket frame, count, cursor, or existence signal SHALL block review and release.

An authenticated caller that names a task, Executor, event, control, environment, secret, secret version, or audit resource owned by another team SHALL receive the same non-revealing `404` used for an unknown identifier. The request SHALL append no domain event, mutate no canonical resource, create no control, disclose no audit row, and perform no secret decryption.

Audit entries SHALL include `team_id`, actor identity and type, action, resource type and identifier, `request_id`, outcome, and timestamp without plaintext secret or decrypted environment values.

Team provisioning and membership resolution SHALL remain outside State Registry. This change SHALL NOT add team CRUD, a membership-management UI, multi-team Executor or operator contexts, cross-team sharing, task movement between teams, or project-level RBAC. `v0006-web-ui` MAY bootstrap one team initially, and `v0007-auth` MAY supply operator team claims, without weakening State Registry's requirement to validate trusted context and authorize by immutable `team_id`.

Open-environment scope tokens SHALL be signed and verified per the "Sign scope tokens with an allow-listed HMAC and rotate server-controlled keys" ADR. State Registry SHALL be the sole issuer and verifier of those tokens, SHALL verify that the protected-header `kid` equals the payload `key_id` before any MAC computation, SHALL recompute the signature under the declared allow-listed HMAC algorithm, SHALL compare it under constant-time comparison, SHALL reject tokens outside the documented active `key_id` window, SHALL reject tokens whose `issued_at` is premature beyond 30 seconds of clock skew, whose `expiry` is not strictly later than `issued_at`, whose `expiry - issued_at` exceeds five minutes, or whose `audience` does not match the expected literal identifier, and SHALL deny tokens presented by an Executor other than the currently assigned same-team Executor for a non-terminal referenced task before any OpenBao operation.

### Consequences
Positive consequences:

- Display-name changes cannot change tenant authority or ownership.
- Listener retries deduplicate inside one team without colliding with another team's source identifiers.
- The same team invariant protects REST point reads, collections, audit, controls, environments, secrets, and WebSocket frames.
- Same-tag Executors in different teams cannot discover or approve each other's tasks.
- Database-enforced parent/child ownership reduces the chance that an application query creates a cross-team association.
- Team-bound scope tokens prevent a valid token for one team, task, environment, or Executor from being replayed in another context.

Negative consequences:

- Every tenant-owned query and relationship must carry or derive `team_id`, which adds composite indexes, foreign-key constraints, and test cases.
- Non-revealing `404` semantics reduce diagnostic detail for callers; trusted operational logs and plaintext-free audit context must carry enough request information for support.
- Team CRUD, membership, and project authorization require later changes rather than implicit behavior in State Registry.
- WebSocket isolation requires connection-bound authorization and per-frame safeguards, not only an upgrade-time check.

## ADR: Use an event-sourced task lifecycle with normalized 3NF PostgreSQL persistence

### Status
Proposed

### Context
The platform needs one authoritative record of every task transition and Executor observation so audit, replay, and external reconciliation stay deterministic across restarts and concurrent writers. Mutable projection rows alone hide the source of truth. Accepting caller-selected recovery sequence values for older lifecycle events would allow an Executor to reorder history or bypass transition validation without a clearly defined authority model.

State Registry must persist team-owned platform state in a schema that survives concurrent appends, supports external read-back in one deterministic accepted order, rejects stale transitions safely, and avoids update anomalies. The durable surface must express ownership cleanly so migrations are straightforward and individual domain entities do not blur into each other.

### Decision
State Registry SHALL define the canonical task lifecycle as `created -> dispatched -> running -> finished | failed`. Every transition SHALL be an immutable task event row appended to `task_events` in PostgreSQL in the same transaction that updates projected `tasks.current_state`. State Registry SHALL emit `created` on successful ingestion and `dispatched` on atomic approval. The assigned same-team Executor SHALL emit `running`, `finished`, and `failed`. No other event type belongs to the canonical lifecycle.

`event_id` SHALL be a stable monotonic identifier and the idempotency key. A retry with the same event identifier SHALL return the original acceptance without appending a duplicate. After idempotent retry resolution, every new event's `(occurred_at, event_id)` tuple SHALL be strictly greater than the task's latest accepted tuple. State Registry SHALL reject an older or equal new tuple without appending or changing projected state. The contract SHALL have no `accepted_sequence` field or caller-supplied recovery override. Accepted events SHALL be read by `(occurred_at ASC, event_id ASC)`.

`task_id` SHALL be globally unique. The idempotency key `(task_id, event_id)` SHALL be safe for retry deduplication because `task_id` does not collide with team ownership; `team_id` SHALL also be persisted on every event row and verified against the authenticated Executor before append.

State Registry SHALL reject any event submission or approval whose target transition is not permitted from current state and SHALL append no event and change no projection on rejection. Executor-emitted task events SHALL also require the authenticated assigned Executor and same-team ownership.

State Registry SHALL persist its durable surface in a third normal form PostgreSQL schema. Every domain entity SHALL live in its own table, every non-key attribute SHALL depend on the whole primary key, and transitive dependencies SHALL be removed. Ownership SHALL be expressed through foreign keys without denormalizing mutable display data into child rows.

The Registry SHALL own at minimum `teams`, `executors`, `tasks`, `task_events`, `executor_events`, `environment_definitions`, `secrets`, `secret_versions`, `audit_entries`, and `task_control_requests`. Normalized relationships SHALL include `teams 1:N executors`, `teams 1:N tasks`, `teams 1:N environment_definitions`, `teams 1:N secrets`, `executors 1:N executor_events`, `executors 1:N tasks`, `tasks 1:N task_events`, `tasks 1:N task_control_requests`, `environment_definitions 1:N secrets`, and `secrets 1:N secret_versions`.

`tasks.executor_id` SHALL be nullable before dispatch, immutable after approval, and constrained to a same-team Executor. `task_events.executor_id` SHALL be non-null for every Executor-emitted event and `NULL` only for registry-emitted `created` and `dispatched`; `task_events.team_id` SHALL be non-null on every row and SHALL equal the parent task's immutable `team_id`. `executor_events.executor_id` SHALL be `NOT NULL` and `executor_events.team_id` SHALL be `NOT NULL` on every Executor self event; both SHALL match the authenticated Executor's identity and immutable team. Child rows SHALL inherit and enforce the parent team's ownership through matching composite foreign keys or equivalent database constraints. The Registry SHALL NOT publish an ER diagram as part of this contract.

Each task SHALL declare exactly one required tag. Each Executor SHALL register exactly one immutable team and exactly one authorized tag. The Registry SHALL store `max_capacity` and `running_count` as observations on `executors`, refreshed by the latest same-team Executor self event in the same transaction that appends to `executor_events`. The Registry SHALL NOT read, compare, evaluate, or enforce capacity during discovery or approval.

### Consequences
Positive consequences:

- The immutable accepted `task_events` log is the authoritative lifecycle record used by audit, replay, projection verification, and external reconciliation.
- Invalid or out-of-order lifecycle transitions are rejected without corrupting history.
- Removing `accepted_sequence` eliminates a caller-controlled ordering bypass and keeps recovery outside the normal event-ingestion contract.
- 3NF normalization and same-team foreign-key ownership prevent transitive update anomalies and cross-team child associations.
- A logical `secrets` parent gives immutable `secret_versions` a stable ownership and lifecycle anchor.
- `executor_events.executor_id NOT NULL` makes every self observation attributable to an Executor and its inherited team.
- Exactly-one-team and exactly-one-tag constraints simplify discovery and assignment filters.
- Removing capacity from discovery and approval decouples scheduling correctness from runtime bookkeeping.

Negative consequences:

- Producers must generate monotonic event identifiers and submit new lifecycle events in order; stale new events are rejected rather than inserted retroactively.
- Recovery from a lost or delayed transition requires an explicit future recovery design instead of an `accepted_sequence` escape hatch.
- The Registry must enforce `tasks.executor_id` immutability explicitly because ordinary foreign keys do not provide set-once behavior.
- 3NF and tenant-enforcing relationships require more joins and composite indexes than a denormalized task row.
- Read paths require indexes including `(team_id, task_id)`, `(team_id, current_state, required_tag)`, `(task_id, occurred_at, event_id)`, `(team_id, executor_id, authorized_tag)`, and unique `(team_id, source, source_task_id)`.
- Exactly-one-tag registration requires separate Executor processes for different tags.

## ADR: Use same-team, same-tag discover-then-approve task dispatch

### Status
Proposed

### Context
Multiple eligible Executors in one team may discover the same `created` task at the same time. Executors in another team may use the same tag but must never see or approve that task. Discovery must remain cheap and non-blocking, but no two Executors may start the same task. Capacity enforcement belongs to each Executor; State Registry must not couple assignment correctness to observed capacity.

### Decision
Task discovery SHALL be a read-only snapshot filtered first by the authenticated Executor's immutable `team_id` and then by exact equality between the task's single required tag and the Executor's single authorized tag. Discovery SHALL not reserve, hide, or assign a task, SHALL not depend on observed capacity, and SHALL apply team filtering before pagination or counts.

Before starting work, an Executor SHALL ask State Registry whether it may start a specific `task_id`. State Registry SHALL perform an atomic compare-and-set from `created` to `dispatched` only after verifying same-team ownership, exact tag equality, and authenticated Executor identity. In one transaction it SHALL append `dispatched`, set immutable `tasks.executor_id`, record approval time, and remove the task from same-team discovery.

The successful requester SHALL receive `200 approved` with the canonical task, environment reference, and team-bound scope token containing `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `key_id`, `issued_at`, and `expiry` with `expiry > issued_at` and `expiry - issued_at <= 5 minutes` and `issued_at <= server_now + 30 seconds`. A later same-team competitor SHALL receive `409 task_already_dispatched`. An authenticated Executor naming a foreign-team task SHALL receive the same `404` as an unknown task. An invalid service identity or a request using an unregistered tag MAY receive an authentication or authorization error. The Registry SHALL never return a capacity-related rejection. No error response may change assignment or append an event.

### Consequences
Positive consequences:

- Exactly one same-team, same-tag Executor can receive permission to start a task.
- Duplicate execution is prevented even when eligible Executors race on the same discovery snapshot.
- Same tags can be reused safely in different teams without cross-team discovery.
- Foreign identifiers reveal no task existence or state.
- Discovery remains non-locking and scales independently from the approval transaction.
- Capacity remains local, so stale observations cannot produce Registry-side scheduling failures.

Negative consequences:

- Every task start requires an additional State Registry round trip.
- Executors must handle stale results, normal same-team `409` conflicts, and non-revealing `404` responses.
- Approval and tenant-filtered discovery are correctness-critical and require concurrent and cross-team isolation tests.
- An Executor that wins approval while locally full may delay runtime start; local capacity management remains its responsibility.

## ADR: Use OpenBao Transit for secret encryption

### Status
Proposed

### Context
State Registry must store immutable secret versions while preventing plaintext persistence, logging, operator reads, cross-team access, and access by unassigned Executors. The encryption-key service must support centralized policy, key rotation, envelope encryption, auditability, and free self-hosting without binding FlowAI to one public cloud.

FlowAI needs a service operated independently from the State Registry database. Compromise of PostgreSQL alone must not reveal the key material required to decrypt stored ciphertext. Cryptographic separation does not replace tenant authorization: State Registry must establish same-team ownership and scope before calling the key service.

### Considered Options

#### OpenBao Transit

OpenBao is community-governed open-source software under MPL 2.0. Its Transit secrets engine provides encryption and decryption APIs, managed key versions, rotation, rewrapping, data-key generation, HMAC operations, ACL policies, workload authentication, audit devices, and optional HSM or external-KMS sealing.

Advantages:

- Fully self-hosted and free to use under an OSI-approved license.
- Avoids HashiCorp Vault's BUSL licensing constraints.
- Transit is purpose-built for cryptography-as-a-service and does not require OpenBao to store application secret records.
- Supports key rotation and decrypting older ciphertext versions without rewriting every State Registry record immediately.
- HTTP API allows State Registry to remain implemented in Go without language coupling.
- Mature Vault-compatible operational and policy model.

Disadvantages:

- Adds a security-critical service that must be deployed, initialized, unsealed, monitored, backed up, and upgraded.
- Production high availability normally requires a multi-node cluster and durable integrated Raft storage.
- State Registry secret writes and open-environment reads depend on OpenBao availability.
- The OpenBao Go client may require a small internal Transit HTTP wrapper rather than relying on complete high-level Transit helpers.
- Operators must manage TLS, workload authentication, ACL policies, audit devices, seal recovery, and key lifecycle correctly.

#### HashiCorp Vault Transit

Vault Transit provides a mature and similar API, but current releases use BUSL 1.1 rather than an OSI-approved open-source license. It remains technically suitable, especially where Vault is already operated, but introduces avoidable licensing and vendor-governance risk for a platform intended to remain open and self-hostable.

#### Infisical KMS

Infisical provides an open-source secrets-management core and KMS capabilities. It combines secret management and encryption in a broader platform, but some advanced functionality is commercial and its Transit-style envelope-encryption model is less directly aligned with this design.

#### OpenStack Barbican

Barbican is Apache-2.0 licensed and provides dedicated key management. It is operationally heavy outside an existing OpenStack deployment and would require FlowAI to implement more of the envelope-encryption workflow itself.

#### Application-managed encryption keys

State Registry could encrypt secrets directly using key material from configuration or local files. This has the lowest deployment cost but places encryption keys beside the application, weakens separation from database compromise, complicates rotation and audit, and increases the risk of custom cryptographic mistakes. This option is rejected.

### Decision
FlowAI SHALL use a separately deployed OpenBao Transit secrets engine as the cryptographic key service for State Registry secret versions.

State Registry SHALL authenticate to OpenBao using a short-lived workload identity and a least-privilege policy limited to required Transit key operations. Production communication SHALL use mutually authenticated TLS with certificate validation. Root tokens SHALL be used only for bootstrap and SHALL NOT be available to State Registry.

OpenBao SHALL own and rotate the Transit encryption key. State Registry SHALL own team-scoped environment definitions, logical `secrets`, immutable `secret_versions`, ciphertext, scope metadata, and audit records in PostgreSQL. Every `secret_versions` row SHALL reference its owning `secrets` row. OpenBao SHALL NOT be the canonical application secret store and SHALL NOT decide tenant authorization.

Before any encryption or decryption request, State Registry SHALL verify trusted caller context, immutable `team_id`, same-team parent relationships, assignment where applicable, project/task applicability, and every team-bound scope-token claim. A foreign-team or otherwise unauthorized request SHALL fail before any OpenBao operation.

Scope tokens SHALL satisfy the "Sign scope tokens with an allow-listed HMAC and rotate server-controlled keys" ADR: an allow-listed HMAC algorithm from the set `HS256`/`HS384`/`HS512` (the "HMAC-SHA-256 or a stronger HMAC" family), a protected-header `kid` and a payload `key_id` that MUST equal `kid`, the literal `audience` identifier `state-registry.environment.open`, an `issued_at` whose premature tolerance is `issued_at <= server_now + 30 seconds`, an `expiry` strictly later than `issued_at` and no greater than five minutes after `issued_at`, and canonical claims `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, and `executor_id`. State Registry SHALL verify `kid == key_id` before any MAC computation, SHALL compare the MAC under constant time using the declared allow-listed algorithm, SHALL accept only `key_id` values inside the documented active key window, SHALL never log token plaintext, claim values, MAC bytes, key material, or derived key bytes, and SHALL deny tokens for foreign teams, foreign Executors, terminal task state, or unassigned Executors before any OpenBao operation. A retry of the same token within TTL by the same assigned same-team Executor MAY be allowed but SHALL still pass every canonical, transition, and assignment check and SHALL NOT extend TTL or revive an expired token.

Encryption and decryption SHALL use the OpenBao Transit HTTP API. State Registry MAY use the official OpenBao Go client for authentication and token lifecycle, with a small internal typed wrapper around Transit HTTP operations where high-level client support is incomplete.

Secret plaintext SHALL be sent to OpenBao only over mutually authenticated encrypted transport and SHALL remain in State Registry memory only for the duration of an authorized write or open-environment response. Plaintext SHALL NOT be persisted, logged, included in audit records, returned to API Gateway, or included in task or Executor events.

OpenBao production deployment SHALL use durable storage, TLS, audit logging, monitored backup and recovery, and a documented seal strategy. Integrated Raft is the default storage recommendation. Local development MAY use one OpenBao node; production SHOULD use an odd-sized highly available cluster, normally three nodes.

### Consequences
Positive consequences:

- PostgreSQL compromise alone does not expose Transit encryption keys.
- FlowAI remains free to self-host under an OSI-approved OpenBao license.
- Central ACLs, workload identities, audit logging, rotation, and rewrapping replace application-managed key files and custom cryptographic key handling.
- State Registry remains the sole canonical owner of team-scoped environment, logical secret, and secret-version records.
- State Registry authorization fails before decryption, limiting OpenBao use to an already verified team and assignment context.

Negative consequences:

- OpenBao becomes required infrastructure for secret writes and open-environment reads.
- Operations must maintain OpenBao availability, storage backups, seal recovery, certificates, policies, and upgrades.
- Loss of OpenBao key material or its seal dependencies can make existing ciphertext permanently unrecoverable even when PostgreSQL backups remain intact.
- Network calls to Transit add latency to secret writes and authorized environment opens.

Failure isolation:

- When OpenBao is unavailable, State Registry SHALL fail secret writes and open-environment requests closed without falling back to plaintext or local application keys.
- Task ingestion, discovery, approvals, events, non-secret environment operations, controls, and team-scoped state reads SHOULD remain available.
- Logs and audit records contain team and resource identifiers, actor, request, key-version metadata, and outcome only; never plaintext.

Implementation follow-up:

- Define the OpenBao Transit key path, ACL policy, workload-auth method, rotation policy, seal strategy, backup procedure, and recovery runbook before production deployment.
- Implement a typed Go Transit client with timeouts, TLS verification, token renewal, bounded retries, and no plaintext logging.
- Add integration tests proving encryption, decryption, key rotation compatibility, fail-closed behavior, same-team authorization, foreign-team denial before Transit calls, and recovery of older ciphertext versions.

## ADR: Sign scope tokens with an allow-listed HMAC and rotate server-controlled keys

### Status
Proposed

### Context
State Registry issues short-lived scope tokens that authorized Executors present at `GET /v1/environments/{environment_id}/open?task_id={task_id}`. Those tokens gate decrypted environment and secret access through OpenBao Transit, so a token forgery, replay, or post-terminal reuse would directly expose secret plaintext. The tokens must remain verifiable without round trips to OpenBao, must be bound to the authenticated assigned same-team Executor, must resist replay after terminal task state or unassignment, and must allow graceful key rotation without weakening audit or operational logging safety.

The platform needs a token security model that is compact, deterministic, and auditable, with explicit guarantees for algorithm strength, key control, claim verification, comparison safety, replay handling, rotation, and logging.

### Decision
State Registry SHALL be the sole issuer and verifier of open-environment scope tokens. Each token SHALL be signed using an allow-listed HMAC algorithm from the set `HS256`/`HS384`/`HS512` (the "HMAC-SHA-256 or a stronger HMAC" family), keyed with a State Registry-controlled key selected by a `key_id` claim included in the payload. The compact three-part wire format is `<header>.<payload>.<signature>`; the protected header SHALL carry `alg` (allow-listed), `kid`, and `typ`; the payload SHALL carry `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry`, and `key_id`. The protected-header `kid` SHALL equal the payload `key_id`. `issued_at` SHALL satisfy `issued_at <= server_now + 30 seconds`, `expiry` SHALL be strictly later than `issued_at`, and `expiry - issued_at` SHALL NOT exceed five minutes.

State Registry SHALL verify that the protected-header `kid` equals the payload `key_id` before any MAC computation, SHALL recompute the signature under the declared allow-listed algorithm using the server-side key handle, SHALL compare the result with a constant-time comparison before any other check, SHALL accept only `key_id` values inside the documented active key window for HMAC rotation, SHALL require an `audience` claim that matches the documented literal identifier `state-registry.environment.open`, SHALL perform canonical claim verification (presence, type, encoding, and allowed values for every claim, including that `project_id` is required but nullable only when the canonical environment has no project scope) before any team, assignment, applicability, or OpenBao operation, and SHALL never log token plaintext, individual claim values, MAC bytes, key material, or derived key bytes in application logs, audit records, or error responses.

A token SHALL be accepted only when the calling authenticated Executor identity is the currently assigned same-team Executor for the referenced task and the referenced task is in a non-terminal state. State Registry SHALL reject any other identity, a terminal task state, or an unassigned Executor before any OpenBao operation. State Registry MAY allow a retry of the same token within its TTL by the same assigned same-team Executor; the retry SHALL NOT bypass canonical claim, transition, or assignment checks, SHALL NOT extend TTL, and SHALL NOT revive an expired token.

Key material SHALL be loaded from a secure configuration source. State Registry SHALL document an active key window: each `key_id` maps to a server-controlled key, retired keys SHALL be accepted only during a documented overlap period for rotation, and key material SHALL NOT be persisted alongside token material or logs. Tokens SHALL be issued on atomic task approval; re-issuance after a transition that invalidates a token SHALL require a fresh approval response and SHALL NOT reuse the prior `key_id`-signed envelope.

### Consequences
Positive consequences:

- Allow-listed HMAC (`HS256`/`HS384`/`HS512`) with a constant-time comparison gives a stable, well-analyzed algorithm family and removes timing-side-channel exposure during verification.
- Binding every token to a `key_id` with a documented active window lets State Registry rotate keys without breaking in-flight tokens and without admitting forged or retired-key tokens.
- The five-minute expiry ceiling and required `audience` claim minimize replay opportunity and let multiple cooperating services reject tokens minted for a different verifier.
- Canonical claim verification before any team, assignment, applicability, or OpenBao step ensures malformed or unexpected tokens cannot trigger downstream state reads or secret decryption.
- Denying tokens for terminal task state, unassigned Executors, or different Executor identities before OpenBao keeps OpenBao usage strictly within authorized same-team task windows and preserves the OpenBao failure-isolation posture.
- Banning token plaintext, claim values, MAC bytes, and key material from logs and audit records keeps the most sensitive token and key data out of long-lived observability surfaces.
- Allowing same-identity retry within TTL without bypassing canonical checks improves Executor robustness against transient network or restart failures without weakening security.

Negative consequences:

- State Registry becomes the sole token verifier and must own the documented active key window, the configuration source for keys, and the rotation policy.
- The five-minute TTL requires Executors to refresh tokens promptly after approval and may complicate retries that span task reassignment or restart; explicit recovery flows remain necessary.
- Storing key material separately from token data and ensuring no logging of sensitive material adds configuration, deployment, and CI hygiene requirements.
- A documented active key window for rotation adds operational coordination between key provisioning, deployment, and Registry restarts.
- Constant-time MAC verification and canonical claim checking add small per-request CPU costs compared with non-verifying paths, but these costs are acceptable for open-environment traffic.

Failure isolation:

- When State Registry cannot verify a token (expired, foreign team, foreign identity, terminal task state, retired `key_id`, malformed claim, or constant-time MAC failure), it SHALL reject the request closed without any OpenBao operation and SHALL append a plaintext-free audit entry.
- When OpenBao is unavailable, State Registry SHALL still perform token verification and the audit append; only the decrypt step depends on OpenBao availability.
- Logs and audit records SHALL contain only permitted identifier metadata, decision, and outcome; never token plaintext, individual claim values, MAC bytes, or key material.
