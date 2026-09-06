## ADDED Requirements

### Requirement: Platform provides one administrative Task Sources interface

Before any concrete Jira Listener, GitLab Webhook Listener, or Cron Task Scheduler is implemented, the platform SHALL provide one authenticated team-scoped task-sources page through Web UI and API Gateway. The page SHALL obtain canonical data from State Registry and SHALL display registered task-source plugins for the operator's verified team with concise plugin identity/type/version, capabilities, source system, filter summary, health/activity, ingestion statistics, and associated task-type categories. Each category SHALL expose its category name, canonical `task_type_id`, execution tag, prompt template, canonical `agent_id`, selected same-team `model_id`, optional additional execution parameters, and optional task-type image override. The page SHALL allow the operator to configure a prompt template that MAY interpolate the task's canonical `source_id`, select canonical `agent_id` and `model_id` values, and configure an additional validated execution-parameter set that MAY be empty. The same page SHALL allow an authenticated operator to request creation of a task-type category only for the operator's verified team and SHALL display the canonical `task_type_id` returned by State Registry. Web UI SHALL NOT call State Registry or plugin services directly and SHALL NOT display source credentials, model credentials, secrets, raw sensitive filters, or task payloads. Exact APIs, fields, template grammar, execution-parameter schema, statistic windows, permissions, and presentation SHALL be selected in a later design phase.

#### Scenario: Team operator reviews sources and creates a task-type category

- **WHEN** an authenticated operator opens the Task Sources page and submits a valid category name, execution tag, prompt template with or without the supported `source_id` placeholder, canonical `agent_id`, same-team `model_id`, an optional valid execution-parameter set, and an optional image for the operator's verified team
- **THEN** the page shows same-team source summaries, statistics, filters, and existing task-type categories, routes the creation through API Gateway to State Registry, and displays the newly returned canonical `task_type_id`

#### Scenario: Operator attempts cross-team task-type creation

- **WHEN** an operator submits another team's identifier or an unauthenticated user calls the task-type creation surface
- **THEN** the platform rejects creation without mutating State Registry or disclosing cross-team source data

### Requirement: Platform provides a separate team model catalog

The platform SHALL provide an authenticated team-scoped Models surface through Web UI and API Gateway. An operator SHALL be able to add model entries for the operator's verified team independently of task-type creation and SHALL receive a canonical `model_id` from State Registry. A task type SHALL reference one same-team model by `model_id`; it SHALL NOT embed model credentials or use a model owned by another team. Model catalog reads SHALL expose only safe configuration metadata and SHALL NOT expose credentials or secret values. Exact provider fields, secret-reference mechanism, update/versioning rules, and deletion behavior SHALL be selected in detailed design.

#### Scenario: Team adds a model and selects it for a task type

- **WHEN** an authenticated operator adds a valid model entry for the verified team and then selects its canonical `model_id` while configuring a same-team task type
- **THEN** State Registry stores two separate team-owned resources and the task type references the model without copying credentials into the task type

#### Scenario: Task type references another team's model

- **WHEN** an operator attempts to configure a task type with a `model_id` owned by another team
- **THEN** the platform returns the same non-revealing response as for an unknown model and changes no task type or model

### Requirement: Task-source services implement and register a versioned plugin contract

Every task-source service SHALL be an independently deployed network plugin with its own self-contained code and SHALL carry a task-source plugin manifest. Before ingesting tasks, each instance SHALL register with State Registry using a stable immutable plugin instance ID, plugin type, contract version, implementation version, immutable `team_id`, registered `source_system_id`, declared capabilities, safe description, and safe filter-summary metadata. State Registry SHALL reject an unsupported contract version, duplicate/conflicting identity, unknown source system, or team/source mismatch before marking the plugin active. Plugin registration SHALL NOT create a team, source system, or task type and SHALL NOT grant broader ingestion authority. The common contract SHALL be defined by OpenAPI; every service SHALL duplicate its own matching wire types and SHALL NOT import shared platform code.

#### Scenario: Compatible task-source plugin registers

- **WHEN** an authenticated plugin instance submits a supported manifest whose team and source-system binding matches its registered listener identity
- **THEN** State Registry records one canonical active plugin registration and the Task Sources page displays it with its declared safe metadata and capabilities

#### Scenario: Plugin contract or ownership is invalid

- **WHEN** a plugin submits an unsupported contract version, conflicting plugin identity, unknown source system, or mismatched team binding
- **THEN** State Registry rejects registration, marks no plugin active, grants no ingestion authority, and exposes no source credential or sensitive filter value
