## Purpose

Define Executors as FlowAI's stateless worker fleet. Multiple Executor instances run across Kubernetes or local Docker, register with the Event Router, pull tasks from its wait list, run agent loops in containers, resolve scoped secrets, stream history, and clean up after execution.

## Requirements

### Requirement: Executor participates in Event Router scheduling
Each Executor SHALL register with the Event Router, receive an Executor identifier, report availability, pull matching tasks from the Event Router wait list, post task status, and honor configured capacity.

#### Scenario: Executor starts
- **WHEN** an Executor process starts
- **THEN** it registers routing target, capacity, and metadata with the Event Router

#### Scenario: Executor completes a task
- **WHEN** an Executor finishes a task
- **THEN** it posts terminal status to the Event Router and frees capacity for another task

#### Scenario: Executor pulls work
- **WHEN** an Executor has available capacity
- **THEN** it requests work from the Event Router wait list and pulls up to its configured capacity

### Requirement: Executor runs isolated agent tasks
Each Executor SHALL run assigned agent tasks in containers by pulling the agent OCI image, preparing a fresh container runtime, mounting task-scoped secrets, starting the container, and running the agent loop.

#### Scenario: Task is assigned
- **WHEN** the Event Router returns a task to an Executor
- **THEN** the Executor starts the agent loop in an isolated container for that task

#### Scenario: Executor has capacity above one
- **WHEN** an Executor is configured with capacity greater than one
- **THEN** it may run up to that many concurrent task containers

### Requirement: Executor writes history and resolves secrets directly
Each Executor SHALL write historical events to the State Store and resolve scoped task secrets from the Secret Service without using the Web UI or API Gateway.

#### Scenario: Agent emits an event
- **WHEN** an agent task produces a runtime event
- **THEN** the Executor writes the event to the State Store

#### Scenario: Task requires secrets
- **WHEN** a task starts with a valid secret scope token
- **THEN** the Executor calls the Secret Service open-env surface directly

### Requirement: Executor handles runtime control and cleanup
Each Executor SHALL accept control events from the Event Router during task execution, stop containers after task completion, unmount temporary secret material, and signal readiness for the next task.

#### Scenario: Operator control reaches a running task
- **WHEN** the Event Router sends an inject, cancel, or rerun control event for a task
- **THEN** the Executor applies the control event to the running task container

#### Scenario: Task ends
- **WHEN** a task reaches a terminal state
- **THEN** the Executor stops the container, removes plaintext secret bytes, and signals readiness to the Event Router

### Requirement: Executor remains stateless and non-authoritative
Each Executor SHALL NOT be called by the Web UI or API Gateway, SHALL NOT coordinate live tasks through the State Store, SHALL NOT decide which tasks to run, SHALL NOT queue tasks, SHALL NOT persist secrets, SHALL NOT modify task payloads, SHALL NOT own a database, SHALL NOT bypass the Secret Service, and SHALL NOT validate inputs handled by upstream processing services.

#### Scenario: Executor restarts
- **WHEN** an Executor instance restarts
- **THEN** restart-safe queue and history state remain in the Event Router and State Store rather than an Executor database

#### Scenario: Task payload arrives
- **WHEN** an Executor receives a task payload from the Event Router
- **THEN** it runs the payload as given instead of modifying or validating it
