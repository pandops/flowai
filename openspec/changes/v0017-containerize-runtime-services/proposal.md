## Why

State Registry and concrete Executors are currently exercised through a mix
of host binaries, in-process servers, and runtime images. That allows test and
production startup paths to drift and leaves image buildability unverified.

## What Changes

- Require State Registry, Docker OpenHands Executor, and K8s OpenHands
  Executor to own production container build definitions inside their service
  directories.
- Require integration and cross-service E2E harnesses to build and start the
  real services from those images rather than host binaries or in-process
  servers.
- Run the Docker Executor image with an explicitly scoped Docker-compatible
  runtime socket and run the K8s Executor as a Pod image.
- Record image identity in E2E evidence so a host-process substitution fails
  deterministically.
- Keep unit tests host-runnable when they do not start a runtime service.
- Leave Web UI containerization in `v0006-web-ui`.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `state-registry`: require a service-owned production image and container-only runtime startup in integration and E2E environments.
- `executor`: require every concrete Executor to own and run from its production image in integration and E2E environments.

## Impact

- Affected code: `svc/state-registry/`, `executor/docker_openhands/`,
  `executor/k8s-openhands/`, and root `qa-e2e/` orchestration.
- Affected deployment: local Docker-compatible runtime and test Kubernetes
  cluster image loading.
- Affected APIs: none; public service contracts remain unchanged.
- Affected test topology: real service processes become container-only.
