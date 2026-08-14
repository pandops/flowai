## ADDED Requirements

### Requirement: Platform provides one administrative Task Sources interface

Before any concrete Jira Listener, GitLab Webhook Listener, or Cron Task Scheduler is implemented, the platform SHALL provide one authenticated system-administrator task-sources page through Web UI and API Gateway. The page SHALL obtain canonical data from State Registry and SHALL display registered task-source plugins with concise plugin identity/type/version, capabilities, owning team, source system, filter summary, health/activity, ingestion statistics, and associated canonical `task_type_id` values. The same page SHALL allow only an authenticated system administrator to request creation of a task type and SHALL display the canonical `task_type_id` returned by State Registry. Web UI SHALL NOT call State Registry or plugin services directly and SHALL NOT display source credentials, secrets, raw sensitive filters, or task payloads. Exact APIs, fields, statistic windows, permissions, and presentation SHALL be selected in a later design phase.

#### Scenario: Administrator reviews sources and creates a task type

- **WHEN** an authenticated system administrator opens the Task Sources page and submits a valid task-type creation request for a registered team/source context
- **THEN** the page shows canonical source summaries, statistics, filters, and existing task types, routes the creation through API Gateway to State Registry, and displays the newly returned canonical `task_type_id`

#### Scenario: Non-admin attempts task-type creation

- **WHEN** an ordinary operator or unauthenticated user opens or calls the task-type creation surface
- **THEN** the platform rejects creation without mutating State Registry or disclosing global cross-team source data

### Requirement: Task-source services implement and register a versioned plugin contract

Every task-source service SHALL be an independently deployed network plugin with its own self-contained code and SHALL carry a task-source plugin manifest. Before ingesting tasks, each instance SHALL register with State Registry using a stable immutable plugin instance ID, plugin type, contract version, implementation version, immutable `team_id`, registered `source_system_id`, declared capabilities, safe description, and safe filter-summary metadata. State Registry SHALL reject an unsupported contract version, duplicate/conflicting identity, unknown source system, or team/source mismatch before marking the plugin active. Plugin registration SHALL NOT create a team, source system, or task type and SHALL NOT grant broader ingestion authority. The common contract SHALL be defined by OpenAPI; every service SHALL duplicate its own matching wire types and SHALL NOT import shared platform code.

#### Scenario: Compatible task-source plugin registers

- **WHEN** an authenticated plugin instance submits a supported manifest whose team and source-system binding matches its registered listener identity
- **THEN** State Registry records one canonical active plugin registration and the Task Sources page displays it with its declared safe metadata and capabilities

#### Scenario: Plugin contract or ownership is invalid

- **WHEN** a plugin submits an unsupported contract version, conflicting plugin identity, unknown source system, or mismatched team binding
- **THEN** State Registry rejects registration, marks no plugin active, grants no ingestion authority, and exposes no source credential or sensitive filter value
