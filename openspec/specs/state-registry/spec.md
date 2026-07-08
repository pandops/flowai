## Purpose

Define the State Registry as FlowAI's historical record and canonical platform-state registry for task records, task events, audit trail, operator control-request records, and non-secret agent runtime environment configuration.

## Requirements

### Requirement: State Registry owns historical platform records
The State Registry SHALL own canonical task records, task event logs, audit trails, operator control-request records, and non-secret agent runtime environment configuration.

#### Scenario: Executor streams a task event
- **WHEN** an Executor posts a task event to the State Registry
- **THEN** the State Registry appends the event to the task's historical event log

#### Scenario: Task record is persisted
- **WHEN** a submitted task is recorded historically
- **THEN** the State Registry stores the task subject, parameters, status, timestamps, and Executor assignment when present

### Requirement: State Registry appends event streams verbatim
The State Registry SHALL accept historical event writes from Executors during task execution and append events such as tool use, LLM responses, file changes, and errors without modifying the event stream.

#### Scenario: Executor writes an event
- **WHEN** an Executor posts a runtime event to `POST /events`
- **THEN** the State Registry appends the event verbatim to the task's event log

### Requirement: State Registry serves gateway reads
The State Registry SHALL expose read APIs that the API Gateway can proxy for task records, event logs, audit data, and current state views, including `GET /state`, `GET /tasks/:id`, `GET /tasks/:id/events`, and `GET /audit`.

#### Scenario: Operator opens a task detail view
- **WHEN** the API Gateway requests a task record for the Web UI
- **THEN** the State Registry returns the canonical task record and related historical data

### Requirement: State Registry owns runtime environment configuration and audit trail
The State Registry SHALL hold non-secret agent runtime environment configuration and SHALL hold audit entries for operator interventions, automation submissions, and Executor lifecycle events.

#### Scenario: Runtime environment config is read
- **WHEN** a caller reads agent runtime environment configuration
- **THEN** the State Registry returns non-secret configuration values and secret references only

#### Scenario: Audit history is queried
- **WHEN** the API Gateway requests audit data for the Web UI
- **THEN** the State Registry returns the historical audit trail

### Requirement: State Registry records operator control requests
The State Registry SHALL accept API Gateway-proxied operator intervention and cancellation requests as audit-backed control-request records and SHALL expose those records for Executors to read during assigned task execution without pushing them to the Router or Executors.

#### Scenario: Operator requests intervention
- **WHEN** the API Gateway proxies an authenticated operator intervention request
- **THEN** the State Registry appends a control-request record and audit entry for the task

#### Scenario: Executor checks task controls
- **WHEN** an Executor asks for pending control-request records for an assigned task
- **THEN** the State Registry returns matching records without calling the Router or Secret Registry

### Requirement: State Registry is independent from queueing and secrets
The State Registry SHALL NOT queue tasks, hold in-flight queue state, receive task submissions from Automation, dispatch tasks, match tasks to Executors, save routing configuration, execute tasks, store secret values, issue bearer tokens, route messages between services, or call the Router or Secret Registry.

#### Scenario: Automation submits a task
- **WHEN** Automation submits a new task
- **THEN** the submission goes to the Router and not to the State Registry
