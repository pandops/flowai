## ADDED Requirements

### Requirement: Web UI is a standalone repository service

The Web UI SHALL be a self-contained runtime service under `svc/web-ui/`.
Its command, process configuration, HTTP serving code, static browser
artifact, and service-level tests SHALL live inside that directory and SHALL
NOT import code from another service. The service SHALL embed its browser
artifact in the executable, serve it from `/`, and expose `GET /healthz` as a
process-health endpoint. The service SHALL own a production `Containerfile`,
and every integration or E2E environment that starts Web UI SHALL build and
run that image rather than a host binary or in-process HTTP server.
Cross-service Playwright tests SHALL remain under the root `qa-e2e/` tree and
SHALL interact only through public HTTP and WebSocket surfaces.

#### Scenario: Web UI starts independently

- **WHEN** the `svc/web-ui/cmd/web-ui` executable starts with valid bind configuration
- **THEN** it serves the embedded operator interface from `/` and returns HTTP 200 from `GET /healthz` without importing or starting State Registry code

#### Scenario: E2E starts the Web UI image

- **WHEN** a cross-service E2E scenario requires the Web UI runtime
- **THEN** the harness builds `svc/web-ui/Containerfile`, starts that image, and reaches the browser artifact and `GET /healthz` through the container's published HTTP port

#### Scenario: Web UI receives an invalid bind port

- **WHEN** `WEB_UI_BIND_PORT` is not an integer from 0 through 65535
- **THEN** the Web UI process rejects configuration before opening its listener

### Requirement: Web UI renders one selected-team operator surface

The Web UI SHALL render task lists, task detail views with live log streams,
control requests, and team-owned environment and secret configuration for
exactly one currently selected team. It SHALL receive a list of available
`team_id` and display-name pairs from its configured adapter, render that list
as a selector, require one selected team whenever the list is non-empty, and
use `team_name` only for presentation. It SHALL NOT discover memberships,
derive teams from Keycloak groups, administer teams, register source systems
or task types, or expose global admin projections.

#### Scenario: Operator opens the selected-team dashboard

- **WHEN** an operator opens the Web UI and the adapter supplies one or more teams
- **THEN** the Web UI selects exactly one team, labels every view with it, and offers the supplied teams in a selector without team-administration controls

#### Scenario: Operator selects another supplied team

- **WHEN** the operator selects another team from the supplied list
- **THEN** the Web UI replaces the active context with that team's `team_id`, cancels or ignores stale requests and subscriptions, and reloads every team-scoped view and live subscription for the new team

### Requirement: Configured adapter is the only browser backend

The Web UI SHALL send all backend HTTP and WebSocket traffic through one configured adapter origin and SHALL NOT configure or call any State Registry, Executor, or other backend service origin. In v0006 E2E this adapter SHALL be the test-only mocked proxy; the requirement SHALL NOT imply a production API Gateway exists.

#### Scenario: Operator reads task history

- **WHEN** an operator opens task history in the Web UI
- **THEN** the Web UI sends the request only to the configured mocked-proxy origin and makes no direct request to State Registry or an Executor

### Requirement: Web UI does not create tasks

The Web UI SHALL provide no create-task, launch-task, clone-task,
retry-as-new, or bulk-ingestion action. It SHALL observe existing tasks and
submit only supported controls against them. Empty task states SHALL explain
that tasks arrive from configured automation or listeners and SHALL NOT render
a creation form or CTA.

#### Scenario: Operator opens an empty task history

- **WHEN** the selected team has no tasks
- **THEN** the Web UI renders an informational empty state without any task creation, launch, clone, or retry-as-new action

### Requirement: Operator actions carry the selected team identifier

The Web UI SHALL use configured-adapter routes for operator state reads,
team-owned environment writes, secret writes, and task intervention requests
without implementing login or token handling in this change. Every
team-scoped REST request SHALL carry the currently selected `team_id` in the
documented request field, and every live subscription SHALL subscribe with
that same `team_id`. It SHALL NOT call or expose `/admin/teams`, `/admin/source-systems`,
`/admin/task-types`, `/admin/tags`, `/admin/tasks`, or listener task-ingestion
routes.

#### Scenario: Operator cannot access system-administrator APIs

- **WHEN** an operator inspects or uses the no-auth bootstrap Web UI
- **THEN** no system-administrator registration or global projection control
  is rendered and no browser request targets an `/admin/*` route

#### Scenario: Operator stores executor environment data

- **WHEN** an operator submits executor environment or secret data from the Web UI
- **THEN** the Web UI sends the write through the configured adapter with the currently selected `team_id`

### Requirement: Web UI does not assert authentication context

The Web UI SHALL NOT generate or assert an operator identity, bearer token,
Keycloak group, membership proof, trusted internal header, or request ID. The
selected `team_id` is an ordinary v0006 API parameter and SHALL NOT be
described as authenticated or trusted. Browser-visible team names remain
display-only.

#### Scenario: Browser request is created

- **WHEN** the Web UI creates a REST request or WebSocket upgrade
- **THEN** it sends no internal identity or authentication headers and sends only the selected `team_id` in the documented API field

#### Scenario: Web UI does not invent an operator

- **WHEN** the operator inspects the Web UI surface and source
- **THEN** the Web UI does not display, expose, log, or otherwise assert an operator identity

### Requirement: Client state is non-authoritative

The Web UI SHALL display state received through the configured adapter, keep only transient presentation state, and SHALL NOT persist canonical task, event, audit, team, routing, or secret data.

#### Scenario: UI refreshes task detail

- **WHEN** the task detail page is refreshed
- **THEN** the Web UI rebuilds the selected-team view from configured-adapter responses rather than a local authoritative store

### Requirement: Web UI manages scoped launch parameters

The Web UI SHALL label operator-managed environment definitions as
`Launch parameters`. It SHALL let an operator create, read, replace, and
delete team- and task-type-scoped launch parameters through the configured
adapter. Each definition MAY contain ordinary env variables, logical secret
references, and an optional image override. The UI SHALL explain that a team
scope applies to every launch in the configured team and that task-type scope
overrides same-named values from broader scopes. It SHALL NOT render the
removed prototype notice beginning `Global is administrator-managed`, offer task- or
global-scope mutation or expose secret plaintext. Every create form for an
ordinary env variable or logical secret SHALL contain a required editable
field labeled `Key name`. The operator, not the UI, supplies this env-style
key. The UI SHALL validate it before submission, SHALL NOT silently generate
or rename it, and SHALL submit the exact accepted key together with the
ordinary value or secret value.

#### Scenario: Operator creates team-wide launch parameters

- **WHEN** an operator creates ordinary env variables and secrets with team scope
- **THEN** the Web UI submits the currently selected `team_id` and renders the stored definition without secret plaintext

#### Scenario: Operator supplies the key name

- **WHEN** an operator opens the create-variable or create-secret form
- **THEN** the Web UI requires an editable `Key name` value, validates it before any request, and submits that exact key without generating or renaming it

#### Scenario: Operator binds parameters to a task type

- **WHEN** an operator selects task-type scope and supplies a same-team `task_type_id`
- **THEN** the Web UI submits that binding through the configured adapter with the selected `team_id` and renders the selected task category on the stored definition

#### Scenario: Operator configures an image override

- **WHEN** an operator supplies a valid image in task-type- or team-scoped launch parameters
- **THEN** the Web UI displays the selected scope and image as an override while retaining the required team image as the final fallback

#### Scenario: Operator deletes an env variable or secret

- **WHEN** an operator confirms deletion of an ordinary env variable or logical secret
- **THEN** the Web UI waits for the adapter mutation to succeed, removes the item from the current launch-parameter projection, retains its immutable history, and does not show the removed item again after reload

### Requirement: Web UI renders launch-parameter revision history

The Web UI SHALL let an operator read immutable revision history for same-team
launch parameters through the configured adapter. It SHALL derive the history of an
ordinary env variable from revisions of its parent definition and SHALL label
this as `Change history`. Secret history SHALL instead use immutable secret
versions and SHALL never display secret values. The Web UI SHALL NOT invent a
per-variable version endpoint.

#### Scenario: Operator opens ordinary env history

- **WHEN** an operator opens the history of an ordinary env variable
- **THEN** the Web UI reads parent launch-parameter revisions through the configured adapter, shows deterministic revisions, and exposes no secret material

### Requirement: Web UI renders task controls in the task event feed

The Web UI SHALL add an accepted cancellation request to the task feed as a
control event, disable cancellation while its control is pending, and render
later acknowledgement, completion, or failure events received through the API
configured adapter. It SHALL distinguish control events from lifecycle events and SHALL
NOT project a cancellation control status as a task lifecycle state. After
refresh, it SHALL rebuild the feed from task lifecycle history,
control-event history, and the current control projection.

#### Scenario: Operator cancels a running task

- **WHEN** an operator submits cancellation and the assigned Executor later reports its result
- **THEN** the feed shows the requested event and each later control result in order, cancellation remains disabled while pending, and no canceled task lifecycle state appears

#### Scenario: Cancellation processing fails

- **WHEN** the assigned Executor reports a failed control event
- **THEN** the feed shows the failure as a control result and derives retry availability from the returned control representation

### Requirement: Web UI renders a live task execution log

The task detail view SHALL render the assigned Executor's output as a live
streaming log rather than as an Executor-event list. The log SHALL support
visually distinct `work` and agent-published `reasoning` streams,
ordered replay followed by live continuation, automatic following of the
newest line, automatic cursor-based reconnect, reconnecting and error states,
and an empty state before output exists. Lifecycle and task-control
events SHALL remain in their separate task timeline and SHALL NOT be rendered
as log lines. The log SHALL be read-only and SHALL NOT provide text input,
command execution, buttons, links, menus, copy, manual follow controls, manual
reconnect, clear, truncate, delete, or other interactive or content-mutation
actions.
The UI SHALL label `reasoning` as agent-published output and SHALL NOT claim
to expose hidden model reasoning.

#### Scenario: Running task emits output

- **WHEN** ordered log chunks arrive for the selected running task
- **THEN** the Web UI appends their lines without reordering or duplication and automatically follows the newest line

#### Scenario: Live connection reconnects

- **WHEN** the task log connection is interrupted after a committed cursor
- **THEN** the Web UI shows reconnecting state, automatically resumes from that cursor without an operator action, and does not duplicate already rendered lines

### Requirement: Web UI shows canonical task source identifiers

The Web UI SHALL display a task's `source_system_id`, external `source_id`,
and `task_type_id` as canonical metadata returned through the configured adapter.
Because v0006 has no operator source-system or task-type catalog read, the Web
UI SHALL NOT invent display names or links for those identifiers.

#### Scenario: Operator opens task details

- **WHEN** the configured adapter returns a task carrying source-system, external-source, and task-type identifiers
- **THEN** the Web UI displays those identifiers as read-only metadata without calling an admin route or inventing catalog metadata

### Requirement: Web UI renders an actionable task dashboard

The Web UI SHALL render selected-team task statistics for week and month
periods and SHALL NOT render a day-period control. Week SHALL default to the
current local ISO week, display `Week NN, YYYY`, allow navigation to earlier
ISO weeks, serialize the selection as `week=YYYY-Www`, and render exactly seven
ordered Monday-through-Sunday buckets including zero-count days. Month SHALL
default to the current calendar month, allow navigation to earlier months, and
render every calendar day including zero-count days. Forward navigation SHALL
be disabled at the current week or month. Browser Back and Forward SHALL
restore period and calendar selection. The dashboard SHALL NOT render
lifecycle-status statistic cards; `pending`, `created`, `running`, `finished`,
and `failed` filtering belongs to task history. It SHALL NOT represent
cancellation as a lifecycle state and SHALL use a subdued non-primary series
color for the launch chart. While the
dashboard is open, committed same-team task ingestion and lifecycle changes
SHALL update the applicable period totals, status counts, and chart without a
manual reload, without double-counting replayed frames, and without exposing
foreign-team changes.

Every rendered daily bucket SHALL be a keyboard-operable control. Activating
it SHALL open task history with the exact bucket date serialized as
`date=YYYY-MM-DD`; the history request and visible Date filter SHALL use that
same value. Reload and browser Back/Forward SHALL preserve the date-filtered
history and the originating dashboard calendar respectively.

#### Scenario: New task updates the open dashboard

- **WHEN** a same-team task is durably ingested inside the selected period
- **THEN** the dashboard adds that task exactly once to the total, `pending` count, and applicable chart bucket without a manual reload

#### Scenario: Operator opens an earlier ISO week

- **WHEN** the operator activates the previous-week arrow from the current week
- **THEN** the dashboard URL contains the earlier `YYYY-Www` key, the label shows its ISO week number and week-year, all seven Monday-through-Sunday buckets render in order, and browser Back restores the current week

#### Scenario: Operator opens an earlier calendar month

- **WHEN** the operator activates the previous-month arrow from the current month
- **THEN** the dashboard renders every day of that earlier calendar month including zero-count days and browser Back restores the current month

#### Scenario: Operator opens one dashboard day

- **WHEN** the operator activates the dashboard bucket for `2026-08-15`
- **THEN** task history opens with `date=2026-08-15` in the URL and visible Date control and lists only tasks ingested in that UTC calendar day

### Requirement: Web UI renders searchable paginated task history

The Web UI SHALL render selected-team task history with search, lifecycle
status, rolling period, and exact UTC calendar-date filters. It SHALL provide page sizes `10`, `25`, `50`, and
`100`, default to `10`, preserve deterministic ordering while paging, expose
loading, empty, and recoverable error states, and render a mobile card
composition instead of forcing the desktop table outside the viewport. The
history SHALL provide no task-creation action and each result SHALL navigate
to its task detail.

The desktop history table SHALL contain only operator-useful columns: task
identifier, task type identifier, lifecycle status, assigned Executor, start
or ingestion time, and duration or completion time when available. It SHALL
NOT render internal ownership fields, source-system deduplication fields,
request identifiers, cursors, scope tokens, or duplicate team metadata as
table columns. Such canonical metadata MAY remain on task detail when the
operator needs it there.

#### Scenario: Operator filters and pages task history

- **WHEN** the operator applies search, status, and period filters and selects a page size
- **THEN** the result set, count, pagination controls, URL state, and mobile or desktop representation consistently describe the same selected-team query

### Requirement: Web UI renders task detail and executor navigation

The Web UI SHALL render canonical task metadata, current lifecycle status,
the lifecycle order `created → running → finished | failed`, all resolved
ordinary env variable names and values, logical secret references without
secret plaintext, and the assigned Executor as a link to that Executor's
detail. A pending task SHALL show no fabricated lifecycle event before a
successful claim. Cancellation SHALL be a button adjacent to the current
status when permitted, SHALL require confirmation, and SHALL be disabled with
an explanatory pending state after acceptance; it SHALL NOT occupy a separate
task-detail block.

#### Scenario: Operator follows the assigned Executor

- **WHEN** the operator opens an assigned task and activates its Executor link
- **THEN** the Web UI opens that Executor's selected-team detail without losing the originating task's identity or exposing secret plaintext

### Requirement: Web UI renders Executor inventory and detail

The Web UI SHALL provide an `Executors` navigation item, a selected-team
Executor inventory, and an Executor detail view containing canonical identity,
type, ownership scope, authorized tag, health or activity projection, Executor
events, and tasks currently assigned to it. System-scoped Executors visible to
the configured team SHALL remain read-only and the UI SHALL NOT provide
registration or lifecycle-management actions.

The Executor inventory table SHALL contain only Executor identifier, concrete
type, ownership scope, authorized tag, current activity, and available
capacity. It SHALL NOT add internal timestamps, team identifiers, protocol
fields, or repeated detail-only values as columns.

#### Scenario: Operator opens an Executor from the inventory

- **WHEN** the operator selects an Executor
- **THEN** its detail shows its parameters, events, and currently running tasks and each task links back to task detail

### Requirement: Web UI renders team audit history

The Web UI SHALL render the configured team's audit history with action and
actor search or filters, deterministic pagination, page sizes `10`, `25`,
`50`, and `100` defaulting to `10`, and a download action only when supported
by the available backend contract. Audit details SHALL contain no secret plaintext and
the UI SHALL expose no global or foreign-team audit projection.

The audit table SHALL contain only occurrence time, action, actor, affected
resource, and outcome. Request identifiers and safe structured details SHALL be
available from the entry detail, not as permanent table columns. Internal
database identifiers and cryptographic metadata SHALL not be rendered.

#### Scenario: Operator inspects an audited parameter change

- **WHEN** the operator filters audit history for a launch-parameter mutation
- **THEN** matching same-team entries retain deterministic order and actor/request attribution without revealing current or historical secret values

### Requirement: Web UI tables expose only actionable fields

Every desktop data table SHALL have an explicit, view-specific column set and
SHALL omit fields that are unused in that view. The Web UI SHALL NOT turn an
API response object into columns automatically. Pagination cursors, scope
tokens, immutable ownership identifiers already represented by the selected
team, transport metadata, and implementation-only identifiers SHALL never be
visible columns. The equivalent mobile cards SHALL contain the same useful
information without empty labels or placeholder rows for omitted values.

#### Scenario: API response contains additional fields

- **WHEN** a collection response contains documented or forward-compatible fields that are not part of the view's explicit column set
- **THEN** the Web UI ignores those fields in the table and mobile cards while preserving navigation and supported detail metadata

### Requirement: Web UI is responsive and keyboard operable

The Web UI SHALL provide desktop, tablet, and mobile layouts for every
operator screen. Interactive controls SHALL expose discernible hover,
`focus-visible`, active, disabled, and loading states; dialogs SHALL contain
focus while open and restore focus on close; labels, roles, accessible names,
status announcements, and contrast SHALL support keyboard and assistive
technology operation.

#### Scenario: Keyboard operator uses the mobile task flow

- **WHEN** the viewport is mobile-sized and the operator navigates from history to task detail and opens then dismisses cancellation confirmation using only the keyboard
- **THEN** no content is clipped horizontally, focus order follows the visual flow, the dialog manages focus correctly, and the destructive action is not submitted

### Requirement: Web UI exposes consistent asynchronous and validation states

Every dashboard, collection, detail, live stream, form, and dialog SHALL
provide a non-destructive loading state, an informative empty state, a
recoverable error state where retry is possible, field-level validation, and
disabled submit behavior while invalid or pending. Errors SHALL preserve
non-secret operator input where safe and SHALL NOT expose secret plaintext,
trusted headers, stack traces, or foreign-resource existence.

#### Scenario: Launch-parameter submission fails validation and then transport

- **WHEN** the operator first submits an invalid definition and then a valid definition receives a recoverable backend error
- **THEN** field-level guidance appears before any request, the pending submission cannot be repeated, the recoverable failure preserves safe non-secret input, and no secret value is redisplayed

### Requirement: Web UI filters every filterable view consistently

The Web UI SHALL give every dashboard period selector and every filter exposed
by task history, Executor inventory, launch parameters, revision or version
history, team audit history, and an Executor's active-task collection a stable
accessible name and a stable semantic filter key. Each filter SHALL work alone
and in combination with the other filters on its view, SHALL support explicit
reset, SHALL produce an informative no-results state, and SHALL remain
consistent with pagination. Shareable filter state SHALL be represented in the
URL and restored after reload and browser navigation; view-local presentation
state SHALL reset predictably when the operator leaves its owning context.
Desktop and mobile compositions SHALL apply the same query semantics.

#### Scenario: Operator combines and resets filters

- **WHEN** the operator applies every available filter alone and then applies a valid combination on each filterable view
- **THEN** visible results, counts, pagination, URL state, reload, and mobile representation agree with the same query, and reset restores the documented default result set
