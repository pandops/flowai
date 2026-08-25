## ADDED Requirements

### Requirement: Web UI renders OpenHands Goal progress

The same-team task detail view SHALL distinguish Goal-mode tasks and render the
objective, status, current audit iteration, immutable maximum iterations, and
latest bounded verdict summary returned through API Gateway. It SHALL keep Goal
progress separate from lifecycle, task-control, and execution-log entries and
SHALL not claim to expose hidden judge or model reasoning.

#### Scenario: Running Goal advances

- **WHEN** a same-team Goal frame reports a later audit iteration
- **THEN** Web UI updates Goal progress without duplicating lifecycle or log entries

#### Scenario: Goal reaches its cap

- **WHEN** the task ends with the stable Goal-cap failure reason
- **THEN** Web UI shows that the audit iteration limit was reached rather than presenting the Goal as complete

### Requirement: Web UI uses authorized controls for Goal stop and resume

Web UI SHALL expose pause and continue for Goal-mode tasks only when the v0010
control projection permits those actions. It SHALL send controls only through
API Gateway and SHALL NOT call OpenHands, an Executor, or a native Goal endpoint
directly.

#### Scenario: Operator pauses a Goal

- **WHEN** a same-team operator pauses an eligible running Goal
- **THEN** Web UI submits the ordinary authorized pause control and renders its ordered control and Goal outcomes

#### Scenario: Continue is unavailable after cleanup

- **WHEN** the Goal runtime cleanup deadline has won
- **THEN** Web UI disables direct continue and does not offer dynamic continuation or hibernation as an implicit substitute
