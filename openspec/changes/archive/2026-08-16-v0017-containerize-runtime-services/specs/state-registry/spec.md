## ADDED Requirements

### Requirement: State Registry runs from its service-owned image

State Registry SHALL own a production `Containerfile` under
`svc/state-registry/`. Before an E2E suite starts, its harness SHALL build the
image from the checked-out source and pass the resolved immutable image
identity into the tests. Every E2E test that requires a real State Registry
SHALL start a fresh container from that image with PostgreSQL supplied as an
attached containerized dependency, SHALL send requests only to the container's
dynamically published HTTP port, and SHALL stop and remove the container and
owned dependencies in teardown. An E2E test SHALL NOT substitute or contact a
host-compiled or in-process State Registry server.

#### Scenario: E2E starts State Registry

- **WHEN** an E2E scenario requires durable platform state
- **THEN** the harness starts a fresh container from the prebuilt service-owned State Registry image, records its image identity and published port, reaches it only through that port, and removes it after the test
