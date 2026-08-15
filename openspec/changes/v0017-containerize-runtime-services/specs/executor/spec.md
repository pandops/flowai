## ADDED Requirements

### Requirement: Concrete Executors run from service-owned images

Each concrete Executor SHALL own a production `Containerfile` inside its
service directory. Integration and cross-service E2E tests that exercise an
Executor runtime SHALL build and start that service-owned image and SHALL NOT
substitute a host-compiled Executor binary. The Docker OpenHands Executor SHALL
receive only the explicitly configured Docker-compatible runtime socket it
needs to supervise sibling task containers. The K8s OpenHands Executor SHALL
run as its own Pod image.

#### Scenario: E2E starts concrete Executors

- **WHEN** an E2E scenario exercises Docker OpenHands or K8s OpenHands execution
- **THEN** the harness runs the corresponding service-owned image, records its image identity, and observes the Executor only through public HTTP, State Registry effects, and concrete runtime effects
