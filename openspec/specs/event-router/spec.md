## Purpose

Define the Event Router as FlowAI's task queue manager and only broker. It organizes incoming tasks, routes them through processing services, accumulates ready tasks in the Executor wait list, and keeps durable queue and Executor-pool state in its own database.

## Requirements

### Requirement: Event Router owns transient queue state
The Event Router SHALL own incoming task queue state, processing state, executor wait-list state, executor registry state, slot accounting, in-flight task assignment state, and durable queue storage for broker restart recovery.

#### Scenario: Automation submits a task
- **WHEN** Automation posts a task submission to the Event Router
- **THEN** the Event Router places the task in its incoming queue

#### Scenario: Broker restarts
- **WHEN** the Event Router restarts after accepting queue or Executor-pool state
- **THEN** it recovers that transient routing state from its own database

### Requirement: Event Router delegates task processing logic
The Event Router SHALL route incoming tasks through subscribed processing services for validation, secret-scope binding, and normalization, and SHALL NOT perform that processing logic itself.

#### Scenario: Incoming task needs normalization
- **WHEN** an incoming task requires validation or normalization
- **THEN** the Event Router hands the task to a subscribed processing service before it reaches the Executor wait list

### Requirement: Event Router manages the Executor pool
The Event Router SHALL hold Executor registrations, availability signals, routing targets, configured capacity, and per-Executor slots in use.

#### Scenario: Executor registers
- **WHEN** an Executor posts registration with routing target, capacity, and metadata
- **THEN** the Event Router records the Executor in its pool registry

#### Scenario: Executor signals ready
- **WHEN** an Executor posts an availability signal
- **THEN** the Event Router updates that Executor's available capacity

### Requirement: Event Router dispatches tasks to matching executors
The Event Router SHALL match wait-listed tasks to Executors whose routing target matches and whose slots in use are below configured capacity.

#### Scenario: Executor asks for work
- **WHEN** an Executor with free capacity requests the next task
- **THEN** the Event Router returns a matching wait-listed task and accounts for the occupied slot

### Requirement: Event Router receives Executor status and sends Executor control
The Event Router SHALL receive running, terminal, and slot-free status updates from Executors and SHALL send control events to Executors when a permitted control path creates one.

#### Scenario: Executor posts terminal status
- **WHEN** an Executor posts terminal status for a task
- **THEN** the Event Router updates queue state and frees the occupied slot

### Requirement: Event Router is isolated from operator-facing stores
The Event Router SHALL NOT call the API Gateway, State Store, or Secret Service, and SHALL NOT store historical records, encrypted secret values, plaintext secret values, or business-policy decisions beyond matching.

#### Scenario: Task status changes
- **WHEN** an Executor posts a task status update to the Event Router
- **THEN** the Event Router updates queue state only and does not write the historical task record

#### Scenario: Task payload passes through the broker
- **WHEN** the Event Router forwards a task or message
- **THEN** it forwards the message as received instead of modifying message content
