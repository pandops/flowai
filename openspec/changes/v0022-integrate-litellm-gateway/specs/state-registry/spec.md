## ADDED Requirements

### Requirement: State Registry stores safe team-owned LiteLLM target references

State Registry SHALL allow a trusted-Gateway same-team operator to register and revise a LiteLLM target reference containing an immutable canonical `target_id`, immutable `team_id`, `kind` from `{model, a2a_agent}`, display metadata, an immutable-revision public alias, safe capability metadata, and enabled state. Model targets SHALL expose the canonical target ID as `model_id`; A2A agent targets SHALL expose it as `agent_id`. Within one team, `agent_id` SHALL occupy one database-enforced namespace shared by local runtime agents and LiteLLM A2A agents, and its immutable kind discriminator SHALL make resolution unambiguous; a colliding ID SHALL be rejected without mutation. This requirement supersedes `v0013` model registration only where `v0013` permits an operator-supplied model secret reference: for a LiteLLM-backed target the reference is always server-provisioned. Before committing a revision, State Registry SHALL verify the exact alias and kind through an allowlisted LiteLLM metadata endpoint using a read-only service identity, create a pending credential binding, use a distinct key-provisioner identity to create a virtual key restricted to that team and exact alias, store its plaintext immediately as an OpenBao-backed immutable secret version, and atomically activate the target revision and binding. A failed validation, key creation, secret write, or final database commit SHALL leave no active target or credential binding and SHALL revoke any created LiteLLM key. Because immutable secret versions are not deleted, a secret version written before a later failure SHALL be marked orphaned and permanently unreachable through environment-open or operator reads, then removed only by the defined retention process with a plaintext-free audit record. It SHALL NOT store or return provider credentials, LiteLLM administrator credentials, the master key, or virtual-key plaintext outside encrypted secret storage. Unknown and foreign identifiers SHALL produce the same non-revealing response.

#### Scenario: Team registers a known model alias

- **WHEN** a same-team operator registers a model-kind reference whose exact alias exists in LiteLLM
- **THEN** State Registry provisions an exact-alias team key, stores it through OpenBao, and creates one canonical target revision containing only safe metadata and a server-owned opaque reference

#### Scenario: Alias is absent or has another kind

- **WHEN** the supplied alias is absent, disabled, or resolves to a different target kind
- **THEN** State Registry rejects the request without persisting a target revision

#### Scenario: Operator references another team's target

- **WHEN** an operator reads or selects a target owned by another team
- **THEN** State Registry returns the same non-revealing response used for an unknown identifier

#### Scenario: A2A agent ID collides with a local agent ID

- **WHEN** a team attempts to register a LiteLLM A2A target using an `agent_id` already bound to a local runtime agent in that team
- **THEN** State Registry rejects the registration without creating a target, key, secret version, or ambiguous catalog entry

#### Scenario: Database finalization fails after secret storage

- **WHEN** State Registry has created the LiteLLM key and encrypted its secret version but cannot commit the active target binding
- **THEN** it revokes the LiteLLM key, marks the immutable secret version orphaned and inaccessible, creates no active target, and emits only a plaintext-free cleanup audit record

### Requirement: State Registry snapshots LiteLLM execution selection

For each accepted task, State Registry SHALL snapshot each selected canonical target ID, immutable target revision, target kind, exact LiteLLM public alias, safe capability metadata, and opaque virtual-key secret-version reference. Discovery, task, event, audit, and administrative projections SHALL NOT expose the secret reference or value. Only after claim SHALL the assigned Executor receive the target configuration through the authorized `OpenEnvironmentResponse.values` string map, using `LITELLM_BASE_URL`, `LITELLM_MODEL_ALIAS`, `LITELLM_MODEL_KEY`, and, when selected, `LITELLM_A2A_ALIAS`, `LITELLM_A2A_KEY`, `LITELLM_A2A_PROTOCOL_VERSION`, and `LITELLM_A2A_TRANSPORT`.

#### Scenario: Task snapshot pins a target revision

- **WHEN** State Registry accepts a task whose task-type revision selects an enabled compatible target
- **THEN** it immutably snapshots that exact target revision without any credential plaintext

#### Scenario: Target changes after ingestion

- **WHEN** an administrator or team operator changes or disables the target after task ingestion
- **THEN** the existing task retains its original target identity and either executes that exact alias or fails explicitly without retargeting

#### Scenario: Unassigned Executor requests target credentials

- **WHEN** an Executor not assigned to the task requests its scoped environment
- **THEN** State Registry rejects the request without returning the alias, secret reference, or runtime key
