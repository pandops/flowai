## ADDED Requirements

### Requirement: OpenHands Executors start native Goal mode

After successfully claiming a Goal-mode task, the Docker or K8s OpenHands Executor SHALL
create one normal conversation and call the native
`POST /api/conversations/{conversation_id}/goal` endpoint with the Registry
objective and effective iteration cap. It SHALL require a pinned Goal-capable
agent-server image, start no ordinary initial-message run in parallel, and emit
FlowAI `running` only after native Goal running is observed.

#### Scenario: Goal starts successfully

- **WHEN** an OpenHands Executor claims a valid Goal-mode task and the native endpoint accepts it
- **THEN** the Executor observes native `running`, appends the FlowAI running and Goal progress events, and supervises the same conversation

#### Scenario: Agent server lacks Goal support

- **WHEN** the pinned runtime does not support the native Goal endpoint or rejects valid configuration
- **THEN** the Executor starts no fallback single run and appends one content-safe `failed` event

### Requirement: OpenHands Executors mirror native Goal events

The assigned Executor SHALL consume persisted OpenHands
`ConversationStateUpdateEvent` records whose key is `goal`, normalize supported
status fields, and append stable idempotent Goal events to State Registry. It
SHALL never forward raw judge prompts, hidden reasoning, credentials, or raw
conversation events and SHALL apply the documented terminal lifecycle mapping.

#### Scenario: Native progress advances

- **WHEN** OpenHands persists a later Goal audit iteration
- **THEN** the Executor appends one ordered normalized Goal event without changing FlowAI lifecycle state

#### Scenario: Native Goal completes

- **WHEN** the latest native Goal event is `complete`
- **THEN** the Executor mirrors it and appends exactly one FlowAI `finished` event

#### Scenario: Native Goal caps

- **WHEN** the latest native Goal event is `capped`
- **THEN** the Executor mirrors it and appends exactly one FlowAI `failed` event with the stable cap reason

### Requirement: OpenHands Executors reconcile Goal state after restart

Before starting or resuming work for an already assigned Goal task, the Executor SHALL
read the owned conversation's persisted event history, locate
the latest `key = "goal"` update, deduplicate already mirrored provider event
IDs, and continue observation or the applicable v0010 recovery. It SHALL NOT
depend on a nonexistent `GET /goal`, guess status, or start a second Goal.

#### Scenario: Executor restarts during Goal execution

- **WHEN** the Executor returns while the owned conversation contains persisted non-terminal Goal state
- **THEN** it reconciles mirrored progress and resumes supervision without another Goal start request

#### Scenario: Goal history is inconsistent

- **WHEN** the conversation is missing required Goal history or contains an impossible regression
- **THEN** the Executor starts no second Goal, emits a content-safe failure, and releases capacity under the ordinary rule
