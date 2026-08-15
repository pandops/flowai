# Design: v0006 Web UI

## Prototype source of truth

- The accepted interaction and visual reference is Open Design project id
  `ad580ba5-d6e3-4d40-93e4-ac2533a4c55a`, artifact `index.html`.
- The implemented Web UI SHALL match that artifact's information hierarchy,
  screen composition, responsive behavior, copy, states, and interactions.
- Product identity is `FlowAI v2`. Former prototype codenames, third-party
  branding, the Raiffeisen logo, and crossed-hammer marks do not appear.
- All operator-facing copy is English. Protocol identifiers such as
  `task_type_id` remain unchanged where the identifier itself is useful.

## Visual contract

- The interface uses the prototype's dark operational palette: near-black
  page background `#090b12`, dark surfaces `#121722` and `#1b2233`, light
  foreground `#f8fafc`, slate borders, and restrained pale blue `#60a5fa` for
  selection, focus, information, links, and primary actions.
- Dashboard bars use a subdued blue series. Zero is a visible 2 px baseline;
  one run is visibly taller at 40 px; positive values scale upward with a
  190 px cap. A zero bucket and a one-run bucket cannot look equal.
- Success, warning, and failure semantics use green, amber, and red. Destructive
  controls use red; blue is not used to imply destructive meaning.
- Desktop uses the persistent left navigation and data tables. Tablet compacts
  spacing without dropping actions. Mobile uses bottom navigation, stacked
  toolbars, cards instead of horizontally overflowing tables, and full-width
  dialogs where needed.
- Hover, `focus-visible`, active, disabled, loading, validation, empty, and
  recoverable error states follow the same component language on every screen.

## Navigation and browser state

- The persistent menu contains `Overview`, `Tasks`, `Executors`, `Parameters`,
  and `Audit`. Team selection appears in the menu only and one team is always
  selected when the supplied list is non-empty.
- Explicit navigation, dashboard calendar arrows, and period changes add
  browser-history entries. Back and Forward restore the selected team, view,
  detail identifier, dashboard period and calendar key, and shareable filters.
- Changing team resets the dashboard to the current ISO week, clears stale
  detail/filter state, cancels in-flight work, and reloads with the replacement
  `team_id`.

## Screen model

### Overview

- The dashboard header contains only dashboard actions: `Week` and `Month`
  period controls and the selected period's calendar arrows. Team selection is
  not duplicated in dashboard content.
- The dashboard does not render lifecycle-statistic cards. Lifecycle filtering
  for `pending`, `created`, `running`, `finished`, and `failed` belongs to
  `Tasks`; cancellation is a control, never a lifecycle status.
- Week is the default. It displays `Week NN, YYYY`, uses an ISO key
  `YYYY-Www`, renders seven ordered Monday-through-Sunday buckets including
  zero days, and permits navigation to earlier weeks. Next is disabled for the
  current week so future weeks cannot be opened.
- Month always defaults to the current calendar month, displays every calendar
  day including zero days, and permits navigation to earlier months. Next is
  disabled for the current month.
- Dashboard data remains live: same-team task ingestion and lifecycle updates
  refresh counts and the applicable bucket exactly once.

### Tasks and task details

- `Tasks` provides search, status, period, page-size
  `10 | 25 | 50 | 100` (default `10`), reset, deterministic pagination, a
  desktop table, and equivalent mobile cards. It has no create-task action.
- The table contains only task id, `task_type_id`, status, Executor, time, and
  duration. Each row/card opens task details.
- Task details place `Cancel task` immediately beside the status when allowed.
  Confirmation explains asynchronous cancellation; after acceptance the
  button is disabled while the system waits for the result. The request and
  result appear in the execution timeline.
- Metadata includes id, `task_type_id`, source ids, linked Executor, created and
  finished times. Launch parameters list all resolved ordinary values and only
  redacted logical-secret references.
- The execution timeline follows `created → running → finished | failed`.
  Pending has no fabricated event before claim.
- `Live log` is an automatic read-only stream of the OpenHands agent's work and
  explicitly emitted reasoning output. It has no start, stop, clear, input, or
  other operator controls and reconnects from its last cursor.

### Executors

- The inventory provides search, activity filter, reset, deterministic
  pagination, and columns for id, concrete type, scope, tag, activity, and
  available capacity.
- Executor detail shows those canonical parameters, capacity, Executor events,
  and a filterable/paginated collection of assigned tasks. Task links return to
  task details; the Executor link on task details opens this screen.

### Parameters

- The page title is `Launch parameters`; its menu label is `Parameters`.
  Actions are `Add variable` and `Add secret` and are not placed on Overview.
- Scope filtering includes read-only `Global`, editable `Team`, and editable
  `Task category`. The page contains no explanatory paragraph beginning
  `Global is administrator-managed...`.
- A create form requires an operator-entered env-style `Key name`. For `Task
  category`, a required selector chooses one of the source-registered
  `task_type_id` values; the UI has no task-category CRUD.
- Ordinary values are visible and editable. Secret values are accepted only
  for create or replace, are never displayed again, and are always redacted.
- The single removal action for a variable, secret, or definition is named
  `Delete`. There is no separate `Revoke` action. Immutable change history and
  secret versions remain accessible after deletion.

### Audit

- `Audit` provides combined action/actor/resource search, reset, deterministic
  pagination, and page sizes `10 | 25 | 50 | 100` with default `10`.
- The table exposes only time, action, actor, resource, and outcome. Secret
  plaintext and implementation-only fields never appear.

## Runtime boundary

- Web UI uses one configurable adapter origin for HTTP and WebSocket traffic.
- At this stage the adapter is a test-only mocked proxy under `qa-e2e/`.
- No production API Gateway, authentication, Keycloak group lookup, membership
  lookup, or group-to-team mapping is implemented by v0006.
- State Registry owns canonical tasks, Executors, launch parameters, secrets,
  histories, controls, logs, streams, and audit state.

## Selected team

- Mocked proxy test configuration supplies `GET /ui/v1/teams` as a list of
  stable `team_id` and display-only `team_name` pairs.
- The list is already prepared for the user; its discovery and authorization
  are outside scope.
- UI always has exactly one selected team when the list is non-empty.
- All team-scoped API paths use
  `/ui/v1/teams/{selected_team_id}/...`.
- Selecting another team cancels or invalidates in-flight reads, closes the
  old live subscription, clears team-scoped presentation state, and reloads
  every page and subscription with the new `team_id`.
- A response or frame tagged by the old selection generation is ignored.

## API contract

- `specs/state-registry/openapi/ui-operator.openapi.yaml` is the exact v0006
  UI/State Registry wire contract.
- Collections use opaque cursors and `limit ∈ {10,25,50,100}`, default `10`.
- State Registry applies path `team_id` before lookup, filters, counts,
  pagination, mutation, history, or streaming.
- Cross-team resource identifiers return the same non-revealing not-found
  response as unknown resources.
- The v0006 `team_id` parameter is not an authentication or membership proof;
  v0007 replaces this temporary boundary with authenticated context.

## Mocked proxy modes

- Fixture mode serves deterministic presentation datasets declared by a test.
- Integration mode forwards UI API operations to real State Registry without
  owning or transforming canonical state.
- Mocked proxy exposes no admin, task-ingestion, Executor, or arbitrary-target
  browser routes.

## Launch parameters and secrets

- Team and task-type scopes are editable; global is read-only.
- Both ordinary variables and secrets require an operator-entered env-style
  key. Secret plaintext is accepted only for create/replace and never returned.
- Create, replace, and delete originate through visible UI controls in their
  E2E cases. Direct State Registry access is assertion-only. Deleting a secret
  performs the backend removal previously described as revocation; the UI has
  no separate revoke operation or label.
- Immutable definition revisions and secret-version metadata remain readable
  after removal; later task resolutions omit deleted keys.

## Live state

- Dashboard, task/control/log, Executor, launch-parameter, and audit changes
  arrive through the selected-team stream.
- Task live log has automatic follow/reconnect and no interactive controls.
- Task cancellation stays a control/event flow and never creates a lifecycle
  state outside `pending`, `created`, `running`, `finished`, or `failed`.
