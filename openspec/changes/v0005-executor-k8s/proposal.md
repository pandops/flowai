# Change: v0005-executor-k8s

## Why

After durable State Registry task intake and assignment exist, FlowAI needs a
cluster Executor that runs approved tasks as Kubernetes Pods without changing
State Registry ownership. The platform also needs tenant isolation between
teams so a K8s Executor registered for one team cannot discover, approve,
emit events against, open environments for, or read controls of tasks that
belong to another team.

This change introduces the team binding contract for the K8s Executor: one
immutable `team_id` per Executor, one `authorized_tag`, both bound to the
authenticated Executor service identity. The State Registry treats
`team_id` as authoritative for matching and authorization; `team_name` is a
display-only label. Cross-team interactions are non-revealing in two
distinct ways: collection-level discovery (the read-side task list) is
filtered by `team_id` BEFORE result shaping, so a tag whose matching tasks
all belong to other teams yields the NORMAL empty result (`204 No Content`
or `200` with an empty task list and no count, cursor, total, or pagination
metadata), indistinguishable from "no tasks match this tag anywhere", so
the existence of tasks in other teams is never disclosed through
collection responses. Point-resource lookups (approval for a specific
`task_id`, event write for a specific `task_id`, control read for a
specific `task_id`, environment open for a specific `environment_id` bound
to a specific `task_id`, task detail read for a specific `task_id`) yield a
non-revealing `404` indistinguishable from "resource does not exist". In
both cases cross-team interactions never produce a Pod and never append
an event.

Local capacity stays a local Executor concern. The State Registry does not
read, compare, or enforce `max_capacity` or `running_count` during discovery
or approval, and the K8s Executor remains non-authoritative across restarts
so a restart reconciles in-flight tasks back to their existing assignment
without reassignment or duplicate events.

## What Changes

- Add the K8s Executor as a separate executor type, registered as
  `executor_type = "k8s"`.
- Require every K8s Executor registration to declare exactly one
  `team_id`, exactly one `authorized_tag`, observed `max_capacity`, observed
  `running_count`, an optional display-only `team_name`, and runtime
  metadata, all bound to the authenticated Executor service identity.
- Make `team_id` authoritative for the Executor: the value supplied at
  registration MUST match the team bound to the authenticated identity, MUST
  be stored on the canonical Executor record, and SHALL NOT be changed by any
  later re-registration, restart, or reassignment.
- Limit discovery, approval, task-event writes, environment opens, and
  control reads to the Executor's bound `team_id`. Collection-level
  discovery SHALL filter by `team_id` BEFORE shaping results so that a tag
  whose matching `created` tasks all belong to other teams yields the
  NORMAL empty result (`204 No Content` or `200` with an empty task list
  and no count, cursor, total, or pagination metadata), indistinguishable
  from "no tasks match this tag anywhere" — never `404`. Point-resource
  lookups (approval for a specific `task_id`, event write for a specific
  `task_id`, control read for a specific `task_id`, environment open for a
  specific `environment_id` bound to a specific `task_id`, task detail
  read for a specific `task_id`) SHALL yield a non-revealing `404`
  indistinguishable from "resource does not exist" when the referenced
  resource belongs to a different team. Cross-team interactions never
  create a Pod and never append an event.
- Discover only `created` tasks whose `required_tag` equals the Executor's
  single `authorized_tag` AND whose `team_id` equals the Executor's bound
  `team_id`. Discovery is read-only, SHALL NOT reserve or assign a task, and
  SHALL NOT read, compare, or enforce `max_capacity` or `running_count`.
- Run approved tasks only as Kubernetes Pods after explicit State Registry
  start approval, emitting a `running` event before Pod creation and exactly
  one of `finished` or `failed` at terminal state. Every Executor-emitted
  task or self event SHALL carry `task_id` (when applicable), `executor_id`,
  `team_id`, `event_id`, `event_type`, `occurred_at`, and `payload`. The
  State Registry event-denial taxonomy SHALL be: an authenticated Executor
  whose envelope `team_id` differs from its immutable service binding is
  rejected with `403 team_mismatch`; a same-team Executor that is not the
  recorded `tasks.executor_id` is rejected with `403 not_assigned`; a
  foreign task or Executor point identifier is rejected with the same
  non-revealing `404` shape used for an unknown identifier; no `403`
  response is returned for a foreign-team probe.
- Observe child Pods and report `max_capacity` and `running_count` as
  informational observations only. The State Registry SHALL NOT gate
  discovery or approval on these observations, and SHALL NEVER emit a
  capacity-based rejection. The Executor alone decides when local capacity
  permits a new discovery or approval request.
- Open assigned task environments from State Registry only after the
  Executor is bound to the assigned task's `team_id` AND presents the
  compact three-part signed scope token `<header>.<payload>.<signature>`
  carried only in the `X-FlowAI-Scope-Token` request header for
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`. The
  protected header SHALL carry `alg` (allow-listed), `kid`, and `typ` (`scope-token+json`),
  and the payload SHALL carry `team_id`, `project_id` (a required
  claim; nullable only when the canonical environment has no project
  scope), `task_id`, `environment_id`, `executor_id`, `audience`
  (literal `state-registry.environment.open`), `issued_at`, `expiry`
  (`expiry > issued_at` and `expiry - issued_at <= 5 minutes`), and
  `key_id` selecting a State Registry-controlled active key. The State
  Registry SHALL verify that the protected-header `kid` equals the
  payload `key_id` BEFORE any MAC computation, SHALL accept only
  algorithms in the allow-listed HMAC set `HS256`/`HS384`/`HS512` (the
  "HMAC-SHA-256 or a stronger HMAC" family), SHALL recompute the
  signature under the declared allow-listed algorithm using the
  server-side key handle and compare it under constant-time comparison,
  SHALL verify the `key_id` against the documented active key window
  (retired keys rejected), SHALL verify `issued_at <= server_now + 30
  seconds`, SHALL verify the literal audience `state-registry.environment.open`, SHALL verify
  the canonical claim shape (including the project-scope rule for
  `project_id`), SHALL verify the authenticated Executor mTLS identity,
  the Executor's same-team ownership, the task assignment, the task's
  non-terminal state, and the project/task applicability BEFORE any
  OpenBao operation or plaintext disclosure. A retry within the token
  TTL by the same assigned same-team Executor MAY be allowed only when
  every canonical claim, transition, and assignment check still passes;
  it SHALL NOT extend TTL, SHALL NOT bypass canonical checks, and SHALL
  NOT revive an expired token. Any invalid or unavailable condition —
  missing or empty token, tampered MAC, algorithm outside the
  allow-listed HMAC set, `kid` not equal to payload `key_id`, `key_id`
  outside the active window, lifetime exceeding five minutes, expired
  or premature issuance, audience mismatch, canonical-claim mismatch
  (including a null `project_id` for a project-scoped environment, or a
  missing `project_id`), `team_id` mismatch, terminal task state, or
  not-assigned caller — SHALL return the same non-revealing `404
  environment_unknown_or_unavailable` shape with zero OpenBao calls and
  no token plaintext, individual claim values beyond identifier-level
  metadata, MAC bytes, key material, or derived key bytes in logs,
  audit entries, or error responses. Plaintext values SHALL exist only
  in memory while serving the open-environment response and SHALL be
  injected only into the assigned Pod.
- Read pending operator controls only for tasks assigned to the Executor in
  the Executor's bound `team_id`. Controls for tasks assigned to a different
  Executor or a different team are never read or applied.
- Reconcile to State Registry after restart by re-reading assigned tasks
  from durable state. The K8s Executor SHALL NOT re-discover and SHALL NOT
  re-approve already-dispatched tasks. A restarted K8s Executor SHALL NOT
  create a duplicate Pod for an already-running task and SHALL NOT emit a
  duplicate `running` event for it.

## Impact

- Change type: development
- Affected specs: `openspec/changes/v0005-executor-k8s/specs/executor/spec.md`
- Affected ADRs: none (existing v0002 ADRs continue to govern State Registry
  contract; this change only adds Executor-side team binding rules).
- Affected diagrams:
  - `openspec/changes/v0005-executor-k8s/specs/diagrams/01-k8s-executor-topology.puml`
  - `openspec/changes/v0005-executor-k8s/specs/diagrams/02-k8s-task-lifecycle.puml`
  Both remain in the `sequence` family; no state-machine, ER, or other
  restricted diagram family is added.
- Affected test cases: `openspec/changes/v0005-executor-k8s/specs/test-cases/`
  (new directory; nine E2E definitions, contiguous `v0005.1` through
  `v0005.9`).
- Affected code: future `executor-k8s/` service (new top-level directory;
  not created by this OpenSpec change).

## Out of scope

- Replace Docker Executor.
- Add Web UI or auth.
- Make State Registry call Kubernetes directly.
- Add a separate team-management API, team CRUD, or team directory in
  State Registry. Team identity is supplied by the authenticated Executor
  service identity; no Registry-side team table is introduced by this
  change.
- Add wildcard tags, tag expressions, or cross-team task routing.
- Make State Registry enforce Executor capacity in any form, including
  `max_capacity` or `running_count`.