## ADDED Requirements

### Requirement: Concrete Executors pause and continue assigned agent work

Upon receiving an accepted pause control, the assigned concrete Executor SHALL use the Registry-issued immutable cleanup deadline, request OpenHands pause, stop agent execution without removing the runtime, and acknowledge pause to State Registry. While paused it SHALL retain the runtime and assignment but consume no new task. Upon receiving an accepted continue control before cleanup, it SHALL cancel pending cleanup, request OpenHands resume for the same conversation, acknowledge resume, and continue the same task. It SHALL NOT extend the deadline when pause, continue, or reconciliation is retried.

#### Scenario: Executor pauses without immediate cleanup

- **WHEN** OpenHands acknowledges pause and the configured cleanup deadline is in the future
- **THEN** the Executor stops agent work, retains the runtime for possible continue, acknowledges paused, and starts no replacement task in that slot

#### Scenario: Executor continues the same session

- **WHEN** it receives an accepted continue control before cleanup and OpenHands acknowledges resume
- **THEN** the Executor cancels cleanup and resumes the same conversation, task assignment, and command identifier

#### Scenario: Continue fails

- **WHEN** OpenHands cannot resume the paused conversation
- **THEN** the Executor reports the failed continue outcome, leaves cleanup scheduled at the original deadline, and does not start a new conversation

### Requirement: Concrete Executors implement interruption as zero-wait pause and cleanup

Upon receiving an accepted interrupt control, the assigned concrete Executor SHALL invoke the same pause-and-cleanup mechanism with effective cleanup wait exactly zero. It MAY make a best-effort OpenHands pause request but SHALL NOT wait for acknowledgement before stopping and removing the container or Pod, releasing local capacity, and acknowledging terminal `interrupted`. A restart SHALL reconcile and immediately finish any incomplete zero-wait cleanup.

#### Scenario: Running task is interrupted immediately

- **WHEN** an accepted interrupt reaches the Executor for a running task
- **THEN** the Executor uses cleanup wait zero, immediately removes the runtime, releases capacity, and acknowledges `interrupted` without waiting for OpenHands

#### Scenario: Paused task is interrupted immediately

- **WHEN** an accepted interrupt reaches the Executor during a configured pause window
- **THEN** the Executor cancels the remaining wait, immediately removes the runtime, releases capacity, and acknowledges `interrupted`

#### Scenario: Executor restarts with incomplete interrupt cleanup

- **WHEN** an Executor restarts and Registry state shows an accepted interrupt whose runtime still exists
- **THEN** it immediately removes the runtime and does not create or extend a cleanup timer

### Requirement: Concrete Executors clean paused runtimes when the deadline expires

If no accepted continue wins before a paused task's immutable cleanup deadline, the assigned Executor SHALL stop and remove the runtime and release local capacity at the deadline. It SHALL report cleanup completion to State Registry and SHALL NOT resume the task, extend the deadline, or implicitly snapshot the session. Hibernate and later recovery are separate actions and SHALL NOT be inferred from pause.

#### Scenario: Pause expires without continue

- **WHEN** the cleanup deadline arrives and no continue has been accepted
- **THEN** the Executor removes the runtime, releases capacity, and reports cleanup completion without extending the deadline

#### Scenario: Executor restarts during pause window

- **WHEN** an Executor restarts with an owned paused runtime before its persisted cleanup deadline
- **THEN** it reconciles the original deadline and waits only for the remaining interval
