## Why

FlowAI needs a concrete service that starts previously defined tasks on a recurring schedule without requiring an external Jira/GitLab event or a human action. Scheduling must be isolated in its own service so cron evaluation, occurrence deduplication, clock behavior, and high-availability rules do not leak into Executors or external listeners.

## Dependencies

- `v0013-add-task-sources-interface` SHALL be implemented, verified, accepted, and synced before implementation of this Cron Task Scheduler begins.
- Cron Task Scheduler SHALL implement the task-source plugin contract, register as a plugin, and report the source metadata, concise schedule/filter summary, health/activity, capabilities, and statistics required by the canonical Task Sources interface; the detailed reporting contract is deferred to design.

## What Changes

- Add a standalone `cron-task-scheduler` service under `svc/cron-task-scheduler/` with no shared code from other services.
- Add a service-owned task-source plugin manifest with plugin type `task_source_cron`; register every instance in State Registry before materializing scheduled task occurrences.
- Read pre-created, team-owned scheduled-task definitions and evaluate their cron expressions in an explicitly configured time zone.
- Create one durable State Registry task for every due occurrence before marking that occurrence complete.
- Give every scheduled occurrence a stable idempotency identity derived from the immutable schedule identity and scheduled instant so restarts or multiple scheduler replicas cannot create duplicate canonical tasks.
- Preserve the schedule's immutable `team_id`, registered `source_system_id`, required `task_type_id`, task payload/template reference, and optional environment/image inputs when creating an occurrence.
- Define explicit behavior for missed runs, pauses, updates, deletion, daylight-saving transitions, clock skew, concurrent replicas, and invalid cron expressions during detailed design.
- Expose probes and safe operational telemetry for schedule lag, due/created/failed occurrences, retries, and leadership/claim state without logging task payloads or credentials.
- Keep task claiming and execution in State Registry and Executors; the scheduler only materializes due task occurrences.

## Capabilities

### New Capabilities

- `cron-task-scheduler`: Future concrete task-source plugin service for registration, discovering pre-created schedules, deterministic cron evaluation, durable occurrence creation, replay/concurrency safety, missed-run policy, probes, and sensitive-data handling.

### Modified Capabilities

None at backlog stage. Detailed design will determine the required State Registry deltas before implementation preparation.

## Impact

- New independent service directory: `svc/cron-task-scheduler/`.
- Service-owned plugin manifest and duplicated task-source plugin wire types; no shared plugin SDK/package.
- Future cross-service suite: `qa-e2e/cron-task-scheduler/` and implementation-active `v0016.*` test definitions after detailed design starts.
- Future State Registry API/schema work for schedule definitions, due-occurrence coordination, and immutable occurrence identity.
- Deployment configuration for scheduler identity, State Registry origin, polling/evaluation interval, time-zone policy, retry limits, and replica coordination.

## Out of Scope

- Executing tasks inside the scheduler or bypassing Executor discovery/claim.
- Using operating-system crontab or an external scheduler as canonical FlowAI schedule state.
- Jira/GitLab event intake, task routing, or team/task-type administration by the scheduler.
- Final cron dialect, missed-run policy, or schedule-management UI/API before detailed design.
