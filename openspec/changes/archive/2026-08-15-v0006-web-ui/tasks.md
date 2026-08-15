# Tasks

## 1. Implementation-start artifact transition

- [x] Move every `specs/test-cases/v0006.*.md` definition unchanged to `qa-e2e/test-cases/` before writing any runnable E2E test or production code; verify the source directory contains no `.md` files and destinations do not overwrite existing IDs.
- [x] Bootstrap or extend the repository E2E harness only under `qa-e2e/` (including Playwright configuration, service fixtures, browser/network capture, State Registry spy fixtures, and the test-only mocked proxy with an externally supplied team list); verify the harness can run a known smoke test before any v0006 RED run.

## 2. Selected-team Web UI slice (`v0006.1`-`v0006.5`)

- [x] **RED:** Generate runnable Playwright tests under `qa-e2e/web-ui/v0006-selected-team.spec.ts` from `v0006.1` through `v0006.5`; verify failures for missing single selection, team switching, replacement `team_id`, stale-response suppression, mocked-proxy-only browser topology, or refresh reconstruction.
- [x] **GREEN:** Implement the minimum Web UI shell, supplied-team selector, selected-team REST/live reload, stale request/subscription cancellation, and single mocked-proxy adapter origin without implementing team discovery, membership, Keycloak, authentication, or a production Gateway.
- [x] **REFACTOR:** Share selected-team context across pages without persisting canonical platform data; rerun the targeted and related Web UI suites.
- [x] Fill each moved `v0006.1`-`v0006.5` definition's `## Implementation reference` with `qa-e2e/web-ui/v0006-selected-team.spec.ts` and the exact Playwright test title; record the RED failure and GREEN pass commands/results.

## 3. Mocked-proxy REST and UI API slice (`v0006.6`-`v0006.9`, `v0006.11`)

- [x] **RED:** Generate runnable E2E tests under `qa-e2e/web-ui/v0006-mocked-proxy-rest.spec.ts` for supplied team lists, selected-team forwarding, excluded admin/ingestion/Executor routes, cross-team non-revealing reads, and non-authoritative response forwarding.
- [x] **GREEN:** Implement the test-only mocked proxy plus the State Registry operations in `specs/state-registry/openapi/ui-operator.openapi.yaml`; do not create an `api-gateway/` production service or authentication behavior.
- [x] **REFACTOR:** Keep fixture mode explicit and integration mode transparent; rerun mocked-proxy, State Registry contract, and Web UI suites.
- [x] Fill moved definitions `v0006.6`-`v0006.9` and `v0006.11` with exact implementation references and recorded RED/GREEN evidence.

## 4. Selected-team mocked-proxy WebSocket slice (`v0006.10`)

- [x] **RED:** Generate the runnable E2E WebSocket test under `qa-e2e/web-ui/v0006-selected-team-websocket.spec.ts`; verify a team switch closes the old subscription and rejects late frames.
- [x] **GREEN:** Implement selected `team_id` subscription forwarding and replacement through the mocked proxy and the documented State Registry team stream.
- [x] **REFACTOR:** Share team switching/cancellation behavior between REST and WebSocket adapters without adding authentication claims.
- [x] Fill moved definition `v0006.10-websocket-bootstrap-operator-and-team-isolation.md` with the exact implementation reference and recorded RED/GREEN evidence.

## 5. Scoped launch parameters, revisions, and control events (`v0006.12`-`v0006.14`)

- [x] **RED:** Generate runnable E2E coverage from `v0006.12` through
  `v0006.14` for global/team/task-type merge precedence, rejection of
  task-scoped definitions, ordinary env
  and logical-secret resolution, launch-parameter image precedence, immutable
  revision history, foreign-team denial, atomic requested control events,
  assigned-Executor results, unauthorized result rejection, team-bound live
  publication, and refresh reconstruction; run the targeted Playwright suite
  and verify behavior-specific failures before schema or production changes.
- [x] **GREEN:** Add the minimum State Registry schema, persistence, scoped
  merge, image resolution, revision and control-event REST operations,
  control projection, audit records, team-bound stream frames, mocked-proxy
  adapter routes, and Web UI launch-parameter/history/feed behavior
  required by all three definitions; rerun the exact RED command and verify
  all cases pass.
- [x] **REFACTOR:** Share scope, pagination, trusted-context, ordered-event,
  and audit primitives without merging lifecycle and control events, exposing
  secret material, or moving business logic into the mocked proxy; rerun the
  targeted and related State Registry, Executor, mocked-proxy, and Web UI suites.
- [x] Fill moved definitions `v0006.12`-`v0006.14` with exact Playwright
  implementation references and recorded RED/GREEN evidence.

## 6. Task live streaming log (`v0006.15`)

- [x] **RED:** Generate runnable E2E coverage for ordered log replay, live continuation, automatic following, automatic cursor reconnect, absence of interactive controls and an Executor-event list, and cross-team/unassigned denial.
- [x] **GREEN:** Implement the minimum assigned-Executor log append, durable ordered replay, team-bound live frames, mocked-proxy operator reads, and task-screen streaming log required by `v0006.15`.
- [x] **REFACTOR:** Keep log, lifecycle, and control channels separate; rerun related State Registry, Executor, mocked-proxy, and Web UI suites.
- [x] Fill moved definition `v0006.15-task-live-streaming-log.md` with exact Playwright implementation references and RED/GREEN evidence.

## 7. FlowAI v2 operator experience (`v0006.16`-`v0006.22`)

- [x] Package the browser artifact as the standalone `svc/web-ui/` runtime
  service with its own command, environment configuration, embedded static
  files, `/healthz` handler, unit tests, and service integration test; keep
  cross-service Playwright fixtures under root `qa-e2e/`.

- [x] **RED:** Generate runnable Playwright coverage under
  `qa-e2e/web-ui/v0006-mission-control.spec.ts` from `v0006.16` through
  `v0006.22` for dashboard period/status drill-down, history filters and
  pagination, task-to-Executor navigation, Executor inventory/detail,
  launch-parameter UI CRUD/history, team audit history, responsive layouts,
  keyboard/focus behavior, accessibility, and asynchronous/validation states;
  run a deterministic mocked proxy for the current Web UI HTTP and WebSocket
  contract without starting or requiring API Gateway; run the targeted file
  and verify behavior-specific failures rather than
  fixture, syntax, dependency, or route-bootstrap failures.
- [x] **GREEN:** Implement the minimum FlowAI v2 Web UI behavior required
  by the seven definitions, including subdued chart series, deterministic
  pagination with `10` as default, mobile task cards, cancellation adjacent to
  status, read-only Executor surfaces, secret-safe dialogs/history/audit, and
  desktop/tablet/mobile interaction states; rerun the exact RED command and
  verify all cases pass.
- [x] **REFACTOR:** Extract only presentation-level compositions inside the
  Web UI service boundary, keep the browser data adapter replaceable, and
  rerun the targeted file plus related Web UI E2E.
- [x] Fill moved definitions `v0006.16`-`v0006.22` with exact Playwright test
  titles, implementation references, and recorded RED/GREEN evidence.

## 8. State Registry → Executor → Web UI lifecycle (`v0006.23`)

- [x] Configure isolated K8s and Docker Playwright runtime scenarios so each
  executes ordered `v0006.24` (create and display team), then `v0006.25`
  (register and display Executor), then `v0006.23` (ingest, execute, and
  display task) steps while retaining one runtime and its captured IDs.
- [x] **RED/GREEN:** Implement `v0006.24` by creating a unique team through
  `POST /admin/teams`, configuring the mocked proxy with its returned
  `team_id`, and asserting that the canonical team name appears in the UI
  without any browser-side team administration action.
- [x] **RED/GREEN:** Implement `v0006.25` in both matrix projects by starting
  the corresponding concrete Executor, waiting for its real State Registry
  registration, and asserting that the captured `executor_id`, concrete
  `executor_type`, scope, tag, and activity appear in the UI without any
  browser-side Executor management action.
- [x] **RED:** Create a two-project Playwright matrix for
  `executor_k8s_openhands` and `executor_docker_openhands`. Reuse the K8s setup
  from `qa-e2e/executor_k8s_openhands/tests/v0005.4-claimed-task-runs-as-pod-with-running-then-terminal-events.spec.ts`
  and the Docker setup from
  `qa-e2e/state-registry/tests/contracts/12-v0005-real-docker-runtime-smoke.spec.ts`
  without copying their deployment logic. For each isolated project, start
  the Web UI and a mocked proxy that adapts live reads and WebSocket frames
  from its real State Registry. Implement `v0006.23` in a browser-capable spec
  and verify each project fails because its newly ingested task, concrete
  Executor, terminal state, or ordered events are not yet rendered—not because
  either reused Executor fixture fails.
- [x] **GREEN:** In each project, ingest one uniquely identified task through
  State Registry, reuse that concrete Executor's successful execution path,
  and implement the minimum UI/proxy behavior needed for the same captured
  `task_id` to appear in history, link to the correct Executor, reach
  `finished`, and show exactly `created → running → finished`; rerun the exact
  RED command and verify both projects pass.
- [x] **REFACTOR:** Share the browser assertions and mocked-proxy contract
  while adapting the existing K8s and Docker suite fixtures; do not copy their
  deployment or polling logic. Retain one task ingestion and one canonical
  State Registry event history per matrix project.
- [x] Fill moved definition
  `v0006.23-registry-task-completes-and-appears-in-ui.md` and definitions
  `v0006.24`-`v0006.25` with exact Playwright test titles, implementation
  references, and RED/GREEN evidence.
- [x] **RED:** In both the Docker and K8s matrix projects, implement
  `v0006.26` after team creation and Executor registration. Add a unique
  ordinary env variable by entering its key and value in the UI
  through the Web UI, assert the real State Registry definition and revision,
  reload the UI, ingest a task, and inspect the running Docker container or K8s
  Pod with `printenv`; verify each project fails for missing persistence,
  rendering, resolution, or injection rather than for unavailable runtime
  prerequisites.
- [x] For `v0006.26`, `v0006.27`, `v0006.32`, and `v0006.33`, require every
  create, replace, and delete mutation to originate from Playwright interaction
  with visible Web UI controls. Permit direct State Registry access only for
  post-action assertions and task ingestion; fail review if fixtures, SQL,
  mocked-proxy state, Executor configuration, or direct mutation APIs perform
  the launch-parameter mutation under test.
- [x] **GREEN:** Wire the minimum UI, mocked-proxy, State Registry resolution,
  and both Executor environment-injection paths needed for the same unique
  value to appear in State Registry, survive UI reload, and be readable inside
  each captured task runtime; let both tasks finish and keep their lifecycle
  green.
- [x] **REFACTOR:** Reuse both real-runtime fixtures and runtime lookup by
  `flowai.task_id`; do not seed the value through test-process env, mocked
  proxy state, Executor configuration, or either runtime image. Fill
  `v0006.26-env-persists-and-reaches-docker-container.md` with its exact
  Playwright reference and RED/GREEN evidence.
- [x] **RED/GREEN:** Implement `v0006.27` in both matrix projects. Create one
  unique logical secret by entering its key and value through the UI, assert opaque State Registry metadata
  and immutable version history, reload the redacted UI, then verify the exact
  value in memory inside the assigned Docker container and K8s Pod. Register
  the plaintext with the test redactor before submission and fail if it occurs
  in any response, browser storage, screenshot, trace, report, audit entry, or
  service log. Fill the definition with both Playwright project references and
  RED/GREEN evidence.
- [x] **RED:** Implement `v0006.32` and `v0006.33` in both matrix projects
  after their corresponding create-and-inject cases. Delete the ordinary env
  variable and delete the logical secret through the UI, assert the real State
  Registry current projections and immutable removal histories, reload the UI,
  then ingest fresh tasks and inspect only their newly created Docker
  containers or K8s Pods with presence-safe checks. Verify RED fails because a
  removed key remains in Registry resolution, the active UI, or a new runtime,
  not because either real-runtime fixture is unavailable.
- [x] **GREEN/REFACTOR:** Make committed env deletion and secret revocation
  remove their active references from later launch-parameter resolutions while
  preserving immutable history and secret redaction. Require both new tasks to
  finish, require the deleted names to remain absent after UI reload, scan all
  artifacts for secret plaintext, share the Docker/K8s absence assertion, and
  fill `v0006.32` and `v0006.33` with exact Playwright references and recorded
  RED/GREEN evidence.
- [x] **RED/GREEN:** Implement `v0006.28` in both matrix projects with
  `max_capacity=1`. Hold the first task in `running`, ingest a second eligible
  task, and assert through State Registry and UI that it remains `pending`,
  unassigned, eventless, and without a runtime for at least two discovery
  intervals. Release the first task and assert that the second is then claimed
  by the same Executor and reaches `created → running → finished`, while the
  concrete runtime count never exceeds one. Reuse the existing Docker
  `v0002.13` and K8s `v0005.6` capacity fixtures and fill both Playwright
  project references with RED/GREEN evidence.
- [x] **RED:** Implement the data-driven `v0006.29` browser suite with a
  declared filter manifest for dashboard, tasks, Executors, launch parameters,
  env revision history, secret version history, audit, and Executor active
  tasks. Fail when any rendered `data-filter-key` lacks a manifest entry or a
  declared filter is absent, then exercise individual values, combinations,
  empty results, reset, URL/reload/history, pagination, desktop/mobile parity,
  keyboard operation, and accessible announcements.
- [x] **GREEN:** Implement or correct every rendered filter so visible item
  identifiers, counts, proxy queries, URL state, pagination, and reset defaults
  agree with the same deterministic fixture model on desktop and mobile; keep
  foreign-team rows and secret plaintext absent.
- [x] **REFACTOR:** Share only filter-state and test-model helpers, retain one
  explicit manifest entry per semantic filter key, and fill
  `v0006.29-all-filterable-views.md` with the exact Playwright references and
  RED/GREEN evidence.
- [x] **RED/GREEN:** Implement `v0006.30` with a controlled current-day clock
  and an unmatched execution tag. Capture dashboard baselines, ingest one
  task through the real State Registry listener API, and require total,
  `pending`, and the current chart bucket to increase exactly once within two
  seconds of mocked-proxy frame receipt without reload. Verify replay,
  reconnect, and foreign-team commits do not change counts.
- [x] **RED:** Extend the existing real OpenHands image smoke sequence with one
  explicitly published agent message while retaining the real agent-server,
  existing marker-writing tool call, observation, finish call, local mock LLM,
  and denied external LLM access. Implement `v0006.31` in both Docker and K8s
  browser projects and verify missing, late, misclassified, reordered, or
  duplicated live entries fail for behavior-specific reasons.
- [x] **GREEN/REFACTOR:** Map published agent output to `reasoning` and
  action/tool/observation/completion output to `work`; stream committed offsets
  through State Registry and mocked proxy; render each within two seconds of
  proxy receipt; and preserve automatic follow/reconnect behavior without
  rendering live-log controls. Share the scripted
  real-runtime sequence and UI assertions across Docker and K8s, then fill
  `v0006.30` and `v0006.31` with exact Playwright references and RED/GREEN
  evidence.

## 9. Completion gates

### Dashboard periods and complete weekly buckets

- [x] **DESIGN:** Update the FlowAI v2 dashboard prototype in Open Design:
  remove the day period; retain navigable ISO week and calendar month; label
  weeks `Week NN, YYYY`; prevent future navigation; preserve selections in URL
  and browser history; and render zero-value calendar days. The accepted
  Preview is recorded in `design.md`.
- [x] **SPEC:** Replace the former three-period dashboard contract with week/month
  in the Web UI v0006 delta. Require seven ordered calendar buckets for every
  week response, all calendar days for month responses, zero counts for days
  without tasks, calendar navigation, URL state, and Back/Forward restoration.
- [x] **RED:** Extend `v0006.16` and dashboard API coverage so tests fail while
  the day selector exists, a week omits an empty day, buckets are unordered,
  or a current-week future day is absent.
- [x] **GREEN:** Implement week/month-only dashboard selection and complete
  Monday-through-Sunday weekly buckets without changing task-history period
  filters unless the updated design explicitly requires it.
- [x] **REFACTOR:** Keep calendar bucket completion authoritative in State
  Registry, keep Web UI rendering data-driven, and record Open Design Preview
  plus RED/GREEN evidence in `v0006.16`.

- [x] **RED:** Add a Web UI service test that builds
  `svc/web-ui/Containerfile`, starts the resulting image, and fails if the
  browser artifact or `/healthz` is supplied by a host binary or in-process
  server.
- [x] **GREEN:** Add the production Web UI `Containerfile` and make the Web UI
  Playwright harness use that image while retaining the test-only mocked proxy
  as the browser's sole backend origin.
- [x] **REFACTOR:** Cache one Web UI image per test worker, record its image
  identity, and keep production build/runtime files inside `svc/web-ui/`.

- [x] Run the complete repository E2E suite rooted at `qa-e2e/`; verify selected-team REST, WebSocket, UI, stale-response suppression, mocked-proxy forwarding, State Registry scoping, and cross-team isolation cases pass with no skipped tests.
- [x] Verify no production API Gateway service is added; Web UI has one mocked-proxy adapter origin, exactly one selected team from the externally supplied list, no membership/Keycloak/team/source-system/task-type administration, and no global admin projection; `team_name` is display-only and all sequence diagrams remain sequence-family `.puml` sources.
- [x] Verify Web UI exposes no task creation, launch, clone, or retry-as-new action and mocked proxy rejects every listener/automation task-ingestion route before forwarding.
- [x] Verify launch parameters merge ordinary env values and logical secret
  references by `global → team → task_type`, task-scoped definitions are
  rejected, task-type bindings are same-team, global mutation is absent from v0006, image
  precedence ends with the required team default, revision history contains
  no secret material, and control events never become task lifecycle states.
- [x] Run the repository's normal build and related unit/integration suites as supplementary checks; these do not replace the required E2E RED/GREEN evidence.
- [x] Run `npx -y @fission-ai/openspec@1.5.0 validate v0006-web-ui --strict --no-interactive` after implementation artifacts and task evidence are complete.
