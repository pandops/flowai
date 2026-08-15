## Context

State Registry and Docker Executor test harnesses currently start host-built
Go binaries, while K8s Executor already has a Dockerfile and runs as a Pod in
its cluster suites. These paths do not prove that the deployable images for
all real services build or behave like the tested processes. Repository rules
require every service to remain self-contained and prohibit shared runtime
code between services.

## Goals / Non-Goals

**Goals:**

- Give State Registry and both concrete Executors service-owned production
  container build definitions.
- Make integration and cross-service E2E tests run those images exclusively.
- Preserve existing public HTTP, State Registry, Docker, and Kubernetes
  behavior.
- Make image identity and host-process absence observable test assertions.

**Non-Goals:**

- Containerizing Web UI, which belongs to `v0006-web-ui`.
- Introducing an image registry, release pipeline, Helm replacement, or
  production orchestration platform.
- Containerizing unit-test processes or in-process fakes.

## Decisions

1. Each image definition lives with its owning service. Root orchestration may
   coordinate builds but may not own a shared production Dockerfile.
2. Multi-stage builds compile one static service binary and copy it into a
   minimal non-root runtime image.
3. E2E harnesses build immutable test tags from the checked-out source and
   inspect the started container or Pod image identity before exercising the
   public contract.
4. Docker Executor receives only the explicitly configured runtime socket and
   required network access. K8s Executor continues to run as a Pod and uses
   the service-owned image loaded into the test cluster.
5. PostgreSQL remains an attached containerized dependency; it is not bundled
   into State Registry.

## Risks / Trade-offs

- Container-only suites take longer to build → cache builder layers and reuse
  one image per test worker.
- Docker socket access is privileged → mount only the selected socket and
  assert the mount in the harness.
- Host networking differs across runtimes → resolve container-reachable
  origins explicitly and never rely on an implicit localhost relationship.
- Minimal images reduce debugging tools → retain service logs and image
  metadata as test artifacts.
