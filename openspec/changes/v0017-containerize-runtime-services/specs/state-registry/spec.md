## ADDED Requirements

### Requirement: State Registry runs from its service-owned image

State Registry SHALL own a production `Containerfile` under
`svc/state-registry/`. Integration and cross-service E2E environments that
start State Registry SHALL build and run that image with PostgreSQL supplied
as an attached containerized dependency and SHALL NOT substitute a
host-compiled or in-process State Registry server.

#### Scenario: E2E starts State Registry

- **WHEN** an E2E scenario requires durable platform state
- **THEN** the harness starts the service-owned State Registry image, records its image identity, and reaches it only through its published HTTP surface
