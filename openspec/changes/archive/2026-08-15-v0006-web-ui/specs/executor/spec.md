## ADDED Requirements

### Requirement: Concrete Executors consume resolved launch parameters

The Docker OpenHands and K8s OpenHands Executors SHALL read the
`launch_parameters` discriminator on a successful claim. When it is `true`,
the Executor SHALL require the accompanying `scope_token`, call
`GET /v1/tasks/{task_id}/launch-parameters/open`, and pass the returned values
to the new container or Pod. It SHALL never require or infer an
`environment_id`, and it SHALL fail the accepted task without starting the
runtime when the token or task-scoped open fails.

When `launch_parameters` is `false`, the Executor SHALL start the runtime with
no Registry-provided env or secret values and SHALL NOT call the open
operation. Both concrete Executors SHALL use the same behavior.

The immutable task snapshot SHALL provide the logical secret
`OPENAI_API_KEY` and the ordinary environment variables `OPENAI_BASE_URL` and
`OPENAI_MODEL`. The Executor SHALL use those task-local values exclusively for
the OpenHands conversation LLM configuration and the task runtime. It SHALL
NOT use Executor-process fallback values for the key, provider URL, or model,
SHALL NOT affect another task, and SHALL never log or return the secret through
an Executor API. If any of the three parameters is absent, the Executor SHALL
fail the accepted task before submitting an OpenHands conversation.

#### Scenario: Claimed task has merged parameters

- **WHEN** a claim returns `launch_parameters = true` with a valid task-bound scope token
- **THEN** the Executor opens the task snapshot once and injects its returned values into the new runtime

#### Scenario: Claim has no parameters

- **WHEN** a claim returns `launch_parameters = false`
- **THEN** the Executor starts without a launch-parameter open request

#### Scenario: Task snapshot supplies the LLM provider configuration

- **WHEN** an assigned task opens a snapshot containing `OPENAI_API_KEY`, `OPENAI_BASE_URL`, and `OPENAI_MODEL`
- **THEN** the Executor starts that task's OpenHands conversation exclusively with those snapshot values without logging or persisting the credential plaintext outside the existing encrypted secret/version and immutable task-snapshot boundaries

#### Scenario: Provider configuration is incomplete

- **WHEN** an assigned task snapshot omits the key, provider URL, or model
- **THEN** the Executor fails the task before submitting an OpenHands conversation and does not fall back to Executor-process configuration

### Requirement: Concrete Executors publish explicit OpenHands output

The Docker OpenHands and K8s OpenHands Executors SHALL inspect every valid
OpenHands conversation WebSocket frame before terminal-state handling. They
SHALL append explicitly published agent/assistant text to the State Registry
task log as `reasoning` and explicitly published tool, terminal, command, or
observation output as `work`. They SHALL ignore user input, conversation
status fields, protocol metadata, and hidden provider state. They SHALL NOT
describe the `reasoning` stream as hidden chain-of-thought.

An append failure SHALL be observable in the Executor service log but SHALL
NOT change or duplicate the canonical task lifecycle. Both concrete Executors
SHALL implement the same mapping and use unique idempotency identifiers for
emitted chunks.

#### Scenario: OpenHands publishes assistant and tool output

- **WHEN** a running conversation emits an explicit agent message followed by a tool observation
- **THEN** the concrete Executor appends one `reasoning` chunk followed by one `work` chunk before processing any terminal status in the frames

#### Scenario: OpenHands emits only protocol state

- **WHEN** a conversation frame contains a terminal status and no explicit agent or tool text
- **THEN** the concrete Executor emits the canonical terminal lifecycle event without appending protocol metadata to the task log

### Requirement: Concrete Executors report cancellation progress

Both concrete Executors SHALL poll assigned pending controls while a task is
running. For a `cancel` control, the Executor SHALL first append an
`acknowledged` control event, then send pause/cancellation to the task's own
OpenHands conversation, and append `completed` only after that call succeeds.
If the call fails after acknowledgement, it SHALL append `failed`. Control
events SHALL remain separate from task lifecycle events; cancellation SHALL
not introduce a new lifecycle state.

#### Scenario: Cancellation reaches OpenHands

- **WHEN** a running task has an assigned pending `cancel` control and OpenHands accepts the pause request
- **THEN** the Executor appends ordered `acknowledged` and `completed` control events and terminates the task through an existing terminal lifecycle state

#### Scenario: Cancellation delivery fails

- **WHEN** OpenHands rejects or cannot receive an acknowledged cancellation
- **THEN** the Executor appends a `failed` control event and does not claim that cancellation was completed
