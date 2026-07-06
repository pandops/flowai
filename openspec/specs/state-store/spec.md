## Purpose

Define the State Store as FlowAI's historical record and canonical platform-state store for task records, task events, audit trail, and routing-rule configuration.

## Requirements

### Requirement: State Store owns historical platform records
The State Store SHALL own canonical task records, task event logs, audit trails, and routing-rule configuration.

#### Scenario: Executor streams a task event
- **WHEN** an Executor posts a task event to the State Store
- **THEN** the State Store appends the event to the task's historical event log

#### Scenario: Task record is persisted
- **WHEN** a submitted task is recorded historically
- **THEN** the State Store stores the task subject, parameters, status, timestamps, and Executor assignment when present

### Requirement: State Store appends event streams verbatim
The State Store SHALL accept historical event writes from Executors during task execution and append events such as tool use, LLM responses, file changes, and errors without modifying the event stream.

#### Scenario: Executor writes an event
- **WHEN** an Executor posts a runtime event to `POST /events`
- **THEN** the State Store appends the event verbatim to the task's event log

### Requirement: State Store serves gateway reads
The State Store SHALL expose read APIs that the API Gateway can proxy for task records, event logs, audit data, and current state views, including `GET /state`, `GET /tasks/:id`, `GET /tasks/:id/events`, and `GET /audit`.

#### Scenario: Operator opens a task detail view
- **WHEN** the API Gateway requests a task record for the Web UI
- **THEN** the State Store returns the canonical task record and related historical data

### Requirement: State Store owns routing rules and audit trail
The State Store SHALL hold routing-rule configuration used by automation or orchestration and SHALL hold audit entries for operator interventions, automation submissions, and Executor lifecycle events.

#### Scenario: Audit history is queried
- **WHEN** the API Gateway requests audit data for the Web UI
- **THEN** the State Store returns the historical audit trail

### Requirement: State Store is independent from queueing and secrets
The State Store SHALL NOT queue tasks, hold in-flight queue state, receive task submissions from Automation, dispatch tasks, match tasks to Executors, execute tasks, store secret values, issue bearer tokens, route messages between services, or call the Event Router or Secret Service.

#### Scenario: Automation submits a task
- **WHEN** Automation submits a new task
- **THEN** the submission goes to the Event Router and not to the State Store
