## ADDED Requirements

### Requirement: Concrete Executors hibernate assigned sessions and release resources

Upon receiving an accepted hibernate control, the assigned concrete Executor SHALL immediately stop agent execution, package only allow-listed OpenHands recovery files into the versioned archive format, reject links, devices, traversal and configured size/count violations, compute the required digest, and upload through State Registry. It SHALL remove the container or Pod and release local capacity after storage acknowledgement or unrecoverable snapshot/storage failure. It SHALL acknowledge `hibernate` only after durable storage succeeds and SHALL acknowledge `failed` if preservation fails. It SHALL NOT retain the runtime for direct continuation.

#### Scenario: Executor hibernates a running session

- **WHEN** an accepted hibernate control reaches an assigned running task and valid recovery files are available
- **THEN** the Executor immediately stops work, uploads a verified archive, removes the runtime, releases capacity, and acknowledges `hibernate`

#### Scenario: Hibernate preservation fails

- **WHEN** archive creation or upload fails after execution stops
- **THEN** the Executor removes the runtime, releases capacity, acknowledges `failed`, and does not advertise a recoverable session

### Requirement: Concrete Executors restore hibernated sessions for later runs

For a claimed continuation task, the assigned Executor SHALL download the hibernated archive through State Registry, verify its manifest and digest, extract it into a fresh empty workspace, start OpenHands with the preserved conversation identifier, and submit the continuation task's new user query only after recovery succeeds.

#### Scenario: Executor restores and continues a hibernated session

- **WHEN** the Executor claims a continuation task and receives a valid archive handle
- **THEN** it safely restores the preserved conversation, submits the new query, and emits ordinary lifecycle events for the continuation task

#### Scenario: Unsafe archive fails closed

- **WHEN** an archive has an invalid path, link, device, unsupported version, excessive size/count, or digest mismatch
- **THEN** the Executor starts no OpenHands conversation, emits `failed` for the continuation task, and exposes no archive content in logs or events
