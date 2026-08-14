## Why

FlowAI needs a concrete service that receives configured GitLab events and turns eligible events or tasks into durable State Registry tasks. This integration must be isolated from Jira and scheduled execution so it can have its own inbound security boundary, deployment lifecycle, mappings, and delivery semantics.

## Dependencies

- `v0013-add-task-sources-interface` SHALL be implemented, verified, accepted, and synced before implementation of this GitLab Webhook Listener begins.
- GitLab Webhook Listener SHALL implement the task-source plugin contract, register as a plugin, and report the source metadata, concise webhook-filter summary, health/activity, capabilities, and statistics required by the canonical Task Sources interface; the detailed reporting contract is deferred to design.

## What Changes

- Add a standalone `gitlab-webhook-listener` service under `svc/gitlab-webhook-listener/` with no shared code from other services.
- Add a service-owned task-source plugin manifest with plugin type `task_source_gitlab_webhook`; register every instance in State Registry before accepting task-producing webhook deliveries.
- Accept GitLab webhooks on a dedicated inbound HTTP endpoint and verify that every delivery came from the configured GitLab source before reading or persisting its payload.
- Bind each service instance to one immutable FlowAI `team_id`, one registered `source_system_id`, one listener identity, and explicitly configured GitLab projects/groups and event types.
- Map eligible GitLab webhook events to pre-registered FlowAI `task_type_id` values and persist them through the existing State Registry listener-ingestion contract before returning successful acknowledgement to GitLab.
- Use a stable GitLab delivery/event identifier as `source_id` so State Registry deduplicates retries without creating duplicate tasks or lifecycle events.
- Reject unsupported, malformed, oversized, unauthorized, or replay-tampered deliveries without creating tasks; bound concurrency and apply backpressure.
- Expose probes and safe operational telemetry without logging webhook secrets, authorization values, source payloads, or task payloads.
- Define detailed GitLab versions, webhook event families, signature/secret mechanism, payload mappings, retry status codes, and size limits during detailed design.

## Capabilities

### New Capabilities

- `gitlab-webhook-listener`: Future concrete task-source plugin service for registration, authenticated GitLab webhook intake, configured event-to-task mapping, durable State Registry ingestion before acknowledgement, replay safety, backpressure, probes, and sensitive-data handling.

### Modified Capabilities

None at backlog stage. Detailed design will confirm whether the existing State Registry listener-ingestion contract is sufficient without modification.

## Impact

- New independent service directory: `svc/gitlab-webhook-listener/`.
- Service-owned plugin manifest and duplicated task-source plugin wire types; no shared plugin SDK/package.
- Future cross-service suite: `qa-e2e/gitlab-webhook-listener/` and implementation-active `v0015.*` test definitions after detailed design starts.
- External dependency: configured GitLab deployment and webhook delivery mechanism; supported GitLab versions are intentionally deferred.
- Existing dependency: State Registry, a pre-registered team/source system/listener identity, and pre-registered task types.
- New externally reachable webhook endpoint with authentication, payload-size, replay, timeout, and rate-control requirements.

## Out of Scope

- Polling GitLab, mutating GitLab resources, or synchronizing FlowAI task state back to GitLab.
- Creating teams, source systems, task types, projects, repositories, issues, merge requests, or pipelines.
- One listener identity spanning several FlowAI teams or source systems.
- Task routing, claiming, or execution outside State Registry and Executors.
