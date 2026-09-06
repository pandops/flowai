## ADDED Requirements

### Requirement: State Registry treats task types as team-created categories

State Registry SHALL allow an authenticated operator, acting through trusted API Gateway context, to create a task-type category only for the operator's verified immutable `team_id`. The request SHALL include a non-empty category name, a required execution tag, a prompt template, an `agent_id`, and a canonical same-team `model_id`; it MAY include an additional validated execution-parameter set, which MAY be empty, and one digest-bearing `default_image` bound to the resulting `task_type_id`. The prompt-template grammar SHALL support an optional placeholder for the ingested task's exact canonical `source_id`; a static template without placeholders SHALL also be valid. State Registry SHALL issue the immutable canonical `task_type_id`, SHALL bind the category to exactly one immutable team, SHALL validate the model reference and execution settings, and SHALL reject unauthenticated, invalid, or cross-team creation without persistence. A task-source plugin SHALL reference an existing same-team `task_type_id` and SHALL NOT create task types or models during registration or ingestion.

#### Scenario: Operator creates a category for the verified team

- **WHEN** trusted API Gateway forwards an authenticated operator's valid category name, execution tag, prompt template, canonical `agent_id`, same-team `model_id`, optional execution-parameter set and image, verified `operator_id`, immutable `team_id`, and `request_id`
- **THEN** State Registry creates one task-type category for that team and returns its immutable canonical `task_type_id`

#### Scenario: Operator attempts cross-team category creation

- **WHEN** an operator request names or otherwise attempts to bind the new category to a team other than the Gateway-verified team
- **THEN** State Registry rejects the request without creating a task type or revealing whether the other team exists

#### Scenario: Static prompt template is accepted

- **WHEN** an operator configures a valid prompt template that contains no placeholder
- **THEN** State Registry accepts it and every task of that type receives the unchanged static prompt

### Requirement: State Registry versions task-type execution configuration

State Registry SHALL allow a same-team authenticated operator through trusted API Gateway context to change a task type's prompt template, `agent_id`, same-team `model_id`, optional execution parameters, and optional task-type image. Every successful change SHALL create a new immutable task-type configuration revision while preserving the immutable `task_type_id` and `team_id`. Invalid or foreign references SHALL create no revision. Existing task execution snapshots SHALL continue to reference their original revision.

#### Scenario: Operator changes task-type execution configuration

- **WHEN** a same-team operator submits a valid replacement prompt template, `agent_id`, `model_id`, execution parameters, or task-type image
- **THEN** State Registry appends one immutable configuration revision and uses it only for tasks ingested after that revision becomes current

#### Scenario: Operator submits an empty additional parameter set

- **WHEN** a same-team operator configures valid `agent_id` and `model_id` values with no additional execution parameters
- **THEN** State Registry stores a valid task-type revision with an empty additional parameter set

### Requirement: State Registry stores team-owned model catalog entries

State Registry SHALL allow an authenticated operator, acting through trusted API Gateway context, to add a model entry only for the operator's verified immutable `team_id`. State Registry SHALL issue a canonical immutable `model_id`, bind the model to exactly one immutable team, store only safe model metadata and opaque secret references selected during detailed design, and reject caller-supplied credentials or secret plaintext in task-type and model-catalog projection bodies. Task types SHALL reference models by same-team `model_id`; a foreign model identifier SHALL be non-revealing and SHALL NOT be persisted.

#### Scenario: Operator adds a model for the verified team

- **WHEN** trusted API Gateway forwards an authenticated operator's valid safe model configuration and secret references under verified operator, team, and request context
- **THEN** State Registry creates one team-owned model entry and returns its canonical `model_id` without returning secret values

#### Scenario: Operator attempts to use a foreign model

- **WHEN** an operator creates or changes a task type using a `model_id` that is unknown or owned by another team
- **THEN** State Registry returns the same non-revealing not-found response and changes no task type or model

### Requirement: State Registry versions team model configuration

Every successful same-team model configuration change SHALL append a new immutable model revision under the existing immutable `model_id` and `team_id`. A model revision SHALL identify its safe provider/model metadata and immutable versions of its secret references without storing secret plaintext. Existing task execution snapshots SHALL continue to reference their pinned model revision; only later task ingestions MAY select the new current revision.

#### Scenario: Team changes model configuration

- **WHEN** a same-team operator submits a valid change to safe model metadata or secret references
- **THEN** State Registry appends one immutable model revision and leaves every existing task snapshot bound to its prior revision

### Requirement: State Registry snapshots task-type prompt and execution settings

For every newly accepted task ingestion, State Registry SHALL load the referenced same-team task-type revision and same-team model revision, validate their compatibility, deterministically render the prompt template by substituting the exact canonical `source_id` when the optional supported placeholder is present, and persist an immutable execution snapshot with the task. The snapshot SHALL include the rendered prompt, `agent_id`, `model_id`, immutable model revision, validated additional execution parameters, immutable task-type configuration revision, the task-type revision's digest-bearing image override or an explicit absence marker, and immutable versions of any model secret references required for later authorized resolution; it SHALL NOT contain secret plaintext. Later changes to the task type, model, or current secret versions SHALL NOT alter an already ingested task's snapshot. A deduplicated retry SHALL return the existing task and its original snapshot without re-rendering or re-validating against newer task-type, model, or secret revisions. Discovery and administrative summaries SHALL NOT expose the rendered prompt, model credentials, secret references, or sensitive execution values; only the assigned Executor SHALL receive the non-secret execution snapshot after a successful claim and resolve its pinned secret references through the authorized secret-access contract.

#### Scenario: Source ID is rendered into the task prompt

- **WHEN** a listener ingests `source_id = ISSUE-42` for a same-team task type whose valid template contains the supported `source_id` placeholder
- **THEN** State Registry persists one immutable rendered prompt containing `ISSUE-42` together with the selected `agent_id`, `model_id`, model revision, and validated execution parameters before acknowledging ingestion

#### Scenario: Static prompt remains unchanged

- **WHEN** a listener ingests a task for a task type whose valid prompt template contains no placeholder
- **THEN** State Registry persists the template text unchanged as the task's rendered prompt

#### Scenario: Task type changes after ingestion

- **WHEN** the team changes a prompt template, `agent_id`, `model_id`, model revision, or execution parameter after a task using the previous configuration was ingested
- **THEN** the existing task retains its original immutable execution snapshot and only later ingestions use the changed task-type configuration

#### Scenario: Dedupe retry follows configuration changes

- **WHEN** a listener retries an existing `(team_id, source_system_id, source_id)` after the task type, model, or referenced secret has a newer revision
- **THEN** State Registry returns the existing canonical task with its original execution snapshot and performs no new rendering, validation, or secret-version selection

#### Scenario: Prompt template or execution settings are invalid

- **WHEN** task-type configuration contains an invalid template, unknown placeholder, foreign or unknown model, incompatible agent/model selection, or invalid additional execution parameter
- **THEN** State Registry rejects the task-type configuration without partially persisting it or changing existing tasks

### Requirement: State Registry accepts image selection only through task type

Task ingestion SHALL NOT accept a top-level `image` field, an `image` field in a protocol body wrapper, or an equivalent request header. This prohibition SHALL NOT inspect or reinterpret an ordinary domain field named `image` inside the opaque task `payload`. A task SHALL reference exactly one same-team `task_type_id`; only that canonical task-type revision MAY carry one digest-bearing `default_image` override. Source-system registration and every launch-parameter definition scope, including task-type scope, SHALL reject image fields. Ingestion SHALL pin the selected task-type revision's image override or its absence in the immutable task execution snapshot. At claim, State Registry SHALL use that pinned task-type image when present and otherwise the owning team's then-current required `default_image`. Per-task, source-system, and launch-parameter image overrides SHALL NOT exist or participate in image resolution. State Registry SHALL persist `resolved_image` and an image source of `task_type_default` or `team_default` atomically with claim.

#### Scenario: Task type supplies the image override

- **WHEN** a listener ingests a task referencing a same-team task type with `default_image`
- **THEN** State Registry accepts no task-level image input and persists the task type's image as `resolved_image` with `image_source = task_type_default` when the task is claimed

#### Scenario: Task type has no image override

- **WHEN** a listener ingests a task referencing a same-team task type without `default_image`
- **THEN** State Registry resolves the owning team's required default image at claim and records `image_source = team_default`

#### Scenario: Listener supplies a per-task image

- **WHEN** a listener submission contains a top-level `image`, an `image` in a protocol body wrapper, or an equivalent request header
- **THEN** State Registry rejects the request without creating or partially persisting a task

#### Scenario: Opaque payload contains a domain field named image

- **WHEN** a listener submits an otherwise valid task whose opaque `payload` contains a domain-specific field named `image`
- **THEN** State Registry treats that field only as payload data and does not use it for runtime image selection

#### Scenario: Source system or launch parameters supply an image

- **WHEN** source-system registration or a launch-parameter mutation at global, team, or task-type scope contains an image field
- **THEN** State Registry rejects the request without storing an image outside the canonical task-type record

## REMOVED Requirements

### Requirement: State Registry owns task-type registration through `/admin/task-types`

### Requirement: State Registry persists per-task, per-task-type, and per-source-system image overrides

### Requirement: State Registry resolves launch-parameter image overrides
