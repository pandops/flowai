## Why

The current dashboard emphasizes calendar aggregates, while operators primarily need to see where every current task is in the lifecycle and observe transitions without refreshing. A live Kanban board makes queue pressure, active work, and terminal outcomes visible in one operational surface.

## What Changes

- Replace the selected-team `/dashboard` calendar/chart with a Kanban board organized by canonical task `current_state`.
- Move the existing week/month calendar, totals, status counts, chart, date drill-down, URL state, and live updates unchanged to a separate `Statistics` navigation item and `/statistics` route.
- Render one ordered column for every lifecycle state supported by the active platform contract; the initial baseline columns are `pending`, `created`, `running`, `finished`, and `failed`.
- Load bounded, cursor-paginated task cards per column from the existing team-scoped task APIs and show canonical per-state counts.
- Subscribe through the existing Gateway-mediated team WebSocket and insert or move each task card exactly once when ingestion or lifecycle events commit.
- Reconcile snapshot and replay on initial connection, reconnect, browser Back/Forward, and selected-team change without losing a commit, duplicating a card, or exposing a foreign-team task.
- Make cards keyboard-operable links to task detail and keep Task History as the searchable historical/table surface.
- Keep the Kanban lifecycle read-only: drag-and-drop SHALL NOT fabricate or request task-state transitions. Existing explicit control actions remain on their authorized surfaces.
- Provide responsive horizontal column navigation, independent column loading/error/empty states, accessible status names/counts, and reduced-motion-safe updates.
- Continue using the existing calendar dashboard API from the new Statistics surface; this change neither removes nor changes that backend contract.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `web-ui`: make the dashboard a live status-column Kanban and move the existing calendar dashboard to Statistics.

## Impact

- Affected code: `svc/web-ui/` navigation, dashboard/statistics routing and state management, and `qa-e2e/web-ui/`.
- Affected APIs: no new State Registry operation is required; the board uses existing team-scoped task list/count and live-stream contracts through the configured adapter/Gateway.
- Affected tests: new `v0018.*` browser E2E definitions; existing v0006 calendar-dashboard behavior is preserved on `/statistics`, while its route-specific runnable assertions are superseded.
- Affected diagrams: one proposed sequence diagram for snapshot, cursor handshake, live movement, and reconnect reconciliation.

## Out of Scope

- Drag-and-drop lifecycle mutation or a generic task-state update API.
- Replacing Task History, task detail, controls, logs, or audit views.
- Changing State Registry lifecycle authority or allowing Web UI to infer state from local timers.
- Changing or removing the existing dashboard-calendar backend endpoint.
