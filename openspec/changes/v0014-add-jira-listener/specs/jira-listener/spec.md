## ADDED Requirements

### Requirement: Platform provides a standalone Jira Listener service

The platform SHALL provide a self-contained task-source plugin service under `svc/jira-listener/` with a service-owned manifest declaring plugin type `task_source_jira`. Every instance SHALL implement the versioned task-source plugin contract and register its immutable plugin/team/source-system identity and safe capabilities/filter summary with State Registry before ingestion. It SHALL obtain eligible tasks from a configured Jira source, map them to pre-registered FlowAI task types, and persist them through State Registry before recording successful source progress. The Jira product/version, intake transport, API contract, retry behavior, and detailed mapping SHALL be selected in a later design phase. The service SHALL NOT share code with other services, ingest before successful plugin registration, execute tasks, or create teams, source systems, or task types.

#### Scenario: Jira task becomes a durable FlowAI task

- **WHEN** the configured Jira source exposes an eligible task that maps to a pre-registered FlowAI task type
- **THEN** Jira Listener persists one team-owned canonical task through State Registry before treating the Jira task as successfully ingested

#### Scenario: Jira plugin instance starts

- **WHEN** a configured Jira Listener instance starts with a supported plugin manifest and valid team/source-system binding
- **THEN** it registers as `task_source_jira` in State Registry before requesting or ingesting eligible Jira tasks
