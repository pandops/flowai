# Design: K8s Executor

## Runtime Shape

- K8s Executor runs inside or against a Kubernetes cluster. E2E runtime
  verification uses a fresh isolated local `k3d` cluster created once per
  Playwright suite run with a collision-resistant name; Docker verification
  uses Docker Engine. The harness never reuses a pre-existing cluster. Before
  teardown it captures Pod descriptions, events, logs, and relevant Kubernetes
  objects as test artifacts, then deletes the cluster on success or failure.
  Deterministic API/lifecycle/error tests use an
  OpenHands-compatible agent API mock. In addition, one smoke test per
  runtime uses the same real `autotest/agent-openhands-image` and runs one
  short real OpenHands task through to conversation `finished`. The real
  agent-server is configured against a local deterministic OpenAI-compatible
  mock LLM shared by both smoke paths. The mock returns the scripted tool-call
  sequence needed to write the unique workspace marker; no external LLM,
  network credential, or CI API-key secret is required.
- In E2E the K8s Executor itself runs as a single-replica Deployment inside
  `k3d`, under a dedicated ServiceAccount and least-privilege namespace RBAC.
  A PVC mounted at `<cache_dir>` retains `executor.db` and `executor.lock`
  across Executor Pod replacement. The Deployment uses a non-overlapping
  replacement strategy so only one Executor Pod may own the local cache at a
  time. Recovery is exercised by deleting the Executor Pod and waiting for its
  replacement, while task Pods remain running. Task Pods therefore carry no
  owner reference to the ephemeral Executor Pod; lifecycle ownership remains
  explicit in Executor reconciliation and cleanup.
- The E2E harness installs a repository-pinned local-path provisioner manifest
  into each temporary `k3d` cluster and waits for its controller to become
  ready. The Executor cache claim explicitly names that provisioner's
  StorageClass and uses `ReadWriteOnce`; no ambient or host-cluster default
  StorageClass is assumed. The dynamically provisioned PV and PVC survive
  Executor Pod replacement and disappear only with test namespace/cluster
  teardown. Floating `latest` provisioner images or manifests are forbidden.
- On startup it authenticates to the State Registry as one Executor service
  identity and registers exactly one immutable ownership `scope` from
  `{team, system}`. Team scope binds the identity to exactly one immutable
  `team_id`; system scope carries no team binding. The Executor never invents
  or rotates task ownership.
- `executor_id` is not configured or client-generated. On an empty first-start
  cache the concrete Executor calls `POST /v1/executors`, receives the
  State Registry-generated UUID, and atomically stores it before task intake;
  subsequent starts reuse it through `PUT /v1/executors/{executor_id}`. K8s stores the cache on a PVC. Docker stores the
  same identity/recovery cache in a host-backed volume, never only in the
  container writable layer.
- Before any registration call, the process takes a non-blocking exclusive OS
  lock on `<cache_dir>/executor.lock` and holds it until exit. Lock contention
  is fail-closed and produces unhealthy status with no Registry or runtime
  mutation. The cache filesystem must provide POSIX advisory locks. No
  Registry lease/fencing protocol is added; cloning a cache into independent
  volumes is an unsupported operation that this local lock cannot detect.
- Cache persistence uses one bbolt file at `<cache_dir>/executor.db` with
  versioned `metadata`, `assignments`, and `event_outbox` buckets. Mutations
  commit transactionally before external side effects; unsupported schema or
  corruption is fail-closed.
- Executor-to-Registry transport is plain HTTP. Legacy backend TLS fields are
  accepted for staged cleanup but their paths are never read and no client TLS
  configuration is constructed. Deployment network policy owns the caller
  boundary.
- The `assignments` bucket also stores a durable claim intent before the HTTP
  claim so an uncertain response can be retried with the identical
  `(task_id, command_id)`; the claimed assignment is committed before
  `running` or runtime creation. Cache permissions are owner-only and no
  secret/environment plaintext is stored.
- The concrete directory, command, config, binary, import path, wire
  `executor_type`, slog/probe service name, and regression constant use
  `executor_k8s_openhands`; the selected agent tool is OpenHands and the
  Go wire constant is `ExecutorTypeK8sOpenHands`.
  The OpenSpec change ID remains `v0005-executor-k8s`.
- As a prerequisite structural correction, the existing Docker OpenHands
  service is renamed from `executor_docker_openhands` to
  `executor_docker_openhands`. Its correctly spelled Go constant remains
  `ExecutorTypeDockerOpenHands`, but its wire value changes to
  `executor_docker_openhands`. The old wire value receives no compatibility
  alias; archived OpenSpec artifacts retain their historical spelling.
- Registration supplies exactly one `authorized_tag`, observed
  `max_capacity`, observed `running_count`, and runtime metadata. Team scope
  also supplies one identity-bound `team_id`; system scope supplies no team
  binding and omits the `team_id` property rather than sending null. Scope and any team
  binding are immutable across re-registration and restart. The request body
  carries no `identity`; State Registry derives and persists the canonical
  identity from the generated Executor ID and the submitted, validated
  registration configuration.
- It discovers eligible `pending` tasks only where the task's
  `required_tag` equals the Executor's single `authorized_tag`; team scope
  additionally matches the bound `team_id`, while system scope spans teams
  and receives metadata-only summaries until claim. The
  eligibility predicate is applied BEFORE FIFO ordering
  `(ingested_at ASC, task_id ASC)`, BEFORE pagination, and BEFORE
  counts. Discovery is read-only, SHALL NOT reserve or assign a task,
  and SHALL NOT read, compare, or enforce `max_capacity` or
  `running_count`. Discovery MAY return the same task to multiple
  eligible same-team Executors.
- Before starting work it asks the State Registry to claim a specific
  `task_id` using `POST /v1/executors/{executor_id}/claim` with the
  `task_id` and a stable `command_id`. Claim is allowed only when the
  task is `pending`, the task's required tag matches the Executor's
  single registered tag, the scope-conditional eligibility predicate
  holds, AND the requested task is the OLDEST currently eligible
  `pending` task for that authenticated Executor. Inside one
  transaction, claim atomically sets `tasks.owner_command_id` (from the
  request `command_id`) and `tasks.executor_id` (from the claiming
  Executor), persists `tasks.resolved_image` and `tasks.image_source`
  from the four-level precedence (`tasks.image` ->
  `task_types.default_image` -> `source_systems.default_image` ->
  required `teams.default_image`), sets `tasks.claimed_at` to the
  transaction commit time, and appends the FIRST lifecycle event
  `created` with `executor_id` non-null and equal to the claiming
  Executor and a payload meaning `task <task_id> loaded by <executor_id>`.
  The Registry then projects the task to `created` and removes the task
  from the claimer's scope of discovery. The K8s Executor SHALL use
  `tasks.resolved_image` verbatim and SHALL refuse the task if it
  cannot be pulled or started; no local fallback image is permitted and
  the Executor SHALL NOT substitute its own image for the
  Registry-resolved one. Image equality between tasks in different
  teams grants no authority or cross-team visibility. After successful
  claim the Executor emits a `running` task event and then creates a
  fresh Kubernetes Pod carrying the immutable labels
  `flowai.executor_id`, `flowai.team_id`, `flowai.task_id`,
  `flowai.command_id`, `flowai.executor_scope`,
  `flowai.resolved_image_source`, and the runtime label
  `flowai.runtime=k8s`. The claim-conflict taxonomy is: unknown or
  foreign `task_id` returns non-revealing `404`; same `(task_id,
command_id)` retry by the original Executor returns the original
  `200 claimed` body with no new event; a different `command_id` for an
  already-claimed task returns `409 task_already_claimed`; an eligible
  non-oldest task returns `409 older_task_must_be_claimed_first`. No
  `dispatched` state or event exists; no event is appended at
  ingestion. Cross-team point-resource lookups for a specific `task_id`
  (claim, event write, control read, environment open bound to that
  `task_id`, task detail read) are indistinguishable from an unknown
  `task_id` and return a non-revealing `404` without appending any
  event and without creating any Pod.
- The assigned Executor emits a `running` task event before creating
  the Pod. For OpenHands, it treats the terminal conversation event as
  authoritative: normalized `execution_status = finished` produces the
  single `finished` event, while `failed`, `error`, `stuck`, or `paused`
  produces the single `failed` event. The OpenHands agent-server is a
  long-running server, so its process or Pod exit code is not the task
  completion signal. Every `failed` payload includes a non-empty,
  machine-readable `failure_reason`. An agent-container restart before the
  OpenHands `finished` signal is terminal failure with
  `failure_reason = "pod_restarted_before_finish"`; the Executor does not
  resume or recreate that execution. After State Registry accepts the terminal event,
  the Executor selects the configured non-negative
  `finished_cleanup_delay` after `finished` or `failed_cleanup_delay`
  after `failed` (each default `0s`), keeps the Pod in its local capacity
  count during that delay, and then deletes it idempotently.
  The existing Docker OpenHands Executor gains the same setting and
  ordering for stop/removal of its task container. If the Docker task
  container exits with any exit code before OpenHands reports `finished`, it
  produces exactly one `failed` event with `failure_reason =
  "container_exited_before_finish"`; exit code `0` is not success and the
  execution is not resumed or recreated. Every
  Executor-emitted task or self event carries `task_id`
  (when applicable), `executor_id`, `team_id` (equal to the team-owned
  Executor's bound team or the system-owned Executor's assigned task team), `event_id`,
  `event_type`, `occurred_at`, and `payload`. The Registry event-denial taxonomy is: an authenticated
  Executor whose envelope `team_id` differs from its immutable service
  binding is rejected with `403 team_mismatch`; a same-team Executor
  that is not the recorded `tasks.executor_id` is rejected with `403
not_assigned`; a foreign task or Executor point identifier is rejected
  with the same non-revealing `404` shape used for an unknown
  identifier. No `403` response is returned for a foreign-team probe.
- It decides locally when to discover, claim, and start a Pod based on
  its own capacity observation; the State Registry SHALL NOT gate
  discovery or claim on `max_capacity` or `running_count`, and SHALL
  NEVER emit a capacity-based rejection.
- It opens an environment for an assigned task only after binding to
  the assigned task's `team_id` AND presenting the State Registry-issued
  compact three-part signed scope token `<header>.<payload>.<signature>`
  carried only in the `X-FlowAI-Scope-Token` request header for
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`. The
  protected header carries `alg` (allow-listed), `kid`, and `typ`
  (`scope-token+json`); the payload carries `team_id`, `project_id` (a
  required claim; nullable only when the canonical environment has no
  project scope), `task_id`, `environment_id`, `executor_id`, `audience`
  (literal `state-registry.environment.open`), `issued_at`, `expiry`
  (`expiry > issued_at` and `expiry - issued_at <= 5 minutes`), and
  `key_id` selecting a State Registry-controlled active key. The State
  Registry verifies that the protected-header `kid` equals the payload
  `key_id` BEFORE any MAC computation, accepts only algorithms in the
  allow-listed HMAC set `HS256`/`HS384`/`HS512` (the "HMAC-SHA-256 or
  a stronger HMAC" family), recomputes the signature under the declared
  allow-listed algorithm using the server-side key handle and compares
  it under constant-time comparison, verifies the `key_id` against the
  documented active key window (retired keys rejected), verifies
  `issued_at <= server_now + 30 seconds`, verifies the literal audience
  `state-registry.environment.open`, verifies the canonical claim shape
  (including the project-scope rule for `project_id`), verifies the
  persisted Executor assignment and submitted request context, the Executor's same-team
  ownership, the task assignment, the task's non-terminal state, and
  the project/task applicability BEFORE any decrypt operation by the
  active provider or plaintext disclosure. A retry within the TTL by
  the same assigned same-team Executor MAY be allowed only when every
  canonical claim, transition, and assignment check still passes; it
  SHALL NOT extend TTL, SHALL NOT bypass canonical checks, and SHALL
  NOT revive an expired token. Any invalid or unavailable condition —
  missing or empty token, tampered MAC, algorithm outside the
  allow-listed HMAC set, `kid` not equal to payload `key_id`, `key_id`
  outside the active window, lifetime exceeding five minutes, expired
  or premature issuance, audience mismatch, canonical-claim mismatch
  (including a null `project_id` for a project-scoped environment, or
  a missing `project_id`), `team_id` mismatch, terminal task state, or
  not-assigned caller — returns the same non-revealing `404
environment_unknown_or_unavailable` shape with zero provider
  decrypt operations and no token plaintext, individual claim values
  beyond identifier-level metadata, MAC bytes, key material, or
  derived key bytes in logs, audit entries, or error responses.
  Plaintext values live in memory only while the response is composed
  and are injected only into that task's Pod.
- It reads pending operator controls only for tasks assigned to the
  Executor and uses the claimed task's immutable `team_id` as the envelope.
  Team scope cannot read a foreign-team task; system scope gains no authority
  beyond tasks canonically assigned to that Executor.
- It observes Pods it started and writes running-child-count and
  lifecycle events to State Registry as informational observations only.
  Self events carry `team_id` so the Registry can scope observation
  writes to the correct team partition without using them for
  authorization.
- After a restart it loads already-claimed non-terminal tasks exclusively
  from its persistent recovery cache and matches each cached
  `owner_command_id` to the immutable Pod label `flowai.command_id`. The
  server-generated stable `executor_id` is read from that cache. The K8s Executor SHALL NOT re-discover
  or re-claim already-claimed tasks, SHALL NOT create a duplicate Pod
  for an already-running task, and SHALL NOT emit a duplicate
  `running` event for it. A restart continues observation of in-flight
  Pods from Kubernetes and emits the terminal `finished` or `failed`
  event only when the recovered OpenHands conversation reaches terminal
  state. A persistent-volume-backed recovery cache stores assignment keys,
  Pod identity, OpenHands conversation identity, last observed tool state,
  and a durable event outbox. Events are persisted before send and marked
  accepted only after Registry `202`; pending entries are retried with the
  same `event_id`. Startup reconciles cached runtime/outbox state and Pods
  selected through immutable labels, then
  reconnects to the existing conversation instead of restarting work.
  Missing, unreadable, corrupt, or identity-mismatched cache makes the
  Executor unhealthy; it claims no new work and leaves existing Pods untouched.
  event in the same team-scoped envelope.

## Boundaries

- State Registry never calls Kubernetes; only the K8s Executor controls
  Pods.
- The K8s Executor never runs Docker containers and never contacts Web
  UI or API Gateway.
- `team_id` is authoritative for matching and authorization. Executor
  registration never carries `team_name`.
- Cross-team interactions are split between collection-level filtering
  and point-resource denial. Collection-level discovery filters by
  `team_id` BEFORE shaping results, so a tag whose matching `pending`
  tasks all belong to other teams yields the NORMAL empty result
  (`204 No Content` or `200` with an empty task list and no count,
  cursor, total, or pagination metadata), indistinguishable from "no
  tasks match this tag anywhere" — never `404`. Point-resource lookups
  (claim for a specific `task_id`, event write for a specific
  `task_id`, control read for a specific `task_id`, environment open
  for a specific `environment_id` bound to a specific `task_id`, task
  detail read for a specific `task_id`) yield a non-revealing `404`
  indistinguishable from "resource does not exist", so cross-team
  existence is never disclosed through point-resource responses.
- Local capacity is a local Executor concern. The Registry never returns
  `422 executor_at_capacity` or any other capacity-based rejection.
- Image strings are not team-owned resources and image equality
  between tasks in different teams grants no authority or cross-team
  visibility. The K8s Executor uses `tasks.resolved_image` verbatim and
  refuses a task when the resolved image cannot be pulled or started;
  the Executor SHALL NOT maintain a local fallback image.
- The K8s Executor is non-authoritative across restarts: it does not
  reassign work, does not re-claim already-claimed tasks, and does not
  duplicate Pods or `running` events.
- The K8s Executor SHALL NOT append a `created` event itself. The
  FIRST lifecycle event `created` is appended transactionally by the
  State Registry on successful claim; the Executor appends `running`,
  `finished`, and `failed` only.

## Recovery after terminal cleanup

The cleanup delay is an inspection/retention window, not durable recovery.
Before cleanup, an operator may still inspect the live runtime, but the task
is already terminal in State Registry and must not accept new work under the
same lifecycle.

After a Pod or container is deleted, its writable layer, in-memory
OpenHands conversation, and process state are gone. Continuing work then
requires durable state external to that runtime:

1. persist the workspace and OpenHands `persistence_dir` on a
   team/task-scoped PVC or artifact store; OpenHands stores
   `base_state.json` plus its append-only conversation event files there;
2. persist a tool-neutral checkpoint containing the OpenHands conversation
   identifier, source task, pinned image/tool version, workspace snapshot
   identity, and any encryption-key reference needed to restore secrets; and
3. create a new continuation task linked to the terminal source task,
   then start a new Pod that restores the workspace and resumes or
   reconstructs the conversation.

Reopening a `finished` task would violate the append-only lifecycle and
immutable assignment contract. This change therefore does not promise
post-deletion continuation. A follow-up change should choose between
workspace-only continuation (simpler, conversation reconstructed from a
summary) and full OpenHands restoration using the same conversation ID and
persistence directory (higher fidelity, but it requires compatible agent
tools/configuration, durable secret handling, and a pinned/tested OpenHands
persistence contract). Workspace-only continuation is the safer initial
platform contract; full restoration can be an OpenHands-specific extension.

## Proposed Diagrams

- `specs/diagrams/01-k8s-executor-topology.puml` (sequence)
- `specs/diagrams/02-k8s-task-lifecycle.puml` (sequence)

No state-machine, ER, or other restricted diagram family is added; the
team-model rules are expressed as normative requirements in
`specs/executor/spec.md` and as sequence interactions in the two
diagrams above.
