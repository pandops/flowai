# Tasks

Implementation SHALL proceed in the order below. A GREEN task may start only
after its corresponding RED task has failed for the expected
behavior-specific reason. Record the exact RED and GREEN command/result
before checking an implementation task. The K8s Executor SHALL NOT be
implemented before every `v0005.<n>` definition has a runnable Playwright
test in `autotest/k8s-executor/tests/` whose title contains the immutable
`v0005.<n>` id.

## 1. Implementation-start test activation and harness

- [ ] Move every `openspec/changes/v0005-executor-k8s/specs/test-cases/v0005.<n>-*.md`
  unchanged into `autotest/test-cases/` as the first implementation mutation;
  verify ordinals remain contiguous from `v0005.1` through `v0005.9`,
  destinations do not collide, and no v0005 definition remains under the
  change folder.
- [ ] Bootstrap `autotest/k8s-executor/` as an API-only Playwright suite that
  starts the real State Registry and PostgreSQL processes, supplies
  authenticated Executor service identities for two distinct teams
  (`team-A` and `team-B`), supports controlled K8s Executor process
  restarts, and drives only supported HTTP/read interfaces; verify the
  harness can report a behavior-specific connection or unimplemented-operation
  failure rather than a fixture or dependency failure.
- [ ] Add one runnable Playwright test for every moved v0005 definition and
  make each test title contain its immutable `v0005.<ordinal>` id; leave
  every `## Implementation reference` blank until the corresponding test has
  a stable file path and test title.

## 2. Team-bound registration and identity immutability

- [ ] **RED E2E:** implement the runnable tests for `v0005.1` (happy-path
  registration with one `team_id`, one `authorized_tag`, observed
  `max_capacity`, observed `running_count`, optional `team_name`, metadata,
  bound to the authenticated Executor identity) and `v0005.2`
  (registration with a `team_id` that does not match the
  identity-bound team is rejected without persistence); run
  `npm --prefix autotest/k8s-executor test -- --grep 'v0005\.(1|2)\b'` and
  verify failure because team-bound registration, identity match, and
  immutability rules are absent.
- [ ] **RED unit/integration:** add table-driven tests for zero, one, and
  multiple `team_id` submissions, identity-mismatched `team_id`, and
  re-registration with a different `team_id`; verify they fail before
  team validation and registration code exists.
- [ ] **GREEN:** implement authenticated K8s Executor registration that
  declares exactly one scalar `team_id`, exactly one scalar
  `authorized_tag`, an optional display-only `team_name`, observed
  `max_capacity`, observed `running_count`, and runtime metadata; accept a
  registration only when the supplied `team_id` matches the team carried by
  the authenticated Executor service identity; reject zero or multiple
  `team_id` submissions, identity-mismatched `team_id`, and any
  re-registration whose `team_id` differs from the stored `team_id`.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright command and the
  related Go tests; require concrete `200`, `400 invalid_team_id_count`,
  `403`, and `404` outcomes, persistence of the supplied `team_id` on the
  canonical Executor record, and immutability of `team_id` across
  re-registration.
- [ ] **REFACTOR:** isolate identity verification and `team_id` immutability
  checks behind narrow Executor-client interfaces while keeping the
  targeted and related tests green.

## 3. Team-isolated discovery and approval

- [ ] **RED E2E:** implement the runnable test for `v0005.3`
  (collection-level discovery of a tag whose matching `created` tasks all
  belong to a different `team_id` returns the NORMAL empty result — `204
  No Content` or `200` with an empty task list and no `count`, `cursor`,
  `total`, or pagination metadata — indistinguishable from "no tasks
  match this tag anywhere"; point-resource lookups against a foreign-team
  `task_id` (approval, task detail read) return a non-revealing `404`
  indistinguishable from "resource does not exist"; in both cases the
  Executor creates no Pod and appends no event); run
  `npm --prefix autotest/k8s-executor test -- --grep 'v0005\.3\b'` and
  verify failure because same-team exact-tag discovery, the
  team-filter-before-result-shaping rule, and the non-revealing
  cross-team `404` rule on point resources are absent.
- [ ] **RED unit/integration:** add discovery-query tests covering same-team
  match, no-match-anywhere, foreign-team-only match (returns the normal
  empty result with no leak), and registered-tag-only enforcement; add
  point-resource tests for the foreign-team `404`; verify they fail
  before the team-scoped discovery and approval code exists.
- [ ] **GREEN:** implement collection-level discovery that filters by
  `team_id` BEFORE shaping results, returning only `created` tasks whose
  `required_tag` equals the Executor's single `authorized_tag` AND whose
  `team_id` equals the Executor's bound `team_id`; keep discovery
  read-only, SHALL NOT reserve or assign a task, SHALL NOT read, compare,
  or enforce `max_capacity` or `running_count`; ensure the empty-result
  payload contains no `count`, `cursor`, `total`, or pagination metadata
  that would distinguish a foreign-team match from a no-match-anywhere.
- [ ] **GREEN:** implement point-resource approval that accepts only
  `created` tasks whose `team_id` equals the Executor's bound `team_id`
  AND whose `required_tag` matches the registered `authorized_tag`;
  treat a foreign-team `task_id` identically to an unknown `task_id` for
  approval, event write, task detail read, environment open, and control
  read, returning a non-revealing `404` without appending any event.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests; require
  the NORMAL empty collection result (`204 No Content` or `200` with an
  empty task list and no `count`, `cursor`, `total`, or pagination
  metadata) for foreign-team-only discovery, a non-revealing `404` for
  point-resource lookups (approval, event write, task detail read,
  environment open, control read) against a foreign-team identifier, no
  Pod, no event, unchanged `created` state and null assignment, and
  never `422 executor_at_capacity` or any capacity-based rejection.
- [ ] **REFACTOR:** consolidate team-scoped query construction and
  approval guards behind narrow interfaces while keeping all
  discovery/approval tests green.

## 4. Pod lifecycle with team_id envelope and team-scoped event writes

- [ ] **RED E2E:** implement the runnable tests for `v0005.4` (same-team
  approved task emits a `running` event before Pod creation with the full
  envelope — `task_id`, `executor_id`, `team_id`, `event_id`, `event_type =
  running`, `occurred_at`, `payload` — and emits exactly one of `finished`
  or `failed` at terminal state with the same envelope shape) and `v0005.5`
  (a cross-team event post returns the same non-revealing `404` shape used
  for an unknown identifier and appends no event; a same-team Executor
  that is not the recorded `tasks.executor_id` returns `403 not_assigned`;
  an authenticated Executor whose envelope `team_id` differs from its
  immutable service binding returns `403 team_mismatch`); run
  `npm --prefix autotest/k8s-executor test -- --grep 'v0005\.(4|5)\b'` and
  verify failure because the team_id envelope, `running`-before-Pod
  ordering, and the event-denial taxonomy are absent.
- [ ] **RED unit/integration:** add transition and envelope tests covering
  the seven-field envelope for `running`/`finished`/`failed`, missing
  `team_id` rejection, envelope `team_id` mismatch (`403 team_mismatch`),
  unassigned same-team writer (`403 not_assigned`), and foreign point
  probe (`404`); verify behavior-specific failures before the envelope
  validation and event-denial taxonomy exist.
- [ ] **GREEN:** implement seven-field event envelope validation on every
  Executor-emitted task event (running/finished/failed) carrying
  `task_id`, `executor_id`, `team_id`, `event_id`, `event_type`,
  `occurred_at`, `payload`; emit `running` BEFORE creating the Kubernetes
  Pod, emit exactly one of `finished` or `failed` at terminal state,
  enforce the event-denial taxonomy — `403 team_mismatch` for envelope
  `team_id` mismatch, `403 not_assigned` for unassigned same-team
  writers, `404` for foreign task or Executor point identifiers, no
  `403` for foreign-team probes — and never create a Pod for a
  cross-team identifier.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests; require
  a `202 accepted` for every same-team event, a single terminal event per
  task, no Pod created before the accepted `running` event, no Pod
  created for cross-team identifiers, `403 team_mismatch` for envelope
  `team_id` mismatches, `403 not_assigned` for unassigned same-team
  writers, the same non-revealing `404` for foreign point probes and
  unknown identifiers, and no event appended on any rejection.
- [ ] **REFACTOR:** consolidate envelope validation and team-scoped event
  filtering behind narrow Executor-client interfaces without separating
  envelope validation from event forwarding; rerun all targeted tests.

## 5. Local capacity ownership (no Registry-side rejection)

- [ ] **RED E2E:** implement the runnable test for `v0005.6`
  (`max_capacity = running_count` does not cause the Registry to reject
  approval; the Executor alone decides when to start a Pod); run
  `npm --prefix autotest/k8s-executor test -- --grep 'v0005\.6\b'` and
  verify failure because the saturated-capacity rule, capacity-independent
  approval, and local-only slot enforcement are absent.
- [ ] **RED unit/integration:** add capacity-observation tests covering
  saturated `running_count = max_capacity`, sparse `running_count <
  max_capacity`, and self-event observation writes; verify behavior-specific
  failures before capacity handling code exists.
- [ ] **GREEN:** keep the existing v0002 capacity-observation contract:
  observe Pods locally, request approval only when a local slot is
  available, start no Pod before `200 approved`, and start none after
  `409`; report `max_capacity` and `running_count` through
  `POST /v1/executors/{executor_id}/events` with the seven-field envelope
  including `team_id`.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests; require
  no `422 executor_at_capacity` and no capacity-based rejection from the
  Registry, no Pod created beyond local capacity, and self-events carrying
  `team_id`.
- [ ] **REFACTOR:** isolate the local capacity decision from the team-scoping
  rules so both stay independently testable; rerun all targeted tests.

## 6. Assigned task environment open with team-bound scope token (inherits finalized v0002 token contract)

- [ ] **RED E2E:** implement the runnable test for `v0005.7` (the assigned
  K8s Executor in the bound `team_id` opens the environment via
  `GET /v1/environments/{environment_id}/open?task_id={task_id}` with the
  compact three-part signed scope token `<header>.<payload>.<signature>`
  carried only in the `X-FlowAI-Scope-Token` request header; protected
  header carries `alg` (allow-listed `HS256`/`HS384`/`HS512`), `kid`, and
  `typ`; payload carries `team_id`, `project_id` (required claim; nullable
  only when the canonical environment has no project scope), `task_id`,
  `environment_id`, `executor_id`, `audience` (literal
  `state-registry.environment.open`), `issued_at`, `expiry` (`expiry >
  issued_at` and `expiry - issued_at <= 5 minutes`), and `key_id`
  selecting a State Registry-controlled active key; the protected-header
  `kid` SHALL equal the payload `key_id`; `issued_at <= server_now + 30
  seconds`; and every failure variation — missing/empty token, tampered
  MAC, algorithm outside the allow-listed HMAC set, `kid` not equal to
  `key_id`, `key_id` outside the documented active window (including a
  retired `key_id`), `expiry` not strictly later than `issued_at`,
  `expiry - issued_at > 5 minutes`, `issued_at` in the future beyond
  `server_now + 30 seconds`, audience mismatch, missing required claim
  (including a missing `project_id`), null `project_id` for a
  project-scoped environment, mutated `project_id`/`task_id`/
  `environment_id`/`executor_id`/`team_id`, terminal-state task,
  foreign-team or unassigned Executor — returns the same non-revealing
  `404 environment_unknown_or_unavailable` shape with no values and zero
  OpenBao calls); run
  `npm --prefix autotest/k8s-executor test -- --grep 'v0005\.7\b'` and
  verify failure because the inherited v0002 token-contract validation,
  the `kid == key_id` check before any MAC computation, the
  `issued_at`/`expiry` window, the project-scope rule for `project_id`,
  plaintext-free audit, and the uniform non-revealing `404` are absent.
- [ ] **RED unit/integration:** add token-validation tests covering the
  full binding matrix inherited from the v0002 finalized contract —
  protected-header `alg` allow-list, protected-header `kid` equals
  payload `key_id`, `team_id`, `project_id` (present and non-null when the
  canonical environment has a project scope), `task_id`, `environment_id`,
  `executor_id`, `audience` (literal `state-registry.environment.open`),
  `issued_at <= server_now + 30 seconds`, `expiry > issued_at`,
  `expiry - issued_at <= 5 minutes`, `key_id` against the documented
  active key window — plus MAC constant-time comparison, retired-key
  rejection, foreign-team requests, terminal task-state rejection,
  unassigned-Executor rejection, and uniform `404` parity across every
  invalid variation; verify behavior-specific failures before the
  inherited token-validation code exists.
- [ ] **GREEN:** inherit the finalized v0002 token contract verbatim:
  serve `GET /v1/environments/{environment_id}/open?task_id={task_id}`;
  require the compact three-part signed scope token in the
  `X-FlowAI-Scope-Token` request header (no token fields in the body or
  other headers); verify that the protected-header `kid` equals the
  payload `key_id` BEFORE any MAC computation; accept only algorithms in
  the allow-listed HMAC set `HS256`/`HS384`/`HS512`; recompute the
  signature under the declared allow-listed algorithm using the
  server-side key handle and compare it under constant-time comparison;
  verify `key_id` against the documented active key window (retired keys
  rejected); verify `issued_at <= server_now + 30 seconds`,
  `expiry > issued_at`, and `expiry - issued_at <= 5 minutes`; verify the
  literal `audience` is `state-registry.environment.open`; verify the
  canonical claim shape (including that `project_id` is present and
  non-null when the canonical environment has a project scope, and may
  be null only when the canonical environment has no project scope);
  verify every claim (`team_id`, `project_id`, `task_id`,
  `environment_id`, `executor_id`, `audience`, `issued_at`, `expiry`,
  `key_id`) against canonical records; verify the authenticated Executor
  mTLS identity is the assigned same-team Executor; verify the task is
  in a non-terminal state; verify the project/task applicability;
  perform every one of those checks BEFORE any OpenBao operation or
  plaintext disclosure; return env-style values only to the assigned
  same-team Executor over its authenticated mTLS identity; record a
  plaintext-free open-environment audit entry; never log token
  plaintext, individual claim values beyond identifier-level metadata,
  MAC bytes, key material, or derived key bytes; allow a same
  assigned-identity retry within TTL only when every canonical claim,
  transition, and assignment check still passes, and SHALL NOT extend
  TTL, SHALL NOT bypass canonical checks, and SHALL NOT revive an
  expired token; inject returned values only into that task's Pod and
  discard plaintext after Pod startup.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
  require a `200` with env-style values only for the fully-valid token
  from the assigned same-team Executor over its authenticated mTLS
  identity, the same non-revealing `404 environment_unknown_or_unavailable`
  shape for every invalid / tampered / audience-mismatched /
  missing-claim (including missing `project_id`) / null `project_id`
  for a project-scoped environment / retired-key / expired / `expiry <=
  issued_at` / TTL > 5 minute / premature-beyond-30s / foreign-team /
  unassigned / `kid` not equal to `key_id` / terminal-state /
  applicability-violating variation, zero OpenBao calls triggered for
  any failing variation, an audit entry without plaintext only from the
  valid request, and no plaintext in logs or durable Executor storage.
- [ ] **REFACTOR:** isolate the inherited v0002 token-validation,
  `key_id` rotation window, `kid == key_id` guard, MAC constant-time
  comparison, and plaintext-handling rules behind narrow Executor-client
  interfaces without leaking token plaintext, claims, or key material
  into logs; keep the targeted and related tests green.

## 7. Pending controls only for assigned tasks in the bound team

- [ ] **RED E2E:** implement the runnable test for `v0005.8` (the assigned
  same-team Executor reads and applies pending controls for its assigned
  task; a cross-team or unassigned control read returns a non-revealing
  `404`); run
  `npm --prefix autotest/k8s-executor test -- --grep 'v0005\.8\b'` and
  verify failure because assigned-task-only control reads and the
  cross-team `404` are absent.
- [ ] **RED unit/integration:** add control-read tests covering the
  assigned+same-team happy path, the cross-team denial, the unassigned
  denial, and the audit entry shape; verify behavior-specific failures
  before the control-read code exists.
- [ ] **GREEN:** poll the State Registry for pending controls only for
  tasks whose `task_id` is assigned to the Executor AND whose `team_id`
  equals the Executor's bound `team_id`; apply received controls to that
  task's Pod and record the resulting task events with the seven-field
  envelope (including `team_id`); never read, list, or apply controls for
  tasks in another team or assigned to another Executor.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests; require
  `200` with the pending control for the assigned same-team Executor, a
  non-revealing `404` for cross-team or unassigned requests, applied
  controls visible in task events, and no control applied across teams.
- [ ] **REFACTOR:** consolidate control-read scoping and audit hooks behind
  narrow Executor-client interfaces while keeping all targeted tests
  green.

## 8. Restart reconciliation without reassignment

- [ ] **RED E2E:** implement the runnable test for `v0005.9` (after a
  process restart, the K8s Executor re-registers with the same bound
  `team_id` and `authorized_tag`, re-reads its assigned tasks from the
  State Registry, observes existing Pods in Kubernetes, emits no duplicate
  `running` event, and emits exactly one terminal `finished` or `failed`
  event per Pod); run
  `npm --prefix autotest/k8s-executor test -- --grep 'v0005\.9\b'` and
  verify failure because non-reassigning reconciliation, `team_id`
  immutability across restart, and the no-duplicate-`running`-event rule
  are absent.
- [ ] **RED unit/integration:** add reconciliation tests covering same-team
  restart, divergent `team_id` rejection, no duplicate `running` event,
  and exactly one terminal event per Pod; verify behavior-specific
  failures before reconciliation code exists.
- [ ] **GREEN:** implement a restart reconciliation loop that
  re-registers with the same bound `team_id` (the State Registry rejects a
  divergent `team_id`), re-reads assigned tasks from durable state,
  observes existing Pods in Kubernetes, emits no duplicate `running`
  event, and emits exactly one terminal `finished` or `failed` event per
  Pod whose lifecycle ends.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests; require
  no reassignment, no duplicate `running` event, exactly one terminal
  event per Pod, and `team_id` immutability across the restart.
- [ ] **REFACTOR:** extract the reconciliation loop into a dedicated
  Executor-owned component without coupling it to discovery, approval, or
  Pod creation paths; rerun all targeted tests.

## 9. Architecture diagrams

- [x] Prepare both proposed v0005 sequence diagrams already present:
  `01-k8s-executor-topology.puml` (`sequence`) and
  `02-k8s-task-lifecycle.puml` (`sequence`); both updated to reflect the
  team-bound registration envelope, team-isolated discovery/approval,
  seven-field event envelope, team-bound scope-token environment open,
  assigned-only control reads, and restart reconciliation.
- [ ] Verify both diagrams still match the implemented
  registration/envelope/isolation/capacity/env/control/restart contract
  without adding a state-machine, ER, or other unrequested diagram
  family.
- [ ] Run
  `plantuml -checkonly openspec/changes/v0005-executor-k8s/specs/diagrams/*.puml`
  and require both sources to compile without errors.

## 10. Final verification and OpenSpec gates

- [ ] Fill every moved v0005 definition's `## Implementation reference`
  with its exact Playwright file path and test title; verify no test is
  skipped and every task records valid RED-before-GREEN evidence.
- [ ] Run `gofmt` on changed Go files, `go test -race ./...`,
  `go test -tags=integration ./executor-k8s/...`, and `go build ./...`;
  require all unit/integration/race/build checks to pass.
- [ ] Run the full K8s Executor Playwright suite with
  `npm --prefix autotest/k8s-executor test`; require every v0005 E2E to
  pass.
- [ ] Run change validation:
  `npx -y @fission-ai/openspec@1.5.0 validate v0005-executor-k8s --strict --no-interactive`.
- [ ] Confirm the change remains active and unarchived until
  implementation, verification, and acceptance are complete.