## ADDED Requirements

### Requirement: Router owns transient queue state

The Router SHALL own incoming task queue state, processing state, executor wait-list state, executor registry state, slot accounting, in-flight task assignment state, and durable queue storage for broker restart recovery.

#### Scenario: Automation submits a task

- **WHEN** Automation posts a task submission to the Router
- **THEN** the Router places the task in its incoming queue

#### Scenario: Broker restarts

- **WHEN** the Router restarts after accepting queue or Executor-pool state
- **THEN** it recovers that transient routing state from its own database

### Requirement: Router manages Docker Executor scheduling

The Router SHALL hold Docker Executor registrations, availability signals, routing targets, configured capacity, per-Executor slots in use, and latest running child count reported by each Docker Executor.

#### Scenario: Docker Executor registers

- **WHEN** a Docker Executor posts registration with routing target, capacity, and metadata
- **THEN** the Router records executor type `docker`, routing target, capacity, running child count, and metadata in its pool registry

### Requirement: Router dispatches tasks to matching Docker Executors

The Router SHALL match wait-listed tasks to Docker Executors whose routing target matches, whose slots in use are below configured capacity, and whose latest reported running child count is below configured capacity.

#### Scenario: Docker Executor asks for work

- **WHEN** a Docker Executor with free capacity and running child count below capacity requests the next task
- **THEN** the Router returns a wait-listed task matching that Executor's routing target and accounts for the occupied slot

### Requirement: Router receives Docker Executor status

The Router SHALL receive running, terminal, cancellation, and slot-free status updates from Docker Executors while keeping operator-originated control requests outside the Router.

#### Scenario: Docker Executor posts terminal status

- **WHEN** a Docker Executor posts terminal status for a task
- **THEN** the Router updates queue state and frees the occupied slot

### Requirement: Router is isolated from operator-facing stores

The Router SHALL NOT call the API Gateway, State Registry, or Env Registry, and SHALL NOT store historical records, encrypted secret values, plaintext secret values, or business-policy decisions beyond matching.

#### Scenario: Task status changes

- **WHEN** an Executor posts a task status update to the Router
- **THEN** the Router updates queue state only and does not write the historical task record
