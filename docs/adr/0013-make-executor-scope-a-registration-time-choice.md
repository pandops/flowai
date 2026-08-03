# ADR: Make Executor ownership scope a registration-time choice between team-owned and system-owned

### Status

Accepted

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

## More Information

Supersedes: `v0002-state-registry`
