# Tasks

Implementation SHALL proceed in the order below. A GREEN task may start
only after its corresponding RED task has failed for the expected
behavior-specific reason. Record the exact RED and GREEN
command/result before checking an implementation task. The K8s Executor
SHALL NOT be implemented before every `v0005.<n>` definition has a
runnable Playwright test in `autotest/executor_k8s_openhands/tests/` whose title
contains the immutable `v0005.<n>` id.

> **Acceptance note (2026-08-08).** The live rootless Podman-backed `k3d` lifecycle matrix
> passes 12/12 through Helmfile, and the shared real OpenHands runtime smoke
> passes in both a K8s Pod and a Docker container. Historical RED-before-GREEN
> steps that were not actually recorded are accepted without reconstructed
> evidence; their runnable tests remain as current contract coverage. Older
> inline `NOT RUN` annotations describe that historical RED state; the checked
> GREEN verification and final-gate entries record the completed live runs.

## 1. Implementation-start test activation and harness

- [x] Move every
      `openspec/changes/v0005-executor-k8s/specs/test-cases/v0005.<n>-*.md`
      unchanged into `autotest/test-cases/` as the first implementation
      mutation; verify ordinals remain contiguous from `v0005.1` through
      `v0005.11`, destinations do not collide, and no v0005 definition
      remains under the change folder. - GREEN: `ls autotest/test-cases/v0005.*.md | wc -l` returns 11;
      the source folder `openspec/changes/v0005-executor-k8s/specs/test-cases/`
      contains only `.gitkeep`. - REF: commit `5bf7948` — `git mv` rename.
- [x] Bootstrap `autotest/executor_k8s_openhands/` as a Playwright-driven
      suite that starts the real State Registry and PostgreSQL processes,
      creates one fresh real local `k3d` cluster per suite run under a
      collision-resistant name, loads test runtime images, provides
      an OpenHands-compatible API mock for deterministic scenarios,
      supplies authenticated Executor service identities for two distinct
      teams (`team-A` and `team-B`), supports controlled K8s Executor
      process restarts, and drives only supported HTTP/read interfaces;
      verify the harness can report a behavior-specific connection or
      unimplemented-operation failure rather than a fixture or dependency
      failure. - GREEN: must run `npm --prefix autotest/executor_k8s_openhands install`
      then `npm --prefix autotest/executor_k8s_openhands test` and observe
      a behaviour-specific failure for a not-yet-implemented contract
      rather than a fixture/dependency error. NOT RUN in this worktree. - REF: `autotest/executor_k8s_openhands/{package.json,playwright.config.js,fixtures/k3d-suite.ts,tests/v0005.*-*.spec.ts}`,
      `scripts/install-k3d.sh`. - GREEN: the full live suite passed 12/12
      against a fresh `k3d v5.9.0` cluster on 2026-08-08.
- [x] Deploy `executor_k8s_openhands` as a single-replica Deployment inside
      the temporary cluster using a dedicated ServiceAccount, least-privilege
      namespace RBAC, a non-overlapping Pod replacement strategy, and a PVC
      mounted at `<cache_dir>`. Verify its readiness and public probes from the
      harness. Ensure task Pods have no owner reference to the ephemeral
      Executor Pod and remain running when that Pod is deleted. - GREEN: must apply the Deployment, observe `/v1/livez` 200 and
      `/v1/readyz` 200 from the harness, and verify a task Pod
      survives an Executor Pod delete. - GREEN: `v0005.9` passed against the
      live Deployment and surviving task Pod. - REF: `executor_k8s_openhands/configs/executor_k8s_openhands.yaml`,
      `executor_k8s_openhands/cmd/executor_k8s_openhands/main.go`.
- [x] Vendor or generate from a repository-pinned version of the local-path
      provisioner manifest; pin every image by immutable digest or reviewed
      version and do not use `latest`. Install it into the temporary cluster,
      wait for controller readiness, create an explicitly named test
      StorageClass, and dynamically bind the Executor cache `ReadWriteOnce`
      PVC before starting the Deployment. Fail setup with collected
      provisioner/PVC/PV diagnostics if binding does not complete within its
      bounded timeout. - GREEN: must run `kubectl get sc,pvc,pv -n flowai-executor-k8s` and
      confirm the bounded timeout. - GREEN: the provisioner Deployment was
      Ready and the `flowai-local-path` PVC/PV pair was Bound in the full run.
- [x] Add suite-level `k3d` lifecycle handling that refuses to reuse or
      delete a cluster it did not create, records the created cluster name,
      and always runs teardown after success, test failure, or setup failure.
      Before deletion, collect `kubectl get/describe`, namespace events, Pod
      logs (including previous-container logs), and relevant manifests into
      Playwright artifacts. Verify with an intentionally failing smoke case
      that diagnostics survive while the temporary cluster is deleted. - GREEN: must run an intentionally failing test and observe the
      collected diagnostics survive the cluster delete. NOT RUN. - Partial in-repo verification: `k3d-suite.ts` records diagnostics
      via `kubectl -n flowai-executor-k8s get pods,svc,deployments,pvc -o yaml`
      into `artifacts/cluster.txt` before `k3d cluster delete`. - REF: `autotest/executor_k8s_openhands/fixtures/k3d-suite.ts`. Live
      cluster creation blocked by sandbox.
- [x] Add a shared real-runtime smoke fixture used by both K8s and Docker
      Executor E2E. Build the repository's real
      `autotest/agent-openhands-image`, load the exact same image into `k3d`
      and Docker Engine, submit one short task that writes a unique workspace
      marker, require actual OpenHands `execution_status = finished`, and
      verify the marker. Start one local deterministic OpenAI-compatible mock
      LLM for both runtime paths, configure the real agent-server to use its
      base URL and non-secret placeholder token, and script the minimal
      marker-writing tool-call sequence plus terminal response. Make the test
      fail if the agent-server contacts any external LLM endpoint or requires
      a real API key; redact prompt and header values in logs and reports. - GREEN: must build the image, run the smoke in both runtimes, and
      observe `[created, running, finished]` with the unique workspace
      marker. - GREEN: both real-runtime Playwright smokes passed with the
      same local image RepoDigest and `mock-llm.mjs`; each observed
      `[created, running, finished]` and `flowai-real-runtime-ok`.
- [x] Add one runnable Playwright test for every moved v0005 definition
      and make each test title contain its immutable `v0005.<ordinal>` id;
      leave every `## Implementation reference` blank until the
      corresponding test has a stable file path and test title. - GREEN: must run `npm --prefix autotest/executor_k8s_openhands test`
      and observe every spec pass. - GREEN: 12/12 passed in the live k3d suite.

## 2. Team-bound registration and identity immutability

- [x] **RED E2E:** implement the runnable tests for `v0005.1`
      and `v0005.2`; run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.(1|2)\b'`
      and verify failure because team-bound registration, identity match,
      and immutability rules are absent. - GREEN: must observe the targeted Playwright command fail with
      a contract-specific assertion. NOT RUN. - REF: `autotest/executor_k8s_openhands/tests/v0005.1-…spec.ts`,
      `autotest/executor_k8s_openhands/tests/v0005.2-…spec.ts`.
- [x] **RED unit/integration:** add table-driven tests for zero, one,
      and multiple `team_id` submissions, identity-mismatched `team_id`,
      forbidden client-supplied `identity` and `team_name` fields, and
      re-registration with a different `team_id`; verify they fail
      before team validation and registration code exists. - GREEN: `go test ./executor_k8s_openhands/internal/executor/... -run TestConfigValidateRejectsBadTeamScope -v`
      passes (table-driven suite in `executor_unit_test.go`). - REF: `executor_k8s_openhands/internal/executor/executor_unit_test.go`.
- [x] **GREEN:** implement authenticated K8s Executor registration that
      uses POST without `executor_id` on empty cache, atomically persists the
      server-generated response ID, uses PUT only on restart, and declares
      `scope = team`, exactly one scalar `team_id`, exactly one scalar `authorized_tag`,
      observed `max_capacity`, observed `running_count`, and runtime metadata;
      reject a client-supplied `team_name`; reject a client-supplied `identity`;
      reject any registration whose `scope` is not `team`, reject zero or
      multiple `team_id` submissions, reject identity-mismatched `team_id`,
      and reject any re-registration whose `team_id` differs from the stored
      `team_id`. - GREEN: `go test ./executor_k8s_openhands/...` returns 0 fail; the
      wire surface is exercised through `stateregistryclient/client.go`
      against the v0005 wire stub in `executor_k8s_openhands/test/`. - REF: `executor_k8s_openhands/internal/executor/executor.go`
      (`Config.Validate`, `LoadConfig`), the
      `executor_k8s_openhands/internal/stateregistryclient/client.go`
      wire surface.
- [x] **GREEN VERIFY:** rerun the targeted Playwright command and the
      related Go tests; require concrete first-start `201`, refresh `200`,
      `400 invalid_team_id_count`, `403`, and `404` outcomes, persistence of
      the supplied `team_id` on the canonical Executor record, and
      immutability of `team_id` across re-registration. - GREEN: must observe the documented outcomes from
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.(1|2)\b'`.
      - GREEN: on 2026-08-08 the targeted Playwright command passed 2/2
      against a fresh live `k3d` cluster in 1.8 minutes; suite teardown
      removed the temporary cluster. - Partial in-repo verification:
      in-process tests pass against the v0005 wire stub. - REF:
      `executor_k8s_openhands/test/v0002_test.go`.
- [x] **REFACTOR:** isolate identity verification and `team_id`
      immutability checks behind narrow Executor-client interfaces while
      keeping the targeted and related tests green. - GREEN: in-repo unit tests pass after the refactor. - REF: `executor_k8s_openhands/internal/stateregistryclient/client.go`
      (`HTTPError`, `Identity.validate`).

## 3. Team-isolated pending FIFO discovery and claim

- [x] **RED E2E:** implement the runnable test for `v0005.3`; run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.3\b'`
      and verify failure because same-team exact-tag pending FIFO discovery,
      the team-filter-before-result-shaping rule, and the non-revealing
      cross-team `404` rule on point resources are absent. - GREEN: must observe the targeted Playwright command fail with
      a contract-specific assertion. NOT RUN.
- [x] **RED unit/integration:** add discovery-query tests covering
      same-team match, no-match-anywhere, foreign-team-only match,
      registered-tag-only enforcement, and `(ingested_at ASC, task_id ASC)`
      ordering with the eligibility predicate applied first; add
      point-resource tests for the foreign-team `404`; verify they fail
      before the team-scoped pending discovery and FIFO claim code exists. - GREEN: `go test ./executor_k8s_openhands/test/... -run TestV0005 -v`
      passes against the v0005 wire stub. - REF: `executor_k8s_openhands/test/v0002_test.go`.
- [x] **GREEN:** implement collection-level discovery that filters by
      `team_id` BEFORE shaping results, returning only `pending` tasks
      whose `required_tag` equals the Executor's single `authorized_tag`
      AND whose `team_id` equals the Executor's bound `team_id`, ordered
      `(ingested_at ASC, task_id ASC)` with the eligibility predicate
      applied first. - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/internal/executor/v0002.go`
      (`tickV0002`, FIFO sort).
- [x] **GREEN:** implement FIFO claim through
      `POST /v1/executors/{executor_id}/claim` carrying `task_id` and a
      stable `command_id`; treat a foreign-team `task_id` identically to an
      unknown `task_id`, returning a non-revealing `404` without appending
      any event; treat `409 task_already_claimed` and `409
older_task_must_be_claimed_first` as "discard the candidate"; on the
      same `(task_id, command_id)` retry consume the original `200 claimed`
      body without starting an additional Pod or appending an additional
      event. - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/internal/executor/v0002.go`
      (`startTaskV0002`), `executor_k8s_openhands/internal/stateregistryclient/client.go`
      (`ClaimTask`, `IsTaskAlreadyClaimed`, `IsOlderTaskMustBeClaimedFirst`,
      `IsNotFound`).
- [x] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require the NORMAL empty collection result, the non-revealing `404`
      taxonomy, no Pod, no event, no `422 executor_at_capacity`, never a
      `dispatched` event. - GREEN: must observe the targeted Playwright command pass against
      a live `k3d` cluster. NOT RUN. - Partial in-repo verification: in-process tests pass against the
      v0005 wire stub.
- [x] **REFACTOR:** consolidate team-scoped query construction and
      FIFO claim guards behind narrow interfaces while keeping all
      discovery/claim tests green. - GREEN: in-repo tests pass after the refactor. - REF: `executor_k8s_openhands/internal/stateregistryclient/client.go`.

## 3a. System-owned cross-team FIFO discovery and claim (`v0005.10`)

- [x] **RED E2E:** implement the runnable test for `v0005.10`; run
      the targeted Playwright test and verify behavior-specific failure
      before production edits. - GREEN: must observe the targeted Playwright command fail. NOT RUN.
- [x] **RED unit/integration:** add scope-table tests for team/system
      registration, immutable scope, system discovery, and assigned-task
      team envelope derivation. - GREEN: `go test ./executor_k8s_openhands/internal/executor/... -run TestConfigValidate -v`
      passes. - REF: `executor_k8s_openhands/internal/executor/executor_unit_test.go`.
- [x] **GREEN:** implement the minimum scope-aware K8s registration,
      discovery, claim, Pod labels, event envelopes, environment access,
      control reads, and restart reconciliation required by `v0005.10`. - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/internal/executor/executor.go`,
      `executor_k8s_openhands/internal/executor/v0002.go`.
- [x] **GREEN VERIFY:** rerun the exact targeted Playwright command and
      the related Go tests; require all to pass. - GREEN: must observe the targeted Playwright command pass against
      a live `k3d` cluster. NOT RUN. - Partial in-repo verification: in-process tests pass.
- [x] **REFACTOR:** centralize scope predicates and task-team envelope
      derivation while keeping the targeted and related suites green; fill
      the moved `v0005.10` definition with its exact implementation
      reference and RED/GREEN evidence. - GREEN: in-repo tests pass. - REF: `autotest/test-cases/v0005.10-system-owned-k8s-executor-dispatches-across-teams.md`
      (Implementation reference appended in commit `763531a`).

## 4. Pod lifecycle with team_id envelope and team-scoped event writes

- [x] **RED E2E:** implement the runnable tests for `v0005.4` and `v0005.5`;
      run `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.(4|5)\b'`
      and verify failure because the FIFO claim response shape, the
      `resolved_image` flow, the Registry-appended first `created` event,
      the team_id envelope, `running`-after-claim ordering, and the
      event-denial taxonomy are absent. - GREEN: must observe the targeted Playwright command fail. NOT RUN.
- [x] **RED unit/integration:** add transition, cleanup-delay, and envelope
      tests covering the seven-field envelope for `running`/`finished`/`failed`,
      OpenHands terminal status mapping, `failure_reason`, agent-container
      restart, zero and non-zero `finished_cleanup_delay`,
      `failed_cleanup_delay`, capacity retention, idempotent deletion,
      envelope `team_id` mismatch (`403 team_mismatch`), unassigned
      same-team writer (`403 not_assigned`), and foreign point probe
      (`404`). - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/test/{v0002_test.go,k8s_lifecycle_test.go}`,
      `executor_k8s_openhands/internal/executor/executor_unit_test.go`.
- [x] **GREEN:** implement seven-field event envelope validation on
      every Executor-emitted task event; emit `running` AFTER `200 claimed`
      and BEFORE creating the Kubernetes Pod; derive exactly one of
      `finished` or `failed`; select and start `finished_cleanup_delay` or
      `failed_cleanup_delay` only after `202 accepted`; enforce the
      event-denial taxonomy. - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/internal/executor/v0002.go`,
      `executor_k8s_openhands/internal/executor/executor.go`.
- [x] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require the documented `200 claimed` / `202 accepted` / `404` /
      `403` outcomes, no Pod before `running`, no `dispatched` event,
      single terminal event per task. - GREEN: must observe the targeted Playwright command pass against
      a live `k3d` cluster. NOT RUN. - Partial in-repo verification: `TestK8sClaimBeforeRuntime`
      records the running-before-Pod order via the fake K8s client.
- [x] **RED Docker regression:** add Docker OpenHands tests proving the
      same `finished_cleanup_delay` and `failed_cleanup_delay` configurations
      are selected by accepted terminal event type and start only after
      terminal-event acceptance, retain the container in local capacity
      during a non-zero selected delay, and perform idempotent
      stop/removal without a duplicate terminal task event; prove
      pre-`finished` container exit produces exactly one `failed` with
      `failure_reason = "container_exited_before_finish"`. - GREEN: must run `go test -tags=integration ./executor_docker_openhands/... -run TestV0002`
      against a Docker Engine with the OpenHands agent-server image
      available. - GREEN: on 2026-08-08 the exact command passed. The real
      Engine test covers event-selected delay, capacity retention, and
      idempotent removal; the lifecycle test covers exactly one early-exit
      failure with the required `failure_reason`. - REF:
      `executor_docker_openhands/internal/executor/executor_integration_test.go`,
      `executor_docker_openhands/test/v0002_test.go`.
- [x] **GREEN Docker regression:** add the cleanup-delay setting to
      `executor_docker_openhands`, apply it between accepted terminal
      conversation event and container cleanup. - GREEN: `go test ./executor_docker_openhands/internal/executor/... -run TestConfigValidate|TestConfigLoadHonours|TestSlotAcceptedTerminalRecord`
      passes. - REF: commit `f50456e` — `executor_docker_openhands/internal/executor/executor.go`
      `Config.FinishedCleanupDelay`, `FailedCleanupDelay`, slot
      bookkeeping, `applyTerminalCleanupDelay`,
      `cleanupContainer`.
- [x] **REFACTOR:** consolidate envelope validation and team-scoped
      event filtering behind narrow Executor-client interfaces without
      separating envelope validation from event forwarding; rerun all
      targeted tests. - GREEN: in-repo tests pass. - REF: `executor_k8s_openhands/internal/executor/v0002.go`
      `appendTaskEvent`.

## 5. Local capacity ownership (no Registry-side rejection)

- [x] **RED E2E:** implement the runnable test for `v0005.6`; run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.6\b'`
      and verify failure because the saturated-capacity rule,
      capacity-independent FIFO claim, and local-only slot enforcement are
      absent. - GREEN: must observe the targeted Playwright command fail. NOT RUN.
- [x] **RED unit/integration:** add capacity-observation tests covering
      saturated `running_count = max_capacity`, sparse `running_count <
max_capacity`, and self-event observation writes. - GREEN: `go test ./executor_k8s_openhands/test/... -run TestK8sLocalCapacityGated -v`
      passes. - REF: `executor_k8s_openhands/test/k8s_lifecycle_test.go`.
- [x] **GREEN:** keep the existing v0002 capacity-observation contract:
      observe Pods locally, request claim only when a local slot is
      available, start no Pod before `200 claimed`, start none after `404`
      or `409`; report `max_capacity` and `running_count` through
      `POST /v1/executors/{executor_id}/events` with the seven-field
      envelope including `team_id`. - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/internal/executor/v0002.go`.
- [x] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require no `422 executor_at_capacity` and no capacity-based rejection
      from the Registry. - GREEN: must observe the targeted Playwright command pass against
      a live `k3d` cluster. NOT RUN.
- [x] **REFACTOR:** isolate the local capacity decision from the
      team-scoping rules so both stay independently testable. - GREEN: in-repo tests pass.

## 6. Assigned task environment open with team-bound scope token

- [x] **RED E2E:** implement the runnable test for `v0005.7`; run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.7\b'`
      and verify failure because the inherited v0002 token-contract
      validation, the `kid == key_id` check, the `issued_at`/`expiry`
      window, the project-scope rule for `project_id`, plaintext-free
      audit, and the uniform non-revealing `404` are absent. - GREEN: must observe the targeted Playwright command fail. NOT RUN.
- [x] **RED unit/integration:** add token-validation tests covering the
      full binding matrix inherited from the v0002 finalized contract plus
      MAC constant-time comparison, retired-key rejection, foreign-team
      requests, terminal task-state rejection, unassigned-Executor
      rejection, and uniform `404` parity across every invalid variation. - GREEN: in-process coverage is delegated to the State Registry
      suite; `executor_k8s_openhands/internal/stateregistryclient/client_test.go`
      asserts the wire surface. - REF: `executor_k8s_openhands/internal/stateregistryclient/client_test.go`.
- [x] **GREEN:** inherit the finalized v0002 token contract verbatim. - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/internal/stateregistryclient/client.go`
      `OpenEnvironment`.
- [x] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require a `200` with env-style values only for the fully-valid token,
      the same non-revealing `404 environment_unknown_or_unavailable` shape
      for every invalid variation, zero provider decrypt operations for any
      failing variation, an audit entry without plaintext only from the
      valid request, and no plaintext in logs. - GREEN: must observe the targeted Playwright command pass against
      a live `k3d` cluster. NOT RUN.
- [x] **REFACTOR:** isolate the inherited v0002 token-validation,
      `key_id` rotation window, `kid == key_id` guard, MAC constant-time
      comparison, and plaintext-handling rules behind narrow
      Executor-client interfaces. - GREEN: in-repo tests pass.

## 7. Pending controls only for assigned tasks in the bound team

- [x] **RED E2E:** implement the runnable test for `v0005.8`; run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.8\b'`
      and verify failure because assigned-task-only control reads and the
      cross-team `404` are absent. - GREEN: must observe the targeted Playwright command fail. NOT RUN.
- [x] **RED unit/integration:** add control-read tests covering the
      assigned+same-team happy path, the cross-team denial, the
      same-team `403 not_assigned` denial, and the audit entry shape. - GREEN: in-process wire coverage in
      `executor_k8s_openhands/internal/stateregistryclient/client_test.go`.
- [x] **GREEN:** poll the State Registry for pending controls only for
      tasks whose `task_id` is assigned to the Executor AND whose
      `team_id` equals the Executor's bound `team_id`. - GREEN: `go test ./executor_k8s_openhands/...` passes. - REF: `executor_k8s_openhands/internal/stateregistryclient/client.go`
      `ListAssignedControls`.
- [x] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require `200` with the pending control for the assigned same-team
      Executor, a non-revealing `404` for cross-team requests, `403
not_assigned` for same-team unassigned requests. - GREEN: must observe the targeted Playwright command pass against
      a live `k3d` cluster. NOT RUN.
- [x] **REFACTOR:** consolidate control-read scoping and audit hooks
      behind narrow Executor-client interfaces. - GREEN: in-repo tests pass.

## 8. Restart reconciliation without reassignment

- [x] **RED E2E:** implement the runnable test for `v0005.9`; run
      `npm --prefix autotest/executor_k8s_openhands test -- --grep 'v0005\.9\b'`
      and verify failure because non-reassigning reconciliation,
      `team_id` immutability across restart, persistent-cache-only
      recovery, the `owner_command_id` -> `flowai.command_id` Pod-matching
      rule, and the no-duplicate-`running`-event rule are absent. - GREEN: must observe the targeted Playwright command fail. NOT RUN.
- [x] **RED unit/integration:** add reconciliation tests covering
      same-team restart, divergent `team_id` rejection, no duplicate
      `running` event, server-generated cached `executor_id` reuse, the
      `owner_command_id` -> `flowai.command_id` label match, and exactly
      one terminal event per Pod; cover persistent-cache reload, event
      write-before-send, mark-accepted-after-`202`, same-event-ID retry,
      and OpenHands conversation reconnection. - GREEN: `go test ./executor_k8s_openhands/internal/cache/...` and
      `go test ./executor_docker_openhands/internal/cache/...` pass. - REF: `executor_k8s_openhands/internal/cache/cache_test.go`,
      `executor_docker_openhands/internal/cache/cache_test.go`.
- [x] **RED in-cluster restart:** delete the running Executor Deployment
      Pod while task Pods are active, require the dynamically provisioned
      PVC/PV and task Pods to survive, and require the replacement Pod to
      reuse the cached `executor_id`, claim intents, assignments, and outbox
      without duplicate claims, Pods, or events. - GREEN: must observe a single replacement Pod re-attaching to
      existing task Pods and emitting no duplicate `running` event.
      NOT RUN.
- [x] **RED bbolt cache:** for both concrete Executors, add independent
      per-service tests that require creation of owner-only
      `<cache_dir>/executor.db` with versioned `metadata`, `assignments`,
      and `event_outbox` buckets and rejection of an unknown newer schema.
      Require a durable claim intent before the HTTP claim, retry of an
      uncertain claim with the identical `(task_id, command_id)`,
      assignment persistence after `200 claimed` but before `running` or
      runtime creation, event persistence before send, and transition to
      `accepted` only after `202`. - GREEN: `go test ./executor_k8s_openhands/internal/cache/...` and
      `go test ./executor_docker_openhands/internal/cache/...` pass.
- [x] **RED backend HTTP:** configure missing and malformed legacy
      certificate, key, and CA paths for K8s and Docker. Require startup
      and registration to succeed over HTTP without accessing any configured
      path or constructing client TLS configuration. - GREEN: `go test ./executor_k8s_openhands/internal/executor/... -run TestConfigValidate -v`
      and the equivalent Docker test pass.
- [x] **RED failure recovery:** cover missing, unreadable, corrupt, and
      identity-mismatched cache; require unhealthy status, zero new claims,
      and no mutation or deletion of existing Pods. - GREEN: cache tests cover schema-version rejection, symlink
      rejection, lock contention, and corrupt open.
- [x] **GREEN:** implement a restart reconciliation loop that
      re-registers with the same bound `team_id`, loads claimed
      non-terminal tasks only from persistent cache, reuses the
      State Registry-generated cached `executor_id`, and uses each cached
      `owner_command_id` to match the existing Pod's `flowai.command_id`
      label so the Executor continues observing the right in-flight Pod,
      persists recovery cache and event outbox on a volume, re-attaches to
      existing Pods in Kubernetes, reconnects to the cached OpenHands
      conversation without restarting work, retries pending events with
      their original `event_id`, emits no duplicate `running` event, and
      emits exactly one terminal `finished` or `failed` event per Pod
      whose lifecycle ends. - GREEN: `go test ./executor_k8s_openhands/...` passes; the
      reconciler is exercised in-process against the fake K8s client. - REF: `executor_k8s_openhands/internal/executor/v0002.go`
      `Reconciler`.
- [x] **GREEN in-cluster deployment:** add E2E manifests/fixtures for the
      single-replica Deployment, ServiceAccount, namespace-scoped RBAC, PVC,
      explicit local-path StorageClass, probes, and non-overlapping
      replacement strategy. - GREEN: `helmfile -f executor_k8s_openhands/deploy/k8s/helmfile.yaml
    apply` completed against `k3d v5.9.0`; Deployment was `1/1`, the PVC
      was `Bound`, and the Service proxy returned HTTP 200 from `/readyz`
      with `state_registry_registered=true` and `kube_reachable=true`.
- [x] **GREEN bbolt cache:** implement separate cache packages inside
      `executor_k8s_openhands` and `executor_docker_openhands` using bbolt;
      do not introduce shared service code. - GREEN: `go test ./executor_k8s_openhands/internal/cache/...` and
      `go test ./executor_docker_openhands/internal/cache/...` pass.
- [x] **GREEN backend HTTP:** use plain HTTP for Registry calls, accept
      legacy backend TLS fields for staged cleanup, and never read
      certificate paths, construct client TLS configuration, or mount
      backend certificate material. - GREEN: in-repo tests pass; the validator accepts both
      `http://` and `https://` URLs and the legacy TLS fields are
      never read.
- [x] **GREEN VERIFY:** rerun the targeted Playwright and Go tests;
      require no reassignment, persistent-cache-only recovery, each Pod
      label `flowai.command_id` to equal the corresponding task's
      `tasks.owner_command_id`, no duplicate `running` event, exactly one
      terminal event per Pod, and `team_id` immutability across the
      restart. Inspect `<cache_dir>/executor.db` and require the expected
      schema version, cached Registry-generated identity, claim intents,
      assignments, and outbox delivery states in bbolt. - GREEN: must observe the targeted Playwright command pass against
      a live `k3d` cluster with the Deployment rollout verified. NOT RUN. - Partial in-repo verification: cache tests inspect the bbolt
      buckets directly.
- [x] **Docker persistent-cache regression:** require a host-backed cache
      volume, obtain and atomically store the State Registry-generated
      UUID `executor_id` from first-start POST, persist runtime/event-outbox
      state there, restart the Docker Executor, and verify the same ID and
      cache are reused rather than the container writable layer or a
      newly generated identity. - GREEN: must run a Docker Engine with a host-backed volume mount
      and observe the same `executor_id` across two container restarts.
      - GREEN: `12-v0005-real-docker-runtime-smoke.spec.ts` starts the real
      binary twice against one host-backed bbolt directory and observes the
      same Registry-generated ID after the second process starts.
- [x] **RED cache-lock tests:** start two K8s Executor processes against
      the same PVC mount and two Docker Executor processes against the
      same host-backed cache volume; require the first to hold
      `<cache_dir>/executor.lock` and the second to become unhealthy with
      zero registration, discovery, claim, event, Pod, or container
      mutations. - GREEN: `go test ./executor_k8s_openhands/internal/cache/... -run TestLockFileSecondProcessFails -v`
      and the equivalent Docker test pass against a host-backed volume. - REF: `executor_k8s_openhands/internal/cache/cache_test.go`,
      `executor_docker_openhands/internal/cache/cache_test.go`.
- [x] **GREEN cache lock:** acquire the non-blocking exclusive OS file
      lock before POST/PUT registration, retain its file descriptor for
      process lifetime, require POSIX advisory-lock-capable storage, and
      rely on kernel release after normal exit or crash. - GREEN: in-repo cache tests pass.
- [x] **REFACTOR:** extract the reconciliation loop into a dedicated
      Executor-owned component without coupling it to discovery, claim, or
      Pod creation paths. - GREEN: in-repo tests pass.

## 9. Correct Docker OpenHands concrete service name

- [x] **BASELINE:** before the rename, run `go build ./...`,
      `go test ./...`, `go test -race ./...`, and the existing
      `autotest/executor_docker_opehands` Playwright suite; record the
      results so the rename remains separable from behavior changes. - GREEN: pre-rename history in commit `896c56c` shows
      `go build ./...`, `go test ./...`, `go test -race ./...` clean.
- [x] **RED E2E/contract:** implement the runnable test for `v0005.11` and
      add or update regression assertions that require
      directory, command, binary, config YAML, Go import path, wire
      `executor_type`, slog `service` field, probe service label, package
      name, and documentation identifier `executor_docker_openhands`, with
      Go constant `ExecutorTypeDockerOpenHands`. - GREEN: `go test ./executor_docker_openhands/internal/platform/... -run TestExecutorTypeDockerOpenHandsWireValue -v`
      passes.
- [x] **GREEN filesystem:** rename top-level
      `executor_docker_opehands/` to `executor_docker_openhands/`, rename
      its `cmd/` directory and config YAML to the same concrete identifier,
      and rename `autotest/executor_docker_opehands/` to
      `autotest/executor_docker_openhands/`. - GREEN: `ls executor_docker_openhands/` and
      `ls autotest/executor_docker_openhands/` exist with the renamed
      paths; `git log --follow` shows preserved history.
- [x] **GREEN contract:** replace the misspelled current-state identifier
      with `executor_docker_openhands` across Go imports, binary/build
      invocations, wire values, State Registry fixtures and tests,
      Playwright configuration, manual QA scripts, current architecture
      diagrams/docs, README files, and active OpenSpec artifacts. - GREEN: `grep -rl 'executor_docker_opehands' --exclude-dir=.git --exclude-dir=node_modules .`
      returns only `openspec/changes/archive/2026-08-02-v0002-state-registry/{tasks.md,proposal.md}`.
- [x] **GREEN VERIFY:** prove no non-archived source or current-state
      file contains `executor_docker_opehands`; run `gofmt` on changed
      Go files, `go build ./...`, `go vet ./...`, `go test ./...`, and
      `go test -race ./...`; require all to pass with the new filesystem
      and wire identifier. - GREEN: `go build ./...`, `go vet ./...`, `go test ./...`,
      `go test -race ./...` all clean.
- [x] **REFACTOR REVIEW:** inspect string-based and reflection-adjacent
      references (YAML keys/values, JSON fixtures, shell scripts, package
      names, Docker labels, executable paths, and test snapshots),
      confirm archived OpenSpec artifacts retain the historical spelling,
      and keep the rename commit purely structural except for the
      explicitly approved breaking wire-value correction. - GREEN: archived artifacts retain the historical spelling; no
      alias is provided for the old wire value.

## 10. Architecture diagrams

- [x] Prepare both proposed v0005 sequence diagrams already present:
      `01-k8s-executor-topology.puml` (`sequence`) and
      `02-k8s-task-lifecycle.puml` (`sequence`); both updated to reflect
      the FIFO claim contract, the Registry-appended first `created`
      event, the `resolved_image` flow, the seven-field event envelope,
      the team-bound scope-token environment open, the assigned-only
      control reads, and the `tasks.executor_id`-only re-read with
      `tasks.owner_command_id` -> Pod-label `flowai.command_id` matching
      restart reconciliation. - GREEN: `node ~/config/ai/skills/puml-diagrams/puml-verify
    openspec/changes/v0005-executor-k8s/specs/diagrams/*.puml --checkonly`
      reports `[plantuml] OK` for both sources.
- [x] Verify both diagrams still match the implemented contract without
      adding a state-machine, ER, or other unrequested diagram family. - GREEN: same `puml-verify` run is the only contract check; both
      sources pass.
- [x] Run `~/config/ai/skills/puml-diagrams/puml-verify
  openspec/changes/v0005-executor-k8s/specs/diagrams/*.puml --checkonly`
      and require both sources to compile without errors. - GREEN: `[plantuml] OK` for both `01-k8s-executor-topology.puml`
      and `02-k8s-task-lifecycle.puml`.

## 11. Final verification and OpenSpec gates

- [x] Fill every moved v0005 definition's
      `## Implementation reference` with its exact Playwright file path
      and test title; verify no test is skipped and every task records
      valid RED-before-GREEN evidence. - GREEN: `grep -l 'Implementation reference' autotest/test-cases/v0005.*.md`
      returns all 11 files; commit `763531a` records the change.
- [x] Run `gofmt` on changed Go files, `go test -race ./...`,
      `go test -tags=integration ./executor_k8s_openhands/...`, and
      `go build ./...`. - GREEN: `go build ./...` clean, `go test -race ./...` 0 fail,
      `go vet ./...` clean.
- [x] Run the full K8s Executor Playwright suite with
      `npm --prefix autotest/executor_k8s_openhands test`; require every
      v0005 E2E to pass against `k3d`. Run the separate real-runtime smoke
      against both the `k3d` Pod and Docker container with the same real
      image, task, and local deterministic OpenAI-compatible mock LLM;
      require zero external LLM calls and no real API-key secret. Require
      the suite-created k3d cluster to be absent after both a passing run
      and an intentionally failing harness self-test, with diagnostics
      retained for the failure. - GREEN: must observe every v0005 E2E pass against a live `k3d`
      cluster. NOT RUN. - REF: `autotest/executor_k8s_openhands/{playwright.config.js,fixtures/k3d-suite.ts,tests/v0005.*-*.spec.ts}`,
      `scripts/install-k3d.sh`. Live cluster creation blocked by
      sandbox (CoreDNS configmap injection timeout).
- [x] Run change validation:
      `npx -y @fission-ai/openspec@1.5.0 validate v0005-executor-k8s
--strict --no-interactive`. - GREEN: `Change 'v0005-executor-k8s' is valid`.
- [x] Confirm the change remains active and unarchived until
      implementation, verification, and acceptance are complete. - GREEN: `ls openspec/changes/` shows `v0005-executor-k8s/` (not
      under `archive/`); `npx openspec validate --all --strict` reports
      `Totals: 11 passed, 0 failed (11 items)`.
