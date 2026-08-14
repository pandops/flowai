## Why

FlowAI has a durable listener-ingestion contract in State Registry but no concrete service that turns Jira issues into executable platform tasks. Teams need an independently deployable Jira Listener that discovers eligible issues, persists each one through the existing team-bound State Registry API, and never loses or acknowledges work before durable ingestion succeeds.

## Dependencies

- `v0013-add-task-sources-interface` SHALL be implemented, verified, accepted, and synced before implementation of this Jira Listener begins.
- Jira Listener SHALL implement the task-source plugin contract, register as a plugin, and report the source metadata, concise filter summary, health/activity, capabilities, and statistics required by the canonical Task Sources interface; the detailed reporting contract is deferred to design.

## What Changes

- Add a self-contained `jira-listener` runtime service under `svc/jira-listener/`.
- Add a service-owned task-source plugin manifest with plugin type `task_source_jira`; register every instance in State Registry before task ingestion.
- Read eligible tasks from one configured Jira site through the Jira API; the supported Jira product/version and polling or webhook transport will be selected during detailed design.
- Bind each service instance to one immutable FlowAI `team_id`, one registered `source_system_id`, one Jira site, and an explicit Jira issue-type-to-FlowAI `task_type_id` mapping.
- Convert each eligible Jira issue into the existing State Registry listener-ingestion request, using the Jira issue ID as stable `source_id`; State Registry remains responsible for `(team_id, source_system_id, source_id)` deduplication and image resolution.
- Record source progress only after State Registry durably accepts or identifies the corresponding canonical task; retry transient Jira and State Registry failures with bounded backoff.
- Expose health/readiness probes and structured operational telemetry without logging Jira credentials, issue descriptions, task payloads, or State Registry credentials.
- Plan cross-service E2E coverage for ingestion, replay/deduplication, restart recovery, mapping failures, and upstream/downstream outages during detailed design.

## Capabilities

### New Capabilities

- `jira-listener`: Future concrete task-source plugin service for registration, Jira task discovery, deterministic mapping, durable State Registry ingestion, replay safety, retries, probes, and sensitive-data handling. Exact Jira compatibility and transport remain undecided.

### Modified Capabilities

None. The service consumes the existing State Registry listener-ingestion contract without changing it.

## Impact

- New service directory: `svc/jira-listener/`, fully self-contained under the repository's no-shared-code rule.
- Service-owned plugin manifest and duplicated task-source plugin wire types; no shared plugin SDK/package.
- Future cross-service suite: `qa-e2e/jira-listener/` plus implementation-active `v0014.*` test definitions after detailed design starts.
- External dependency: one configured Jira origin and service credential; supported Jira product/version is intentionally deferred.
- Existing dependency: State Registry listener-ingestion API, pre-registered team, source system, and task types.
- Deployment configuration will include Jira origin/credentials, source selection, retry limits, team/source-system binding, task-type mappings, State Registry origin/identity, and progress storage; exact fields are deferred.

## Out of Scope

- Creating or mutating Jira issues, comments, transitions, assignees, or fields.
- Jira webhook intake, bidirectional status synchronization, attachment download, or Jira administration.
- Creating FlowAI teams, source systems, or task types from the listener.
- One listener identity spanning multiple FlowAI teams or source systems.
- Queueing, claiming, executing, or routing tasks outside State Registry.
