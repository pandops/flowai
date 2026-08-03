# ADR: Use FIFO claim-by-oldest-eligible-task dispatch

### Status

Accepted

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

## More Information

Supersedes: `v0002-state-registry`
