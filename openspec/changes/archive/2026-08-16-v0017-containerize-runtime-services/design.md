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
- Make E2E tests run real FlowAI services exclusively from images built from
  the current checkout before the suite starts.
- Preserve existing public HTTP, State Registry, Docker, and Kubernetes
  behavior.
- Make image identity and container-port routing observable test assertions.
- Give every test a fresh isolated service instance and deterministic teardown.

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
3. A suite-level build step builds every required image once from the checked-out
   source, resolves its immutable image ID/digest, and passes that reference to
   tests. Tests SHALL NOT pull an unverified replacement image or build from a
   different worktree.
4. Each E2E test starts a new service container with a unique name, network, and
   dynamically published host port. The test discovers that port from the
   container runtime and sends every service request to it. Teardown stops and
   removes the service and its owned dependencies even after failure.
5. K8s E2E uses the same lifecycle at Pod level: load the prebuilt image into
   k3d, create a fresh namespace and service Pod per test, target only that Pod
   through the test's Service/port-forward, and delete the namespace in
   teardown. A k3d cluster may be suite-scoped, but the service Pod may not be
   reused between tests.
6. Docker Executor runs as a fixed non-root image user. Before starting it, the
   harness resolves the configured Docker-compatible Unix socket, verifies that
   it is a socket with group read/write permission, reads its numeric GID, and
   adds exactly that GID as a supplemental container group. The harness mounts
   only that exact socket path at the configured in-container path. It SHALL NOT
   run the Executor as root or privileged, change the socket to mode `0666`, or
   mount an unresolved/default host path. A socket that cannot grant access by
   supplemental GID fails preflight with an actionable error.
7. Existing E2E harnesses that start real State Registry or Executor host
   binaries are migrated to the container fixtures. Test-only mock servers may
   remain in-process only when they are dependencies rather than the real
   service under test.
8. PostgreSQL remains an attached containerized dependency; it is not bundled
   into State Registry.

## Risks / Trade-offs

- Container-only suites take longer to build → cache builder layers and reuse
  one image per test worker.
- Docker socket access effectively grants control over the selected runtime →
  use a non-root user plus the socket's numeric supplemental GID, mount only the
  validated socket, forbid privileged mode, and assert the exact mount and
  group configuration through container inspection.
- Some Docker-compatible sockets are owner-only (`0600`) → fail preflight
  rather than weakening host permissions or silently running as root.
- Host networking differs across runtimes → resolve container-reachable
  origins explicitly and never rely on an implicit localhost relationship.
- Minimal images reduce debugging tools → retain service logs and image
  metadata as test artifacts.
- Fresh service instances increase E2E duration → build images once per suite,
  reuse only immutable images and optional k3d cluster infrastructure, and
  never reuse the service container or Pod itself.
