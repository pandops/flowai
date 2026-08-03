# ADR: Use an event-sourced task lifecycle with normalized 3NF PostgreSQL persistence

### Status

Accepted

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

## More Information

Supersedes: `v0002-state-registry`
