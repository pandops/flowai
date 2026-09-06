## Why

Before FlowAI adds concrete Jira, GitLab, or cron task-source services, the platform needs a stable plugin contract and operators need one canonical place to understand which task-source plugins are registered, how they select work, whether they are healthy, and what task types they produce. Without that prerequisite, every source would invent its own registration, administration, and observability surface.

## What Changes

- Add an authenticated team-scoped «Task Sources» item to Web UI navigation and a dedicated task-sources page.
- Define a versioned network-level task-source plugin contract and a manifest carried independently by every task-source service; do not introduce a shared Go package or in-process plugin binary.
- Require every task-source service instance to register itself as a plugin in State Registry before ingesting tasks, using an immutable plugin instance ID, plugin type/version, owning team, registered source system, capabilities, and safe display metadata.
- Show every registered task-source plugin with its plugin type/version, source kind, owning team, concise description, concise human-readable filter summary, health/activity state, capabilities, and ingestion statistics.
- Show the task types associated with each source as team-created categories, including category name, canonical `task_type_id`, execution tag, prompt template, canonical `agent_id`, canonical `model_id`, additional execution parameters, and optional task-type image override.
- Allow an authenticated operator to create a task-type category only for the operator's verified team from the same page; State Registry remains the canonical issuer of `task_type_id` and canonical owner of the team-scoped record.
- Allow the team to configure each task type's prompt template with an optional `source_id` placeholder and execution settings including canonical `agent_id`, a reference to a separately registered same-team `model_id`, and an optional validated parameter set selected during detailed design.
- Add a separate team-owned model catalog managed through Web UI and API Gateway. Task types reference catalog entries; they do not embed model credentials or accept cross-team model references.
- Remove listener-supplied per-task image overrides. Image selection has exactly two levels: an optional image on the referenced `task_type_id`, then the owning team's required default image.
- Route all browser traffic through API Gateway; Web UI does not call State Registry or source services directly.
- Keep plugin services independent: each duplicates its own wire types, configuration, clients, and registration code; plugins publish only the metadata/status needed by the canonical State Registry projection and do not host separate human-facing administration pages.
- Treat this change as a prerequisite for the Jira Listener, GitLab Webhook Listener, and Cron Task Scheduler changes.
- Defer exact registration/API paths, manifest schema, compatibility rules, heartbeat/lease behavior, statistics windows, filter-summary schema, permissions, pagination, and UI layout to detailed design.

## Capabilities

### New Capabilities

- `task-sources-interface`: Versioned task-source plugin contract, plugin registration/catalog in State Registry, and team-scoped Web UI/Gateway surface for listing plugins, filter summaries, statistics, health/activity, associated task-type categories, prompt/execution configuration, and same-team task-type creation.
- `model-catalog`: Team-owned model entries created separately by the team and referenced by task types through canonical `model_id` values without exposing credentials.

### Modified Capabilities

- `state-registry`: Team operators create same-team task-type categories and model entries; task types own prompt/execution configuration; task ingestion snapshots the rendered prompt and resolved execution settings; task ingestion no longer accepts `image`; image resolution uses only task-type override then team default.
- `web-ui`: Add team-scoped Task Sources and Models surfaces for category, prompt template, execution-parameter, and model-catalog management; at the State Registry contract level, task-type creation ceases to be system-admin-only.
- `api-gateway`: Gateway forwards verified team context for task-type category and model-catalog management.

## Impact

- Future Web UI navigation item and dedicated task-sources page.
- Future API Gateway team-scoped read/create routes that preserve verified operator and immutable team identity and never expose source credentials.
- Future State Registry plugin registration/catalog, source metadata/statistics, task-type projections, and same-team task-type category creation.
- Future State Registry team-owned model catalog, task-type prompt templates, task-type execution settings, deterministic `source_id` interpolation, and immutable per-task snapshots of the rendered prompt and resolved execution settings.
- Task ingestion contract without an `image` field and two-level image resolution: `task_types.default_image` then `teams.default_image`.
- Detailed design SHALL remove `source_systems.default_image`, remove `image` from launch-parameter definitions at every scope, remove `TaskIngestionRequest.image` and task/discovery projections of the unresolved input, narrow `image_source` to `task_type_default | team_default`, and update both concrete Executor contracts to describe the same two-level resolution.
- Detailed design SHALL replace the system-admin-only task-type creation contract across State Registry, API Gateway, Web UI, and OpenAPI with a trusted-Gateway same-team operator route whose request includes a category name and does not accept a caller-authoritative `team_id`.
- Detailed design SHALL define the prompt-template grammar with an optional supported `source_id` placeholder, escaping and validation rules, size limits, preview behavior, failure handling, and immutable rendering point; static templates without placeholders SHALL remain valid. It SHALL define the extensible execution-parameter schema beyond canonical `agent_id` and `model_id`, including required fields, defaults, compatibility validation, empty-set behavior, and what reaches each concrete Executor.
- Detailed design SHALL define team-scoped model create/list/update or versioning semantics, canonical `model_id`, safe provider/model metadata, secret references, deletion/in-use behavior, and non-revealing cross-team authorization. Model credentials and secret values SHALL remain outside task types, task payloads, browser responses, logs, and audit records.
- A versioned OpenAPI contract implemented independently by Jira Listener, GitLab Webhook Listener, Cron Task Scheduler, and later task-source plugins.
- Future E2E coverage under `qa-e2e/task-sources-interface/` after detailed design starts.
- Active changes `v0011-add-hibernate-status`, `v0016-add-cron-task-scheduler`, `v0019-add-dynamic-task-continuation`, and `v0020-add-openhands-goal-execution`, plus the `v0008-use-openbao-transit` regression boundary, must be rebased during detailed design anywhere they inherit or assert the superseded image inputs or task-type administration contract.

## Out of Scope

- Implementing Jira, GitLab, cron, or another concrete source service in this change.
- Loading third-party code into another service process or sharing an SDK/package between plugin services.
- Letting an operator create a task type for another team or access global cross-team administration.
- Displaying or editing source credentials, webhook secrets, task payloads, secret values, or raw external filters containing sensitive data.
- Embedding model credentials or secret values in task types, prompt templates, execution parameters, or model-catalog responses.
- Creating/updating teams or bypassing API Gateway from Web UI.
