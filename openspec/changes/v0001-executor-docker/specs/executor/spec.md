## ADDED Requirements

### Requirement: Docker Executor participates in mocked scheduling

The Docker Executor SHALL register with a mocked task server, receive an Executor identifier, report availability, poll tasks from the mocked task server, post task status to the mocked task server, and honor configured capacity until the Router change replaces this surface.

#### Scenario: Docker Executor starts locally

- **WHEN** the Docker Executor process starts
- **THEN** it registers executor type `docker`, routing target, capacity, running child count, and metadata with the mocked task server

#### Scenario: Docker Executor polls work

- **WHEN** the Docker Executor has available capacity
- **THEN** it requests work from the mocked task server instead of the Router

### Requirement: Docker Executor runs only Docker containers

The Docker Executor SHALL control assigned task execution by pulling the agent OCI image, preparing a fresh Docker container, mounting task input, starting the container, observing it until completion, and never running agent code directly in the Executor host process.

#### Scenario: Mocked task server assigns a task

- **WHEN** the mocked task server returns a task to the Docker Executor
- **THEN** the Docker Executor starts an isolated Docker container containing the agent runtime for that task

#### Scenario: Docker child exits

- **WHEN** a child Docker container exits
- **THEN** the Docker Executor observes the exit code, cleans up the child container, updates running child count, and posts terminal status to the mocked task server

### Requirement: Docker Executor tracks child capacity

The Docker Executor SHALL observe every child Docker container it started and SHALL refuse to start additional containers when observed running child count is greater than or equal to configured capacity.

#### Scenario: Capacity is reached

- **WHEN** the Docker Executor's observed running child count is greater than or equal to configured capacity
- **THEN** it does not poll or start another task until a later observation shows capacity available

### Requirement: Docker Executor remains local and non-authoritative

The Docker Executor SHALL NOT queue tasks, own a database, persist secrets, call Web UI or API Gateway, run Kubernetes Pods, or decide which tasks should be scheduled.

#### Scenario: Docker Executor restarts

- **WHEN** the Docker Executor restarts
- **THEN** scheduling state remains outside the Docker Executor
