## ADDED Requirements

### Requirement: State Registry owns historical platform records

The State Registry SHALL own canonical task records, task event logs, audit trails, and operator control-request records.

#### Scenario: Executor streams a task event

- **WHEN** an Executor posts a task event to the State Registry
- **THEN** the State Registry appends the event to the task's historical event log

#### Scenario: Task record is persisted

- **WHEN** a submitted task is recorded historically
- **THEN** the State Registry stores the task subject, parameters, status, timestamps, and Executor assignment when present

### Requirement: State Registry appends event streams verbatim

The State Registry SHALL accept historical event writes during task execution and append events such as tool use, LLM responses, file changes, child runtime lifecycle updates, running child count observations, and errors without modifying the event stream.

#### Scenario: Runtime writes an event

- **WHEN** a runtime component posts an event to `POST /events`
- **THEN** the State Registry appends the event verbatim to the task's event log

### Requirement: State Registry serves direct reads before Web UI exists

The State Registry SHALL expose read APIs for task records, event logs, audit data, and current state views.

#### Scenario: Developer reads task history

- **WHEN** a developer requests a task record directly before API Gateway and Web UI exist
- **THEN** the State Registry returns the canonical task record and related historical data

### Requirement: State Registry records operator control requests

The State Registry SHALL accept operator intervention and cancellation requests as audit-backed control-request records and SHALL expose those records for Executors to read during assigned task execution.

#### Scenario: Control request is recorded

- **WHEN** a direct control request is accepted before API Gateway and Web UI exist
- **THEN** the State Registry appends a control-request record and audit entry for the task

### Requirement: State Registry is independent from queueing and secrets

The State Registry SHALL NOT queue tasks, hold in-flight queue state, dispatch tasks, match tasks to Executors, save routing configuration, execute tasks, store executor environment variables, store secret values, issue bearer tokens, route messages between services, or call a scheduler or Env Registry.

#### Scenario: A task is submitted for execution

- **WHEN** a new task is submitted for scheduling
- **THEN** the submission goes to a scheduler surface and not to the State Registry
