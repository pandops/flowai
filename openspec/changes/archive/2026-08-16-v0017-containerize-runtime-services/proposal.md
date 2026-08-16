## Why

State Registry and concrete Executors are currently exercised through a mix
of host binaries, in-process servers, and runtime images. That allows test and
production startup paths to drift and leaves image buildability unverified.

## What Changes

- Require State Registry, Docker OpenHands Executor, and K8s OpenHands
  Executor to own production container build definitions inside their service
  directories.
- Require every E2E suite to build the required real-service images from the
  checked-out source before testing, pass immutable image references into the
  tests, and start a fresh service container per test rather than a host binary
  or in-process real service.
- Run the Docker Executor image with an explicitly scoped Docker-compatible
  runtime socket and run the K8s Executor as a Pod image.
- Require every E2E request to target only the published port of the fresh
  container; for K8s, load the image into k3d and target only the fresh Pod
  through its test exposure. Record image identity in E2E evidence.
- Stop and remove every per-test container, Pod, namespace, network, and other
  owned runtime resource in teardown, including after assertion failure.
- Keep unit tests host-runnable when they do not start a real runtime service;
  test-only fakes may remain in-process but SHALL NOT replace the real service
  under E2E test.
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
