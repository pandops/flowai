## Purpose

Define Executors as FlowAI's stateless worker fleet. Multiple Executor instances run across Kubernetes or local Docker, register with the Router, pull tasks from its wait list, control the lifecycle of Kubernetes Pods or Docker containers that contain agent runtimes, resolve scoped secrets, stream history, and clean up after execution.

## Requirements

### Requirement: Executor participates in Router scheduling
Each Executor SHALL register with the Router, receive an Executor identifier, report availability, pull matching tasks from the Router wait list, post task status, and honor configured capacity.

#### Scenario: Executor starts
- **WHEN** an Executor process starts
- **THEN** it registers routing target, capacity, and metadata with the Router

#### Scenario: Executor completes a task
- **WHEN** an Executor finishes a task
- **THEN** it posts terminal status to the Router and frees capacity for another task

#### Scenario: Executor pulls work
- **WHEN** an Executor has available capacity
- **THEN** it requests work from the Router wait list and pulls up to its configured capacity

### Requirement: Executor controls isolated agent runtime containers
Each Executor SHALL control assigned task execution by pulling the agent OCI image, preparing a fresh Kubernetes Pod or Docker container runtime, mounting task-scoped secrets, starting the Pod or container that contains the agent runtime, and supervising that runtime until completion.

#### Scenario: Task is assigned
- **WHEN** the Router returns a task to an Executor
- **THEN** the Executor creates or starts an isolated Pod or Docker container containing the agent runtime for that task

#### Scenario: Executor has capacity above one
- **WHEN** an Executor is configured with capacity greater than one
- **THEN** it may supervise up to that many concurrent task Pods or Docker containers

### Requirement: Executor writes history, reads controls, and resolves secrets directly
Each Executor SHALL write historical events to the State Registry, read pending operator control-request records for assigned tasks from the State Registry, and resolve scoped task secrets from the Secret Registry without using the Web UI or API Gateway.

#### Scenario: Agent emits an event
- **WHEN** an agent task produces a runtime event
- **THEN** the Executor writes the event to the State Registry

#### Scenario: Operator control is pending
- **WHEN** a running task has a pending operator control-request record in the State Registry
- **THEN** the Executor reads the record and applies it to the task Pod or Docker container

#### Scenario: Task requires secrets
- **WHEN** a task starts with a valid secret scope token
- **THEN** the Executor calls the Secret Registry open-env surface directly

### Requirement: Executor handles runtime control and cleanup
Each Executor SHALL read operator control-request records from the State Registry during task execution, apply those controls to the task Pod or Docker container, stop containers after task completion, unmount temporary secret material, and signal readiness for the next task.

#### Scenario: Operator control reaches a running task
- **WHEN** the State Registry exposes an inject, cancel, or rerun control-request record for a task assigned to the Executor
- **THEN** the Executor applies the control request to the running task Pod or Docker container rather than running the agent directly

#### Scenario: Task ends
- **WHEN** a task reaches a terminal state
- **THEN** the Executor stops the task Pod or Docker container, removes plaintext secret bytes, and signals readiness to the Router

### Requirement: Executor remains stateless and non-authoritative
Each Executor SHALL NOT be called by the Web UI or API Gateway, SHALL NOT coordinate live tasks through the State Registry, SHALL NOT decide which tasks to run, SHALL NOT queue tasks, SHALL NOT persist secrets, SHALL NOT modify task payloads, SHALL NOT own a database, SHALL NOT bypass the Secret Registry, SHALL NOT validate inputs handled by upstream processing services, and SHALL NOT run agent processes directly in the Executor host process.

#### Scenario: Executor restarts
- **WHEN** an Executor instance restarts
- **THEN** restart-safe queue and history state remain in the Router and State Registry rather than an Executor database

#### Scenario: Task payload arrives
- **WHEN** an Executor receives a task payload from the Router
- **THEN** it passes the payload to the supervised task Pod or Docker container as given instead of modifying, validating, or executing it directly
