## ADDED Requirements

### Requirement: K8s Executor participates in Router scheduling

The K8s Executor SHALL register with the Router, receive an Executor identifier, report availability, pull matching tasks from the Router wait list, post task status, and honor configured capacity.

#### Scenario: K8s Executor starts

- **WHEN** a K8s Executor process starts
- **THEN** it registers executor type `k8s`, routing target, capacity, running child count, and metadata with the Router

### Requirement: K8s Executor runs only Kubernetes Pods

The K8s Executor SHALL control assigned task execution by creating a fresh Kubernetes Pod containing the agent runtime, mounting task-scoped inputs, observing that Pod until completion, and never running agent code directly in the Executor process.

#### Scenario: Router assigns a task to a K8s Executor

- **WHEN** the Router returns a task to a K8s Executor
- **THEN** the K8s Executor creates a Kubernetes Pod containing the agent runtime for that task

### Requirement: K8s Executor tracks child Pod capacity

The K8s Executor SHALL observe every child Pod it started and SHALL refuse to start additional Pods when observed running child count is greater than or equal to configured capacity.

#### Scenario: K8s capacity is reached

- **WHEN** the K8s Executor's observed running Pod count is greater than or equal to configured capacity
- **THEN** it does not pull or start another task until a later observation shows capacity available

### Requirement: K8s Executor remains non-authoritative

The K8s Executor SHALL NOT queue tasks, own a database, persist secrets, call Web UI or API Gateway, run Docker containers, or decide which tasks should be scheduled.

#### Scenario: K8s Executor restarts

- **WHEN** the K8s Executor restarts
- **THEN** scheduling state remains in the Router and history remains in the State Registry
