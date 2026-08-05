# Tasks

Implementation SHALL proceed in the order below. A GREEN task may start
only after its corresponding RED task has failed for the expected
behavior-specific reason. Record the exact RED and GREEN
command/result before checking an implementation task. The K8s Executor
SHALL NOT be implemented before every `v0005.<n>` definition has a
runnable Playwright test in `autotest/executor_k8s_openhands/tests/` whose title
contains the immutable `v0005.<n>` id.

## 1. Implementation-start test activation and harness

- [ ] Move every
      `openspec/changes/v0005-executor-k8s/specs/test-cases/v0005.<n>-*.md`
      unchanged into `autotest/test-cases/` as the first implementation
      mutation; verify ordinals remain contiguous from `v0005.1` through
      `v0005.11`, destinations do not collide, and no v0005 definition
      remains under the change folder.
- [ ] Bootstrap `autotest/executor_k8s_openhands/` as a Playwright-driven
      suite that starts the real State Registry and PostgreSQL processes,
      creates one fresh real local `kind` cluster per suite run under a
      collision-resistant name, loads test runtime images, provides
      an OpenHands-compatible API mock for deterministic scenarios,
      supplies authenticated Executor service identities for two distinct
      teams (`team-A` and `team-B`), supports controlled K8s Executor
      process restarts, and drives only supported HTTP/read interfaces;
      verify the harness can report a behavior-specific connection or
      unimplemented-operation failure rather than a fixture or dependency
      failure.
- [ ] Deploy `executor_k8s_openhands` as a single-replica Deployment inside
      the temporary cluster using a dedicated ServiceAccount, least-privilege
      namespace RBAC, a non-overlapping Pod replacement strategy, and a PVC
      mounted at `<cache_dir>`. Verify its readiness and public probes from the
      harness. Ensure task Pods have no owner reference to the ephemeral
      Executor Pod and remain running when that Pod is deleted.
- [ ] Vendor or generate from a repository-pinned version of the local-path
      provisioner manifest; pin every image by immutable digest or reviewed
      version and do not use `latest`. Install it into the temporary cluster,
      wait for controller readiness, create an explicitly named test
      StorageClass, and dynamically bind the Executor cache `ReadWriteOnce`
      PVC before starting the Deployment. Fail setup with collected
      provisioner/PVC/PV diagnostics if binding does not complete within its
      bounded timeout.
- [ ] Add suite-level `kind` lifecycle handling that refuses to reuse or
      delete a cluster it did not create, records the created cluster name,
      and always runs teardown after success, test failure, or setup failure.
      Before deletion, collect `kubectl get/describe`, namespace events, Pod
      logs (including previous-container logs), and relevant manifests into
      Playwright artifacts. Verify with an intentionally failing smoke case
      that diagnostics survive while the temporary cluster is deleted.
- [ ] Add a shared real-runtime smoke fixture used by both K8s and Docker
      Executor E2E. Build the repository's real
      `autotest/agent-openhands-image`, load the exact same image into `kind`
      and Docker Engine, submit one short task that writes a unique workspace
      marker, require actual OpenHands `execution_status = finished`, and
      verify the marker. Start one local deterministic OpenAI-compatible mock
      LLM for both runtime paths, configure the real agent-server to use its
      base URL and non-secret placeholder token, and script the minimal
      marker-writing tool-call sequence plus terminal response. Make the test
      fail if the agent-server contacts any external LLM endpoint or requires
      a real API key; redact prompt and header values in logs and reports.
- [ ] Add one runnable Playwright test for every moved v0005 definition
      and make each test title contain its immutable `v0005.<ordinal>` id;
      leave every `## Implementation reference` blank until the
      corresponding test has a stable file path and test title.

## 2. Team-bound registration and identity immutability

- [ ] **RED E2E:** implement the runnable tests for `v0005.1`
      (first-start `POST /v1/executors` returns `201` with a
      State Registry-generated UUID that is cached before task intake;
      restart refresh uses PUT and returns `200`; registration carries `scope = team`, one immutable
      `team_id`, one `authorized_tag`, observed `max_capacity`, observed
      `running_count`, metadata, bound to the
      authenticated Executor identity derived from mTLS, with no `identity`
      field in the body) and `v0005.2` (registration with a
      `team_id` that does not match the identity-bound team is rejected
      without persistence); run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.(1|2)\b'`
      and verify failure because team-bound registration, identity match,
      and immutability rules are absent.
- [ ] **RED unit/integration:** add table-driven tests for zero, one,
      and multiple `team_id` submissions, identity-mismatched `team_id`,
      forbidden client-supplied `identity` and `team_name` fields, and
      re-registration with a different `team_id`; verify they fail
      before team validation and registration code exists.
- [ ] **GREEN:** implement authenticated K8s Executor registration that
      uses POST without `executor_id` on empty cache, atomically persists the
      server-generated response ID, uses PUT only on restart, and declares
      `scope = team`, exactly one scalar `team_id` referencing
      the existing team bound to the authenticated Executor service
      identity, exactly one scalar `authorized_tag`, observed `max_capacity`,
      observed `running_count`, and runtime metadata;
      reject a client-supplied `team_name`; derive canonical identity only
      from authenticated mTLS and reject a client-supplied `identity`; reject any registration whose
      `scope` is not `team`, reject zero or multiple `team_id`
      submissions, reject identity-mismatched `team_id`, and reject any
      re-registration whose `team_id` differs from the stored `team_id`.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright command and the
      related Go tests; require concrete first-start `201`, refresh `200`, `400 invalid_team_id_count`,
      `403`, and `404` outcomes, persistence of the supplied `team_id` on
      the canonical Executor record, and immutability of `team_id` across
      re-registration.
- [ ] **REFACTOR:** isolate identity verification and `team_id`
      immutability checks behind narrow Executor-client interfaces while
      keeping the targeted and related tests green.

## 3. Team-isolated pending FIFO discovery and claim

- [ ] **RED E2E:** implement the runnable test for `v0005.3`
      (collection-level discovery of a tag whose matching `pending` tasks
      all belong to a different `team_id` returns the NORMAL empty result —
      `204 No Content` or `200` with an empty task list and no `count`,
      `cursor`, `total`, or pagination metadata — indistinguishable from
      "no tasks match this tag anywhere"; point-resource lookups against a
      foreign-team `task_id` (claim, task detail read) return a
      non-revealing `404` indistinguishable from "resource does not exist";
      in both cases the Executor creates no Pod and appends no event); run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.3\b'` and
      verify failure because same-team exact-tag pending FIFO discovery,
      the team-filter-before-result-shaping rule, and the non-revealing
      cross-team `404` rule on point resources are absent.
- [ ] **RED unit/integration:** add discovery-query tests covering
      same-team match, no-match-anywhere, foreign-team-only match (returns
      the normal empty result with no leak), registered-tag-only
      enforcement, and `(ingested_at ASC, task_id ASC)` ordering with the
      eligibility predicate applied first; add point-resource tests for
      the foreign-team `404`; verify they fail before the team-scoped
      pending discovery and FIFO claim code exists.
- [ ] **GREEN:** implement collection-level discovery that filters by
      `team_id` BEFORE shaping results, returning only `pending` tasks
      whose `required_tag` equals the Executor's single `authorized_tag`
      AND whose `team_id` equals the Executor's bound `team_id`, ordered
      `(ingested_at ASC, task_id ASC)` with the eligibility predicate
      applied first; keep discovery read-only, SHALL NOT reserve or assign
      a task, SHALL NOT read, compare, or enforce `max_capacity` or
      `running_count`; ensure the empty-result payload contains no
      `count`, `cursor`, `total`, or pagination metadata that would
      distinguish a foreign-team match from a no-match-anywhere.
- [ ] **GREEN:** implement FIFO claim through
      `POST /v1/executors/{executor_id}/claim` carrying `task_id` and a
      stable `command_id`; on `200 claimed` consume the
      `tasks.resolved_image`, `tasks.image_source`, `tasks.claimed_at`,
      `environment_id`, and the State Registry-issued compact scope token
      from the response; treat a foreign-team `task_id` identically to an
      unknown `task_id` for claim, event write, task detail read,
      environment open, and control read, returning a non-revealing `404`
      without appending any event; treat `409 task_already_claimed` and
      `409 older_task_must_be_claimed_first` as "discard the candidate and
      return to discovery"; on the same `(task_id, command_id)` retry
      consume the original `200 claimed` body without starting an
      additional Pod or appending an additional event.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require the NORMAL empty collection result (`204 No Content` or
      `200` with an empty task list and no `count`, `cursor`, `total`, or
      pagination metadata) for foreign-team-only discovery, a
      non-revealing `404` for point-resource lookups (claim, event write,
      task detail read, environment open, control read) against a
      foreign-team identifier, `409 older_task_must_be_claimed_first` for
      an eligible non-oldest task, `409 task_already_claimed` for a
      different `command_id` on an already-claimed task, the original
      `200 claimed` body for the same `(task_id, command_id)` retry, no
      Pod, no event, no `422 executor_at_capacity` or any capacity-based
      rejection, and never a `dispatched` event.
- [ ] **REFACTOR:** consolidate team-scoped query construction and
      FIFO claim guards behind narrow interfaces while keeping all
      discovery/claim tests green.

## 3a. System-owned cross-team FIFO discovery and claim (`v0005.10`)

- [ ] **RED E2E:** implement the runnable test for `v0005.10` covering
      system-scope registration with the `team_id` property absent, rejection
      of explicit null or a value, metadata-only cross-team FIFO
      discovery, oldest-eligible claim, task-team Pod/event envelopes, and
      unchanged canonical task ownership; run the targeted Playwright test and
      verify behavior-specific failure before production edits.
- [ ] **RED unit/integration:** add scope-table tests for team/system
      registration (including absent/null/value `team_id` representations),
      immutable scope, system discovery, and assigned-task team envelope
      derivation.
- [ ] **GREEN:** implement the minimum scope-aware K8s registration,
      discovery, claim, Pod labels, event envelopes, environment access, control
      reads, and restart reconciliation required by `v0005.10`.
- [ ] **GREEN VERIFY:** rerun the exact targeted Playwright command and the
      related Go tests; require all to pass.
- [ ] **REFACTOR:** centralize scope predicates and task-team envelope
      derivation while keeping the targeted and related suites green; fill the
      moved `v0005.10` definition with its exact implementation reference and
      RED/GREEN evidence.

## 4. Pod lifecycle with team_id envelope and team-scoped event writes

- [ ] **RED E2E:** implement the runnable tests for `v0005.4` (same-team
      FIFO claim returns `200 claimed` with `tasks.resolved_image`,
      `tasks.image_source`, `tasks.claimed_at`, `tasks.owner_command_id`,
      `environment_id`, and the State Registry-issued scope token; the K8s
      Executor uses `tasks.resolved_image` verbatim, emits a `running`
      event after `200 claimed` with the full envelope — `task_id`,
      `executor_id`, `team_id`, `event_id`, `event_type = running`,
      `occurred_at`, `payload` — creates the Kubernetes Pod carrying the
      canonical `task_id`, `command_id`, `team_id`, `executor_id`,
      `executor_scope`, and `resolved_image_source` labels and the runtime
      label `flowai.runtime=k8s`, treats the OpenHands terminal conversation
      status rather than Pod exit code as authoritative, emits exactly one of
      `finished` or `failed` with the same envelope shape, retains the Pod for
      configured `finished_cleanup_delay` or `failed_cleanup_delay`, selected
      by the accepted terminal event type, while keeping it in local capacity,
      and then deletes it
      idempotently; the task's
      canonical history is exactly `[created, running, finished]` with the
      Registry-appended `created` carrying non-null `executor_id` and the
      payload meaning `task <task_id> loaded by <executor_id>`) and
      `v0005.5` (a cross-team event post returns the same non-revealing
      `404` shape used for an unknown identifier and appends no event; a
      same-team Executor that is not the recorded `tasks.executor_id`
      returns `403 not_assigned`; an authenticated Executor whose envelope
      `team_id` differs from its immutable service binding returns `403
team_mismatch`); run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.(4|5)\b'`
      and verify failure because the FIFO claim response shape, the
      `resolved_image` flow, the Registry-appended first `created` event,
      the team_id envelope, `running`-after-claim ordering, and the
      event-denial taxonomy are absent.
- [ ] **RED unit/integration:** add transition, cleanup-delay, and envelope tests
      covering the seven-field envelope for `running`/`finished`/`failed`,
      OpenHands `finished` -> FlowAI `finished`, OpenHands
      `failed`/`error`/`stuck`/`paused` -> FlowAI `failed`, confirmation that
      every `failed` payload has a non-empty machine-readable
      `failure_reason`, agent-container restart before OpenHands `finished` ->
      exactly one `failed` with `failure_reason =
      "pod_restarted_before_finish"` and no resumed or recreated execution,
      agent-server Pod exit is not required, zero and non-zero
      `finished_cleanup_delay`, `failed_cleanup_delay`, selection by accepted
      terminal event type, capacity retention until cleanup, idempotent
      deletion, and no duplicate terminal event on deletion failure,
      missing `team_id` rejection, envelope `team_id` mismatch (`403
team_mismatch`), unassigned same-team writer (`403 not_assigned`),
      and foreign point probe (`404`); verify behavior-specific failures
      before the envelope validation and event-denial taxonomy exist.
- [ ] **GREEN:** implement seven-field event envelope validation on
      every Executor-emitted task event (`running`/`finished`/`failed`)
      carrying `task_id`, `executor_id`, `team_id`, `event_id`,
      `event_type`, `occurred_at`, `payload`; emit `running` AFTER `200
claimed` and BEFORE creating the Kubernetes Pod, derive exactly one of
      `finished` or `failed` from the agent tool's explicit terminal signal,
      select and start `finished_cleanup_delay` or `failed_cleanup_delay` only
      after the corresponding event receives `202 accepted`, retain the Pod
      and capacity slot during the selected delay, then delete idempotently; enforce
      the event-denial
      taxonomy — `403 team_mismatch` for envelope `team_id` mismatch, `403
not_assigned` for unassigned same-team writers, `404` for foreign
      task or Executor point identifiers, no `403` for foreign-team probes
      — and never create a Pod for a cross-team identifier; never append a
      `created` event from the Executor and never append a `dispatched`
      event at any layer.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require `200 claimed` for the FIFO claim, a `202 accepted` for every
      same-team event, a single terminal event per task, no Pod created
      before the accepted `running` event, no Pod created for cross-team
      identifiers, the Registry-appended `created` event present with
      non-null `executor_id` and the documented payload, no `dispatched`
      event at any time, `403 team_mismatch` for envelope `team_id`
      mismatches, `403 not_assigned` for unassigned same-team writers, the
      same non-revealing `404` for foreign point probes and unknown
      identifiers, and no event appended on any rejection.
- [ ] **RED Docker regression:** add Docker OpenHands tests proving the same
      `finished_cleanup_delay` and `failed_cleanup_delay` configurations (each
      default `0s`) are selected by accepted terminal event type and start only
      after terminal-event acceptance, retain the container in local capacity
      during a non-zero selected delay, and perform idempotent stop/removal
      without a duplicate terminal task event; also prove that container exit
      with code `0` or non-zero before OpenHands `finished` emits exactly one
      `failed` with `failure_reason = "container_exited_before_finish"`, never
      succeeds, and never resumes or recreates the execution.
- [ ] **GREEN Docker regression:** add the cleanup-delay setting to
      `executor_docker_opehands`, apply it between accepted terminal
      conversation event and container cleanup, and keep shutdown/drain/error
      cleanup bounded and idempotent; map every pre-`finished` task-container
      exit to `failed` with `failure_reason =
"container_exited_before_finish"` regardless of exit code.
- [ ] **REFACTOR:** consolidate envelope validation and team-scoped
      event filtering behind narrow Executor-client interfaces without
      separating envelope validation from event forwarding; rerun all
      targeted tests.

## 5. Local capacity ownership (no Registry-side rejection)

- [ ] **RED E2E:** implement the runnable test for `v0005.6`
      (`max_capacity = running_count` does not cause the Registry to reject
      claim; the Executor alone decides when to start a Pod); run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.6\b'` and
      verify failure because the saturated-capacity rule,
      capacity-independent FIFO claim, and local-only slot enforcement are
      absent.
- [ ] **RED unit/integration:** add capacity-observation tests covering
      saturated `running_count = max_capacity`, sparse `running_count <
max_capacity`, and self-event observation writes; verify
      behavior-specific failures before capacity handling code exists.
- [ ] **GREEN:** keep the existing v0002 capacity-observation contract:
      observe Pods locally, request claim only when a local slot is
      available, start no Pod before `200 claimed`, and start none after
      `404` or `409`; report `max_capacity` and `running_count` through
      `POST /v1/executors/{executor_id}/events` with the seven-field
      envelope including `team_id`.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require no `422 executor_at_capacity` and no capacity-based rejection
      from the Registry, no Pod created beyond local capacity, and
      self-events carrying `team_id`.
- [ ] **REFACTOR:** isolate the local capacity decision from the
      team-scoping rules so both stay independently testable; rerun all
      targeted tests.

## 6. Assigned task environment open with team-bound scope token (inherits finalized v0002 token contract)

- [ ] **RED E2E:** implement the runnable test for `v0005.7` (the
      assigned K8s Executor in the bound `team_id` opens the environment
      via `GET /v1/environments/{environment_id}/open?task_id={task_id}`
      with the compact three-part signed scope token
      `<header>.<payload>.<signature>` carried only in the
      `X-FlowAI-Scope-Token` request header; protected header carries
      `alg` (allow-listed `HS256`/`HS384`/`HS512`), `kid`, and `typ`;
      payload carries `team_id`, `project_id` (required claim; nullable
      only when the canonical environment has no project scope),
      `task_id`, `environment_id`, `executor_id`, `audience` (literal
      `state-registry.environment.open`), `issued_at`, `expiry`
      (`expiry > issued_at` and `expiry - issued_at <= 5 minutes`), and
      `key_id` selecting a State Registry-controlled active key; the
      protected-header `kid` SHALL equal the payload `key_id`;
      `issued_at <= server_now + 30 seconds`; and every failure
      variation — missing/empty token, tampered MAC, algorithm outside
      the allow-listed HMAC set, `kid` not equal to `key_id`, `key_id`
      outside the documented active window (including a retired
      `key_id`), `expiry` not strictly later than `issued_at`,
      `expiry - issued_at > 5 minutes`, `issued_at` in the future beyond
      `server_now + 30 seconds`, audience mismatch, missing required claim
      (including a missing `project_id`), null `project_id` for a
      project-scoped environment, mutated `project_id`/`task_id`/
      `environment_id`/`executor_id`/`team_id`, terminal-state task,
      foreign-team or unassigned Executor — returns the same
      non-revealing `404 environment_unknown_or_unavailable` shape with
      no values and zero provider decrypt operations); run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.7\b'` and
      verify failure because the inherited v0002 token-contract
      validation, the `kid == key_id` check before any MAC computation,
      the `issued_at`/`expiry` window, the project-scope rule for
      `project_id`, plaintext-free audit, and the uniform non-revealing
      `404` are absent.
- [ ] **RED unit/integration:** add token-validation tests covering the
      full binding matrix inherited from the v0002 finalized contract —
      protected-header `alg` allow-list, protected-header `kid` equals
      payload `key_id`, `team_id`, `project_id` (present and non-null when
      the canonical environment has a project scope), `task_id`,
      `environment_id`, `executor_id`, `audience` (literal
      `state-registry.environment.open`), `issued_at <= server_now + 30
seconds`, `expiry > issued_at`, `expiry - issued_at <= 5 minutes`,
      `key_id` against the documented active key window — plus MAC
      constant-time comparison, retired-key rejection, foreign-team
      requests, terminal task-state rejection, unassigned-Executor
      rejection, and uniform `404` parity across every invalid variation;
      verify behavior-specific failures before the inherited
      token-validation code exists.
- [ ] **GREEN:** inherit the finalized v0002 token contract verbatim:
      serve `GET /v1/environments/{environment_id}/open?task_id={task_id}`;
      require the compact three-part signed scope token in the
      `X-FlowAI-Scope-Token` request header (no token fields in the body
      or other headers); verify that the protected-header `kid` equals the
      payload `key_id` BEFORE any MAC computation; accept only algorithms
      in the allow-listed HMAC set `HS256`/`HS384`/`HS512`; recompute the
      signature under the declared allow-listed algorithm using the
      server-side key handle and compare it under constant-time
      comparison; verify `key_id` against the documented active key window
      (retired keys rejected); verify `issued_at <= server_now + 30
seconds`, `expiry > issued_at`, and `expiry - issued_at <= 5
minutes`; verify the literal `audience` is
      `state-registry.environment.open`; verify the canonical claim shape
      (including that `project_id` is present and non-null when the
      canonical environment has a project scope, and may be null only when
      the canonical environment has no project scope); verify every claim
      (`team_id`, `project_id`, `task_id`, `environment_id`, `executor_id`,
      `audience`, `issued_at`, `expiry`, `key_id`) against canonical
      records; verify the authenticated Executor mTLS identity is the
      assigned same-team Executor; verify the task is in a non-terminal
      state; verify the project/task applicability; perform every one of
      those checks BEFORE any decrypt operation by the active provider or
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
      require a `200` with env-style values only for the fully-valid
      token from the assigned same-team Executor over its authenticated
      mTLS identity, the same non-revealing `404
environment_unknown_or_unavailable` shape for every invalid /
      tampered / audience-mismatched / missing-claim (including missing
      `project_id`) / null `project_id` for a project-scoped environment /
      retired-key / expired / `expiry <= issued_at` / TTL > 5 minute /
      premature-beyond-30s / foreign-team / unassigned / `kid` not equal
      to `key_id` / terminal-state / applicability-violating variation,
      zero provider decrypt operations triggered for any failing
      variation, an audit entry without plaintext only from the valid
      request, and no plaintext in logs or durable Executor storage.
- [ ] **REFACTOR:** isolate the inherited v0002 token-validation,
      `key_id` rotation window, `kid == key_id` guard, MAC constant-time
      comparison, and plaintext-handling rules behind narrow
      Executor-client interfaces without leaking token plaintext, claims,
      or key material into logs; keep the targeted and related tests
      green.

## 7. Pending controls only for assigned tasks in the bound team

- [ ] **RED E2E:** implement the runnable test for `v0005.8` (the
      assigned same-team Executor reads and applies pending controls for
      its assigned task; a cross-team read returns a non-revealing `404`;
      a same-team unassigned read returns `403 not_assigned`); run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.8\b'` and
      verify failure because assigned-task-only control reads and the
      cross-team `404` are absent.
- [ ] **RED unit/integration:** add control-read tests covering the
      assigned+same-team happy path, the cross-team denial, the
      same-team `403 not_assigned` denial, and the audit entry shape; verify
      behavior-specific failures before the control-read code exists.
- [ ] **GREEN:** poll the State Registry for pending controls only
      for tasks whose `task_id` is assigned to the Executor AND whose
      `team_id` equals the Executor's bound `team_id`; apply received
      controls to that task's Pod and record the resulting task events
      with the seven-field envelope (including `team_id`); never read,
      list, or apply controls for tasks in another team or assigned to
      another Executor.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require `200` with the pending control for the assigned same-team
      Executor, a non-revealing `404` for cross-team requests, `403
not_assigned` for same-team unassigned requests, applied controls
      visible in task events, and no control
      applied across teams.
- [ ] **REFACTOR:** consolidate control-read scoping and audit hooks
      behind narrow Executor-client interfaces while keeping all targeted
      tests green.

## 8. Restart reconciliation without reassignment

- [ ] **RED E2E:** implement the runnable test for `v0005.9` (after a
      Deployment-managed Executor Pod replacement, the K8s Executor
      re-registers with the same bound
      `team_id` and `authorized_tag`, reuses its State Registry-generated
      cached `executor_id`, loads claimed non-terminal tasks exclusively from
      persistent cache, and matches each cached `owner_command_id` to the
      existing Pod's `flowai.command_id`
      label to identify which in-flight Pod to continue observing,
      re-attaches to existing Pods in Kubernetes without creating new
      Pods, loads a persistent recovery cache, reconnects to the same
      OpenHands conversation at its current progress, retries pending outbox
      events with their original `event_id`, emits no duplicate `running`
      event, and emits exactly one
      terminal `finished` or `failed` event per Pod); run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.9\b'` and
      verify failure because non-reassigning reconciliation, `team_id`
      immutability across restart, persistent-cache-only recovery,
      the `owner_command_id` -> `flowai.command_id` Pod-matching rule, and
      the no-duplicate-`running`-event rule are absent.
- [ ] **RED unit/integration:** add reconciliation tests covering
      same-team restart, divergent `team_id` rejection, no duplicate
      `running` event, server-generated cached `executor_id` reuse, the
      `owner_command_id` -> `flowai.command_id` label match, and exactly
      one terminal event per Pod; cover persistent-cache reload, event
      write-before-send, mark-accepted-after-`202`, same-event-ID retry, and
      OpenHands conversation reconnection; verify behavior-specific failures before
      reconciliation code exists.
- [ ] **RED in-cluster restart:** delete the running Executor Deployment Pod
      while task Pods are active, require the dynamically provisioned PVC/PV
      and task Pods to survive,
      and require the replacement Pod to reuse the cached `executor_id`, claim
      intents, assignments, and outbox without duplicate claims, Pods, or
      events. Verify behavior-specific failure before reconciliation exists.
- [ ] **RED bbolt cache:** for both concrete Executors, add independent
      per-service tests that require creation of owner-only
      `<cache_dir>/executor.db` with versioned `metadata`, `assignments`, and
      `event_outbox` buckets and rejection of an unknown newer schema. Require
      a durable claim intent before the HTTP claim, retry of an uncertain
      claim with the identical `(task_id, command_id)`, assignment persistence
      after `200 claimed` but before `running` or runtime creation, event
      persistence before send, and transition to `accepted` only after `202`.
      Require every cache mutation to commit in a bbolt write transaction
      before its external side effect. Inspect buckets and logs and require
      that no environment or secret plaintext is present. Cover open failure,
      corruption, and schema mismatch without silently resetting or replacing
      the database. Observe behavior-specific failures before cache
      implementations exist.
- [ ] **RED pre-generated mTLS:** mount valid, expired, missing, and malformed
      client certificate, private key, and CA fixtures for K8s and Docker.
      Require valid material to connect and every invalid case to fail before
      registration without generating or overwriting certificate files.
      Replace the mounted files while the process runs and require the old
      in-memory material to remain active; restart and require the replacement
      material to be loaded.
- [ ] **RED failure recovery:** cover missing, unreadable, corrupt, and
      identity-mismatched cache; require unhealthy status, zero new claims,
      and no mutation or deletion of existing Pods.
- [ ] **GREEN:** implement a restart reconciliation loop that
      re-registers with the same bound `team_id` (the State Registry
      rejects a divergent `team_id`), loads claimed non-terminal tasks only
      from persistent cache, reuses the State Registry-generated cached
      `executor_id`, and uses
      each cached `owner_command_id` to match the existing
      Pod's `flowai.command_id` label so the Executor continues observing
      the right in-flight Pod, persists recovery cache and event outbox on a
      volume, re-attaches to existing Pods in Kubernetes, reconnects to the
      cached OpenHands conversation without restarting work, retries pending
      events with their original `event_id`,
      emits no duplicate `running` event, and emits exactly one terminal
      `finished` or `failed` event per Pod whose lifecycle ends.
- [ ] **GREEN in-cluster deployment:** add E2E manifests/fixtures for the
      single-replica Deployment, ServiceAccount, namespace-scoped RBAC, PVC,
      explicit local-path StorageClass, probes, and non-overlapping replacement
      strategy. Keep task Pods
      independent of the Executor Pod so explicit reconciliation and delayed
      cleanup, rather than Kubernetes garbage collection, own their lifecycle.
- [ ] **GREEN bbolt cache:** implement separate cache packages inside
      `executor_k8s_openhands` and `executor_docker_openhands` using bbolt; do
      not introduce shared service code. Store the cache at
      `<cache_dir>/executor.db`, create versioned `metadata`, `assignments`,
      and `event_outbox` buckets, and commit every mutation before its external
      side effect. Fail closed on open failure, corruption, or an unsupported
      schema; never silently reset or replace an existing database.
- [ ] **GREEN pre-generated mTLS:** load only read-only, pre-created client
      certificate, private key, and CA material from a Kubernetes Secret
      volume or Docker file/secret mounts. Add no certificate issuance,
      enrollment, generation, or rotation implementation. Load the files once
      at process startup and add no file watcher, polling, or hot reload. Add
      no fixed certificate-lifetime or renewal-schedule configuration.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require no reassignment, persistent-cache-only recovery with no
      assignments-list call, each Pod label
      `flowai.command_id` to equal the corresponding task's
      `tasks.owner_command_id`, no duplicate `running` event, exactly one
      terminal event per Pod, and `team_id` immutability across the
      restart. Inspect `<cache_dir>/executor.db` and require the expected
      schema version, cached Registry-generated identity, claim intents,
      assignments, and outbox delivery states in bbolt, with committed
      write-before-side-effect durability and no environment or secret
      plaintext.
- [ ] **Docker persistent-cache regression:** require a host-backed cache
      volume, obtain and atomically store the State Registry-generated UUID
      `executor_id` from first-start POST,
      persist runtime/event-outbox state there, restart the Docker Executor,
      and verify the same ID and cache are reused rather than the container
      writable layer or a newly generated identity.
- [ ] **RED cache-lock tests:** start two K8s Executor processes against the
      same PVC mount and two Docker Executor processes against the same
      host-backed cache volume; require the first to hold
      `<cache_dir>/executor.lock` and the second to become unhealthy with zero
      registration, discovery, claim, event, Pod, or container mutations.
- [ ] **GREEN cache lock:** acquire the non-blocking exclusive OS file lock
      before POST/PUT registration, retain its file descriptor for process
      lifetime, require POSIX advisory-lock-capable storage, and rely on kernel
      release after normal exit or crash. Add no Registry lease/fencing code.
- [ ] **REFACTOR:** extract the reconciliation loop into a dedicated
      Executor-owned component without coupling it to discovery, claim, or
      Pod creation paths; rerun all targeted tests.

## 9. Correct Docker OpenHands concrete service name

- [ ] **BASELINE:** before the rename, run `go build ./...`,
      `go test ./...`, `go test -race ./...`, and the existing
      `autotest/executor_docker_opehands` Playwright suite; record the
      results so the rename remains separable from behavior changes.
- [ ] **RED E2E/contract:** implement the runnable test for `v0005.11` and
      add or update regression assertions that require
      directory, command, binary, config YAML, Go import path, wire
      `executor_type`, slog `service` field, probe service label, package
      name, and documentation identifier `executor_docker_openhands`, with
      Go constant `ExecutorTypeDockerOpenHands`; verify they fail against
      the existing misspelled `executor_docker_opehands` identifier.
- [ ] **GREEN filesystem:** rename top-level
      `executor_docker_opehands/` to `executor_docker_openhands/`, rename
      its `cmd/` directory and config YAML to the same concrete identifier,
      and rename `autotest/executor_docker_opehands/` to
      `autotest/executor_docker_openhands/`; preserve file history as moves
      and do not edit archived OpenSpec change artifacts.
- [ ] **GREEN contract:** replace the misspelled current-state identifier
      with `executor_docker_openhands` across Go imports, binary/build
      invocations, wire values, State Registry fixtures and tests,
      Playwright configuration, manual QA scripts, current architecture
      diagrams/docs, README files, and active OpenSpec artifacts. Keep the
      correctly spelled Go constant `ExecutorTypeDockerOpenHands`, change
      its wire value to `executor_docker_openhands`, and provide no alias or
      compatibility acceptance for `executor_docker_opehands`.
- [ ] **GREEN VERIFY:** prove no non-archived source or current-state file
      contains `executor_docker_opehands`; run `gofmt` on changed Go files,
      `go build ./...`, `go vet ./...`, `go test ./...`, and
      `go test -race ./...`; run the renamed Docker Playwright suite and
      relevant State Registry contract tests; require all to pass with the
      new filesystem and wire identifier.
- [ ] **REFACTOR REVIEW:** inspect string-based and reflection-adjacent
      references (YAML keys/values, JSON fixtures, shell scripts, package
      names, Docker labels, executable paths, and test snapshots), confirm
      archived OpenSpec artifacts retain the historical spelling, and keep
      the rename commit purely structural except for the explicitly approved
      breaking wire-value correction.

## 10. Architecture diagrams

- [ ] Prepare both proposed v0005 sequence diagrams already present:
      `01-k8s-executor-topology.puml` (`sequence`) and
      `02-k8s-task-lifecycle.puml` (`sequence`); both updated to reflect
      the FIFO claim contract, the Registry-appended first `created`
      event, the `resolved_image` flow, the seven-field event envelope,
      the team-bound scope-token environment open, the assigned-only
      control reads, and the `tasks.executor_id`-only re-read with
      `tasks.owner_command_id` -> Pod-label `flowai.command_id` matching
      restart reconciliation.
- [ ] Verify both diagrams still match the implemented
      registration/envelope/isolation/capacity/env/control/restart contract
      without adding a state-machine, ER, or other unrequested diagram
      family.
- [ ] Run `~/config/ai/skills/puml-diagrams/puml-verify
openspec/changes/v0005-executor-k8s/specs/diagrams/*.puml --checkonly`
      and require both sources to compile without errors.

## 11. Final verification and OpenSpec gates

- [ ] Fill every moved v0005 definition's
      `## Implementation reference` with its exact Playwright file path
      and test title; verify no test is skipped and every task records
      valid RED-before-GREEN evidence.
- [ ] Run `gofmt` on changed Go files, `go test -race ./...`,
      `go test -tags=integration ./executor_k8s_openhands/...`, and `go build ./...`;
      require all unit/integration/race/build checks to pass.
- [ ] Run the full K8s Executor Playwright suite with
      `npm --prefix autotest/executor_k8s_openhands test`; require every v0005 E2E
      to pass against `kind`, using the agent API mock for deterministic
      contract cases. Run the separate real-runtime smoke against both the
      `kind` Pod and Docker container with the same real image, task, and
      local deterministic OpenAI-compatible mock LLM; require zero external
      LLM calls and no real API-key secret. Require the suite-created kind
      cluster to be absent after both a passing run and an intentionally
      failing harness self-test, with diagnostics retained for the failure.
- [ ] Run change validation:
      `npx -y @fission-ai/openspec@1.5.0 validate v0005-executor-k8s
--strict --no-interactive`.
- [ ] Confirm the change remains active and unarchived until
      implementation, verification, and acceptance are complete.
