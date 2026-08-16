## ADDED Requirements

### Requirement: Concrete Executors run from service-owned images

Each concrete Executor SHALL own a production `Containerfile` inside its
service directory. Before an E2E suite starts, its harness SHALL build each
required image from the checked-out source and pass the resolved immutable
image identity into the tests. Every E2E test SHALL start a fresh Executor
container or, for K8s, a fresh Pod from that image; SHALL address it only
through its published container port or test-exposed Pod port; and SHALL remove
the service instance in teardown. No E2E test SHALL start or contact a
host-compiled Executor binary. The Docker OpenHands Executor SHALL run as a
fixed non-root user and receive only the explicitly configured
Docker-compatible Unix socket. The harness SHALL validate the socket and grant
access only through its numeric supplemental GID; it SHALL reject owner-only or
invalid sockets and SHALL NOT use root, privileged mode, permission widening,
or an additional host mount. The K8s OpenHands Executor SHALL run from the
prebuilt image loaded into k3d as a fresh Pod per test.

#### Scenario: E2E starts concrete Executors

- **WHEN** an E2E scenario exercises Docker OpenHands or K8s OpenHands execution
- **THEN** the harness runs a fresh service instance from the prebuilt owned image, records its image identity and exposed port, observes it only through that port and downstream public effects, and removes the container or Pod after the test
