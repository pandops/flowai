## ADDED Requirements

### Requirement: Users can hibernate work and start a later run

FlowAI SHALL expose hibernate and later-run actions only through Web UI's API Gateway backend for operators, with a separate authenticated admin path for system administrators. Hibernate SHALL stop execution and release the runtime after session storage and SHALL NOT retain resources for direct continuation. A later run SHALL require a new non-empty user query and SHALL visibly create a continuation task related to the hibernated source. Browser and Gateway SHALL never receive archive bytes or storage credentials.

#### Scenario: Operator hibernates through platform topology

- **WHEN** a same-team operator requests hibernate
- **THEN** Web UI calls only API Gateway, Gateway calls only State Registry, and neither browser nor Gateway calls an Executor or storage backend

#### Scenario: User starts a later run with a new query

- **WHEN** an authorized user selects a hibernated task and submits a new query
- **THEN** Web UI displays the new pending continuation task and its hibernated source without exposing archive contents or locators
