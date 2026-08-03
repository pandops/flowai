# Change: v0005-executor-k8s

## Why

After durable State Registry task intake and FIFO claim exist, FlowAI
needs a cluster Executor that runs claimed tasks as Kubernetes Pods
without changing State Registry ownership. The platform also needs
tenant isolation between teams while preserving the State Registry's
registration-time ownership choice: a team-owned K8s Executor is confined
to one immutable team, while a system-owned K8s Executor may dispatch
matching work across teams without changing the owning `tasks.team_id`.

This change aligns the K8s Executor with the revised v0002 State
Registry client contract: pending FIFO discovery, atomic FIFO claim with
a stable `command_id`, claim-time Registry-appended FIRST `created`
event with non-null claiming `executor_id` and payload meaning
`task <task_id> loaded by <executor_id>`, ownership fields
(`tasks.owner_command_id`, `tasks.executor_id`) immutable from claim
onward, `tasks.resolved_image` used verbatim with no K8s local image
fallback, the unified claim-conflict taxonomy (`404` for unknown or
foreign, `409 task_already_claimed` for different command on a claimed
task, `409 older_task_must_be_claimed_first` for eligible non-oldest,
`200 claimed` for the same `(task_id, command_id)` retry by the original
Executor), and the removal of the legacy `created` -> `dispatched`
state and `dispatched` event. The same concrete K8s service supports both
ownership scopes; scope is chosen at first registration and is immutable.

The change applies the current Executor ownership contract to the K8s
implementation: one immutable `scope` from `{team, system}`, one
`authorized_tag`, and, only for `scope = team`, one immutable `team_id`
bound to the authenticated Executor service identity. The State Registry
treats `team_id` as authoritative for task ownership and treats
`team_name` as a display-only label.
Cross-team interactions are non-revealing in two distinct ways:
collection-level discovery (the read-side task list) is filtered by
`team_id` BEFORE result shaping, so a tag whose matching `pending`
tasks all belong to other teams yields the NORMAL empty result
(`204 No Content` or `200` with an empty task list and no count,
cursor, total, or pagination metadata), indistinguishable from "no
tasks match this tag anywhere", so the existence of tasks in other teams
is never disclosed through collection responses. Point-resource lookups
(claim for a specific `task_id`, event write for a specific `task_id`,
control read for a specific `task_id`, environment open for a specific
`environment_id` bound to a specific `task_id`, task detail read for a
specific `task_id`) yield a non-revealing `404` indistinguishable from
"resource does not exist". In both cases cross-team interactions never
produce a Pod and never append an event.

Local capacity stays a local Executor concern. The State Registry does
not read, compare, or enforce `max_capacity` or `running_count` during
discovery or claim, and the K8s Executor remains non-authoritative
across restarts so a restart reconciles in-flight tasks back to their
existing canonical assignment (`tasks.executor_id` and
`tasks.owner_command_id`) without re-claim, reassignment, or duplicate
events.

## What Changes

- Add a concrete K8s Executor service named `executor_k8s_<tool>`, where
  the agent tool is selected before implementation. Its directory, binary,
  config, import path, wire `executor_type`, slog/probe service name, and
  constant regression test derive from that same concrete identifier. The
  change ID remains `v0005-executor-k8s`.
- Require every K8s Executor registration to declare exactly one immutable
  `scope` from `{team, system}`, exactly one `authorized_tag`, observed
  `max_capacity`, observed `running_count`, and runtime metadata. Team scope
  requires exactly one immutable identity-bound `team_id` and permits an
  optional display-only `team_name`; system scope requires no `team_id` and
  no team binding.
- Make `team_id` authoritative for a team-owned Executor: the value
  supplied at registration MUST match the team bound to the
  authenticated identity, MUST be stored on the canonical Executor
  record, and SHALL NOT be changed by any later re-registration,
  restart, or reassignment.
- Apply scope before discovery, claim, task-event writes, environment opens,
  and control reads. For team scope, collection-level
  discovery SHALL filter by `team_id` BEFORE shaping results so that a
  tag whose matching `pending` tasks all belong to other teams yields
  the NORMAL empty result (`204 No Content` or `200` with an empty
  task list and no `count`, `cursor`, `total`, or pagination metadata),
  indistinguishable from "no tasks match this tag anywhere" — never
  `404`. Point-resource lookups (claim for a specific `task_id`, event
  write for a specific `task_id`, control read for a specific
  `task_id`, environment open for a specific `environment_id` bound to
  a specific `task_id`, task detail read for a specific `task_id`)
  SHALL yield a non-revealing `404` indistinguishable from "resource
  does not exist" when the referenced resource belongs to a different
  team. Cross-team interactions never create a Pod and never append an
  event. For system scope, matching is by the registered tag across teams;
  every claim response and later event/environment/control request derives
  its immutable `team_id` from the claimed task.
- Discover only `pending` tasks whose `required_tag` equals the Executor's
  single `authorized_tag`; team scope additionally requires the task's
  `team_id` to equal the Executor's bound `team_id`, while system scope spans
  teams and returns metadata-only summaries until claim. Discovery results SHALL be ordered
  `(ingested_at ASC, task_id ASC)` with the eligibility predicate
  applied first, BEFORE pagination, and BEFORE counts. Discovery is
  read-only, SHALL NOT reserve or assign a task, and SHALL NOT read,
  compare, or enforce `max_capacity` or `running_count`. The K8s
  Executor SHALL choose the oldest eligible `pending` task and SHALL
  NOT skip it.
- Claim tasks through `POST /v1/executors/{executor_id}/claim` with the
  selected `task_id` and a stable `command_id`. The State Registry SHALL
  atomically (a) set `tasks.owner_command_id` (immutable) to the
  request `command_id`; (b) set `tasks.executor_id` (immutable) to the
  claiming Executor; (c) persist `tasks.resolved_image` and
  `tasks.image_source` from the four-level precedence
  (`tasks.image` -> `task_types.default_image` ->
  `source_systems.default_image` -> required `teams.default_image`),
  which always succeeds because `teams.default_image` is required at
  admin registration; (d) set `tasks.claimed_at` to the transaction
  commit time; (e) append the FIRST lifecycle event `created` with
  `executor_id` non-null and equal to the claiming Executor and a
  payload meaning `task <task_id> loaded by <executor_id>`; and (f)
  remove the task from this Executor's scope of discovery. The K8s
  Executor SHALL use `tasks.resolved_image` verbatim and SHALL refuse
  the task if it cannot be pulled or started; the Executor SHALL NOT
  maintain a local fallback image and SHALL NOT substitute its own
  image for the Registry-resolved one. The claim-conflict taxonomy is
  non-revealing `404` for unknown or foreign `task_id`, `409
task_already_claimed` for a different `command_id` on an
  already-claimed task, `409 older_task_must_be_claimed_first` for an
  eligible non-oldest task, and the original `200 claimed` body for
  the same `(task_id, command_id)` retry by the original Executor. The
  K8s Executor SHALL NOT start a Pod before `200 claimed`, SHALL NOT
  start a Pod after `404`, `409 task_already_claimed`, or `409
older_task_must_be_claimed_first`, and SHALL return to discovery.
  No `dispatched` lifecycle state or event exists; no lifecycle event is
  appended at ingestion when the task is `pending`.
- Run claimed tasks only as Kubernetes Pods after `200 claimed`. The
  Pod SHALL carry the immutable labels `flowai.executor_id`,
  `flowai.team_id`, `flowai.task_id`, `flowai.command_id`,
  `flowai.executor_scope`, `flowai.resolved_image_source`, and the
  runtime label `flowai.runtime=k8s`. The assigned Executor SHALL emit
  a `running` event after the claim succeeds and SHALL emit exactly one
  of `finished` or `failed` at terminal state. The Executor SHALL NOT
  append a `created` event itself; the FIRST lifecycle event is
  appended transactionally by the State Registry on claim. Every
  Executor-emitted task or self event SHALL carry `task_id` (when
  applicable), `executor_id`, `team_id`, `event_id`, `event_type`,
  `occurred_at`, and `payload`. The State Registry event-denial
  taxonomy SHALL be: an authenticated Executor whose envelope `team_id`
  differs from its immutable service binding is rejected with `403
team_mismatch`; a same-team Executor that is not the recorded
  `tasks.executor_id` is rejected with `403 not_assigned`; a foreign
  task or Executor point identifier is rejected with the same
  non-revealing `404` shape used for an unknown identifier; no `403`
  response is returned for a foreign-team probe.
- Observe child Pods and report `max_capacity` and `running_count` as
  informational observations only. The State Registry SHALL NOT gate
  discovery or claim on these observations, and SHALL NEVER emit a
  capacity-based rejection. The Executor alone decides when local
  capacity permits a new discovery or claim request.
- Open assigned task environments from State Registry only after the
  Executor is bound to the assigned task's `team_id` AND presents the
  compact three-part signed scope token `<header>.<payload>.<signature>`
  carried only in the `X-FlowAI-Scope-Token` request header for
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`. The
  protected header SHALL carry `alg` (allow-listed), `kid`, and `typ`
  (`scope-token+json`), and the payload SHALL carry `team_id`,
  `project_id` (a required claim; nullable only when the canonical
  environment has no project scope), `task_id`, `environment_id`,
  `executor_id`, `audience` (literal
  `state-registry.environment.open`), `issued_at`, `expiry`
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
seconds`, SHALL verify the literal audience
  `state-registry.environment.open`, SHALL verify the canonical claim
  shape (including the project-scope rule for `project_id`), SHALL
  verify the authenticated Executor mTLS identity, the Executor's
  same-team ownership, the task assignment, the task's non-terminal
  state, and the project/task applicability BEFORE any decrypt
  operation by the active provider or plaintext disclosure. A retry
  within the token TTL by the same assigned same-team Executor MAY be
  allowed only when every canonical claim, transition, and assignment
  check still passes; it SHALL NOT extend TTL, SHALL NOT bypass
  canonical checks, and SHALL NOT revive an expired token. Any invalid
  or unavailable condition — missing or empty token, tampered MAC,
  algorithm outside the allow-listed HMAC set, `kid` not equal to
  `key_id`, `key_id` outside the active window, lifetime exceeding five
  minutes, expired or premature issuance, audience mismatch,
  canonical-claim mismatch (including a null `project_id` for a
  project-scoped environment, or a missing `project_id`), `team_id`
  mismatch, terminal task state, or not-assigned caller — SHALL return
  the same non-revealing `404 environment_unknown_or_unavailable` shape
  with zero provider decrypt operations and no token plaintext,
  individual claim values beyond identifier-level metadata, MAC bytes,
  key material, or derived key bytes in logs, audit entries, or error
  responses. Plaintext values SHALL exist only in memory while serving
  the open-environment response and SHALL be injected only into the
  assigned Pod.
- Read pending operator controls only for tasks assigned to the
  Executor in the Executor's bound `team_id`. Controls for tasks
  assigned to a different Executor or a different team are never read
  or applied.
- Reconcile to State Registry after restart by re-reading claimed,
  non-terminal tasks from durable state using the canonical assignment
  identity `tasks.executor_id = authenticated_executor_id` (NEVER
  filtering by `tasks.owner_command_id` during the re-read query), and
  then matching each returned task's immutable `tasks.owner_command_id`
  to the existing Pod's `flowai.command_id` label to identify which
  in-flight Pod to continue observing. The K8s Executor SHALL NOT
  re-discover and SHALL NOT re-claim already-claimed tasks. A
  restarted K8s Executor SHALL NOT create a duplicate Pod for an
  already-running task and SHALL NOT emit a duplicate `running` event
  for it.

## Impact

- Change type: development
- Affected specs: `openspec/changes/v0005-executor-k8s/specs/executor/spec.md`
- Affected ADRs: none (existing v0002 ADRs continue to govern State
  Registry contract; this change only aligns Executor-side client
  behavior with the revised v0002 FIFO claim contract).
- Affected diagrams:
  - `openspec/changes/v0005-executor-k8s/specs/diagrams/01-k8s-executor-topology.puml`
  - `openspec/changes/v0005-executor-k8s/specs/diagrams/02-k8s-task-lifecycle.puml`
    Both remain in the `sequence` family; no state-machine, ER, or other
    restricted diagram family is added.
- Affected test cases: `openspec/changes/v0005-executor-k8s/specs/test-cases/`
  (nine E2E definitions, contiguous `v0005.1` through `v0005.9`,
  updated in place to the FIFO claim contract).
- Affected code: future `executor_k8s_<tool>/` service (new top-level
  directory; not created by this OpenSpec change).

## Out of scope

- Replace Docker Executor.
- Add Web UI or auth.
- Make State Registry call Kubernetes directly.
- Add a separate team-management API, team CRUD, or team directory in
  State Registry. Team identity is supplied by the authenticated
  Executor service identity; no Registry-side team table is introduced
  by this change.
- Add wildcard tags, tag expressions, or cross-team task routing.
- Make State Registry enforce Executor capacity in any form, including
  `max_capacity` or `running_count`.
- Reintroduce a `dispatched` lifecycle state or `dispatched` event.
- Provide a K8s-local fallback image when `resolved_image` cannot be
  pulled or started.

## Removed diagrams

- none
