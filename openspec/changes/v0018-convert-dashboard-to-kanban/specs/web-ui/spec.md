## MODIFIED Requirements

### Requirement: Web UI renders an actionable task dashboard

The Web UI SHALL expose separate `Dashboard` and `Statistics` navigation
items. It SHALL render `/dashboard` as a selected-team Kanban board organized
by canonical task `current_state` and SHALL NOT render the week/month calendar
or launch chart on that route. It SHALL move that existing calendar surface to
`/statistics` without removing its behavior. The board SHALL render one ordered
column for every lifecycle state supported by the active task contract; the
initial order SHALL be `pending`, `created`, `running`, `finished`, `failed`.
Each task SHALL be keyed by immutable `task_id`, SHALL appear in exactly one
status column, and SHALL display only canonical data returned through the
configured adapter/API Gateway. The Web UI SHALL NOT infer state from elapsed
time, Executor health, logs, or local interaction.

Each column SHALL show the canonical count for its status and SHALL load a
bounded first page of 25 cards in deterministic newest-activity order. Each
column SHALL expose independent cursor-based progressive loading and its own
loading, empty, and recoverable error state. Loading one column SHALL NOT reset
or reorder another column. A card SHALL identify the task, task type, current
state, assigned Executor when present, and the most relevant canonical
ingestion/start/completion time available. Every card SHALL be a
keyboard-operable link to selected-team task detail. Task History SHALL remain
the searchable, filterable historical surface.

The Kanban board SHALL be lifecycle-read-only. It SHALL provide no drag handle,
drop target, inline state selector, or generic task-state mutation request.
Existing explicit authorized controls SHALL remain on their established
surfaces and State Registry SHALL remain the sole lifecycle authority.

Before retrieving the initial board snapshot, the Web UI SHALL establish the
team stream boundary used as `after`. It SHALL then retrieve the selected-team
status pages and counts and subscribe through the existing Gateway-mediated
WebSocket from that boundary. It SHALL apply replay and live frames in cursor
order, ignore an already-applied cursor, and insert, update, or move a card
atomically by `task_id`. A committed task ingestion or lifecycle change SHALL
update the applicable counts and loaded cards without a manual reload and
without displaying one task in two columns.

On a resumable disconnect, the Web UI SHALL reconnect after the last applied
cursor. When continuity cannot be proved, it SHALL discard the stale
projection and rebuild it from a new boundary and snapshot. Browser
Back/Forward and reload SHALL rebuild the same selected-team projection.
Changing team SHALL abort or ignore the previous team's in-flight requests and
frames, clear its cards and cursor, and create a new projection before showing
the new team's tasks. No foreign-team task or count SHALL be exposed.

The board SHALL preserve lifecycle column order at every viewport width. On a
narrow viewport it SHALL provide horizontal column navigation without forcing
cards below a usable width. Column headings SHALL expose the status name and
count to assistive technology; card focus order SHALL be deterministic; live
movement SHALL be announced politely without stealing focus; and update
animation SHALL respect reduced-motion preferences.

The Statistics surface SHALL preserve the former dashboard's selected-team
week/month task statistics, totals, lifecycle status counts, launch chart,
calendar navigation, URL serialization, browser Back/Forward restoration,
zero-count daily buckets, keyboard-operable date drill-down into Task History,
and exactly-once same-team live updates. It SHALL continue to use the existing
calendar dashboard API contract and SHALL expose no foreign-team update. Only
the active Dashboard or Statistics route SHALL retain its live subscription;
route navigation SHALL close or generation-fence the inactive projection.

#### Scenario: Operator opens the task Kanban

- **WHEN** the selected team has tasks in each canonical lifecycle state and the operator opens `/dashboard`
- **THEN** the Web UI renders ordered `pending`, `created`, `running`, `finished`, and `failed` columns with the canonical counts and each task card exactly once in its current-state column

#### Scenario: A task advances while the board is open

- **WHEN** a same-team task commits a newer canonical lifecycle state and the corresponding frame is delivered live and then replayed
- **THEN** the Web UI moves the card once from its prior column to its new column, adjusts both counts once, preserves the card link, and performs no lifecycle mutation request

#### Scenario: The stream reconnects after an interruption

- **WHEN** the WebSocket disconnects after one cursor and commits occur before reconnection
- **THEN** the Web UI resumes after the last applied cursor or rebuilds from a new boundary when continuity cannot be proved, and the reconciled board contains every selected-team task exactly once

#### Scenario: Operator changes the selected team

- **WHEN** the operator changes team while prior-team snapshot requests or stream frames remain in flight
- **THEN** the Web UI ignores the stale generation, clears the prior projection, and displays only cards and counts authorized for the newly selected immutable `team_id`

#### Scenario: Operator uses a narrow viewport and keyboard

- **WHEN** the operator traverses the Kanban at a narrow viewport with a keyboard and reduced motion enabled
- **THEN** lifecycle order remains apparent, every card link is reachable in deterministic order, status names and counts are announced, horizontal navigation remains usable, and no movement animation is required

#### Scenario: Operator opens Statistics

- **WHEN** the operator activates the `Statistics` navigation item
- **THEN** the Web UI opens `/statistics` with the former dashboard's week/month calendar, totals, status counts, chart, URL and browser-history behavior, live updates, and date drill-down intact, while `/dashboard` remains the Kanban route
