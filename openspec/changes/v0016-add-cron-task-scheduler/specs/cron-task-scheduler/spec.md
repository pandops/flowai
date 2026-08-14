## ADDED Requirements

### Requirement: Platform provides a standalone Cron Task Scheduler service

The platform SHALL provide a self-contained task-source plugin service under `svc/cron-task-scheduler/` with a service-owned manifest declaring plugin type `task_source_cron`. Every instance SHALL implement the versioned task-source plugin contract and register its immutable plugin/team/source-system identity and safe capabilities/filter summary with State Registry before materializing occurrences. It SHALL discover pre-created team-owned task schedules, evaluate when they are due, and persist one idempotent task occurrence through State Registry for each due scheduled instant. The cron dialect, time-zone rules, missed-run policy, schedule-management API, persistence model, and replica-coordination mechanism SHALL be selected in a later design phase. The service SHALL NOT share code with other services, materialize occurrences before successful plugin registration, execute tasks, or replace State Registry as the canonical task source.

#### Scenario: Pre-created scheduled task becomes due

- **WHEN** a valid pre-created task schedule reaches a due instant
- **THEN** Cron Task Scheduler persists exactly one canonical team-owned task occurrence through State Registry for that schedule and instant

#### Scenario: Cron plugin instance starts

- **WHEN** a configured Cron Task Scheduler instance starts with a supported plugin manifest and valid team/source-system binding
- **THEN** it registers as `task_source_cron` in State Registry before materializing any scheduled task occurrence
