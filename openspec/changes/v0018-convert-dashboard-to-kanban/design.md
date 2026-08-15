## Context

The current `/dashboard` is a calendar-oriented aggregate view. Operators must
switch to Task History to understand queue composition and cannot watch one
task progress across lifecycle states on the dashboard itself. The calendar
view remains useful for historical statistics, so this change relocates it to
`/statistics` rather than deleting it. State Registry
already owns canonical task state and exposes team-scoped task listing plus a
cursor-based live stream through the Web UI's configured adapter/API Gateway.

This change is presentation and client-state work. It does not add a second
lifecycle authority, a generic state-mutation endpoint, or a new backend
service.

## Goals / Non-Goals

**Goals:**

- Show the selected team's current tasks in status columns on `/dashboard`.
- Preserve the complete current calendar dashboard on `/statistics` and expose
  it through a separate `Statistics` navigation item.
- Move a task card exactly once when canonical state changes commit.
- Survive snapshot/stream overlap, replay, reconnect, navigation, and team
  changes without duplicates, omissions, or cross-team leakage.
- Keep large terminal-state collections bounded and independently pageable.
- Preserve Task History and task detail as the searchable and detailed views.
- Make the board usable with keyboard, screen reader, narrow viewport, and
  reduced-motion preferences.

**Non-Goals:**

- Drag-and-drop or any dashboard-originated lifecycle transition.
- Inferring lifecycle state from timers, logs, Executor health, or card order.
- Replacing Task History, task detail, or explicit authorized task controls.
- Changing or removing the existing calendar aggregate backend operation.

## Decisions

### 1. Canonical state is the only column key

The board renders one ordered column for each task state in the active API
contract. The initial order is `pending`, `created`, `running`, `finished`,
`failed`. A task is keyed by immutable `task_id` and can exist in exactly one
column. The client never interprets a drag, elapsed time, or an intermediate
event as authority to change that key.

Alternative considered: configurable user columns. Rejected because they
would blur canonical lifecycle state with presentation preferences and would
require persistence and mapping semantics unrelated to this change.

### 2. Read-only cards, explicit navigation

Each card is a keyboard-operable link to task detail. The board has no drag
handle and issues no lifecycle mutation request. Existing control actions stay
on their currently authorized surfaces.

Alternative considered: drag-and-drop between columns. Rejected because State
Registry accepts ordered lifecycle events from authorized platform actors; a
visual gesture must not fabricate those events.

### 3. Snapshot plus cursor stream with idempotent projection

Before snapshot retrieval, the client establishes the stream boundary used as
`after`. It fetches bounded task pages/counts for the selected team, subscribes
from that boundary, applies replay then live frames, and keys updates by
`task_id`. A frame at or before the last applied cursor is ignored. A newer
snapshot for a task replaces the prior card and atomically adjusts source and
destination counts.

On reconnect, the client resumes after the last applied cursor. If the cursor
is no longer replayable, or delivery continuity cannot be proved, it discards
the projection and repeats boundary + snapshot + subscription. This favors a
short loading state over a plausible but incomplete board.

Alternative considered: fetch snapshot and then open the stream. Rejected
because a commit between those operations can be lost.

### 4. Independent bounded columns

Each column initially requests 25 cards in deterministic newest-activity order
using existing task list filters and cursor pagination. Its header count is
the canonical total for the same status filter. “Load more” advances only that
column; loading, empty, and recoverable error states are column-local.

Alternative considered: loading every task into the browser. Rejected because
terminal columns grow without bound.

When a live update affects an unloaded task, counts still update. A newly
eligible card is inserted only if it belongs in the loaded window; otherwise
the column is marked for reconciliation. This preserves bounded memory and
honest counts.

### 5. Team changes create a new projection generation

Changing the selected team aborts outstanding list requests, closes the old
subscription, increments a client generation token, clears all cards/cursors,
and builds a new projection. Late responses or frames carrying an older
generation are ignored. Every request and accepted frame must match the
selected immutable `team_id`.

### 6. Responsive and accessible interaction

Columns remain in lifecycle order. Wide layouts show them side by side; narrow
layouts use horizontal scrolling with visible column headings and do not
compress cards below their usable width. Headings expose status and total,
cards have descriptive accessible names, focus order follows column/card
order, and live movement uses a polite announcement without stealing focus.
Motion is disabled when the user requests reduced motion.

### 7. Calendar dashboard becomes Statistics

The existing calendar composition moves intact to `/statistics`: week/month
selection, URL serialization, browser history, totals, status counts, daily
buckets, Task History date drill-down, and same-team live updates retain their
current semantics. Navigation exposes separate `Dashboard` and `Statistics`
items. `/dashboard` owns the Kanban; `/statistics` owns calendar analysis.

Alternative considered: removing the calendar UI and retaining only its API.
Rejected because period trends and date drill-down remain useful and the user
explicitly requires the current dashboard to move to Statistics.

## Risks / Trade-offs

- **High event rate can cause visual churn.** Coalesce render work while still
  applying every ordered cursor; never coalesce away the final canonical state.
- **Pagination and live movement can disagree at a page boundary.** Treat the
  server count as canonical, preserve a bounded loaded window, and reconcile
  the affected column rather than guessing its next card.
- **A long terminal history makes counts much larger than loaded cards.** Show
  both the total and explicit progressive loading so the distinction is clear.
- **Two live task projections increase browser complexity.** Only the active
  route owns a subscription and projection; route changes tear down inactive
  view state before starting the destination view.

## Migration Plan

1. Add the new browser E2E definitions and make their exact tests fail.
2. Move the current calendar composition to `/statistics`, add its navigation
   item, and preserve its behavior before changing `/dashboard`.
3. Implement the Kanban projection behind the existing `/dashboard` route.
4. Replace only route-specific v0006 runnable assertions with `v0018.*`, while
   retaining their historical test-case definitions and calendar expectations.
5. Verify snapshot, replay, reconnect, team isolation, accessibility, and
   responsive behavior against container-built services.
6. Roll back by deploying the previous Web UI image; no data or API migration
   is required.

## Open Questions

None. Any future lifecycle states are added by the canonical task contract and
then rendered as columns; this change does not define them independently.
