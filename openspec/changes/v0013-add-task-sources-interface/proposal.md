## Why

Before FlowAI adds concrete Jira, GitLab, or cron task-source services, the platform needs a stable plugin contract and operators need one canonical place to understand which task-source plugins are registered, how they select work, whether they are healthy, and what task types they produce. Without that prerequisite, every source would invent its own registration, administration, and observability surface.

## What Changes

- Add an authenticated system-administrator «Task Sources» item to Web UI navigation and a dedicated task-sources page.
- Define a versioned network-level task-source plugin contract and a manifest carried independently by every task-source service; do not introduce a shared Go package or in-process plugin binary.
- Require every task-source service instance to register itself as a plugin in State Registry before ingesting tasks, using an immutable plugin instance ID, plugin type/version, owning team, registered source system, capabilities, and safe display metadata.
- Show every registered task-source plugin with its plugin type/version, source kind, owning team, concise description, concise human-readable filter summary, health/activity state, capabilities, and ingestion statistics.
- Show the task types associated with each source, including canonical `task_type_id` and execution tag.
- Allow an authenticated system administrator to create a task type from the same page; State Registry remains the canonical issuer and owner of `task_type_id`.
- Route all browser traffic through API Gateway; Web UI does not call State Registry or source services directly.
- Keep plugin services independent: each duplicates its own wire types, configuration, clients, and registration code; plugins publish only the metadata/status needed by the canonical State Registry projection and do not host separate human-facing administration pages.
- Treat this change as a prerequisite for the Jira Listener, GitLab Webhook Listener, and Cron Task Scheduler changes.
- Defer exact registration/API paths, manifest schema, compatibility rules, heartbeat/lease behavior, statistics windows, filter-summary schema, permissions, pagination, and UI layout to detailed design.

## Capabilities

### New Capabilities

- `task-sources-interface`: Versioned task-source plugin contract, plugin registration/catalog in State Registry, and administrative Web UI/Gateway surface for listing plugins, filter summaries, statistics, health/activity, associated task types, and system-admin-only task-type creation.

### Modified Capabilities

None at backlog stage. Detailed design will define the required deltas to Web UI, API Gateway, State Registry, and authentication/admin routing before implementation preparation.

## Impact

- Future Web UI navigation item and dedicated task-sources page.
- Future API Gateway administrative read/create routes that preserve authenticated system-administrator identity and never expose source credentials.
- Future State Registry plugin registration/catalog, source metadata/statistics and task-type projections, plus reuse or extension of system-admin-only task-type creation.
- A versioned OpenAPI contract implemented independently by Jira Listener, GitLab Webhook Listener, Cron Task Scheduler, and later task-source plugins.
- Future E2E coverage under `qa-e2e/task-sources-interface/` after detailed design starts.

## Out of Scope

- Implementing Jira, GitLab, cron, or another concrete source service in this change.
- Loading third-party code into another service process or sharing an SDK/package between plugin services.
- Letting ordinary operators create task types or access global cross-team administration.
- Displaying or editing source credentials, webhook secrets, task payloads, secret values, or raw external filters containing sensitive data.
- Creating/updating teams or bypassing API Gateway from Web UI.
