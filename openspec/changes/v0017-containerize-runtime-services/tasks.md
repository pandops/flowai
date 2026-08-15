## 1. Implementation-start transition

- [ ] Move `specs/test-cases/v0017.1-runtime-services-use-owned-container-images.md` unchanged to `qa-e2e/test-cases/` before writing runnable E2E or production code; verify the source directory contains no `.md` file and the destination ID was not overwritten.
- [ ] Create `qa-e2e/containerized-runtime-services/` with `package.json`, Playwright configuration, suite-level image builder, per-test Docker lifecycle fixture, per-test k3d Pod lifecycle fixture, failure-safe teardown, and a static host-binary guard.
- [ ] Configure suite setup to build State Registry, Docker OpenHands Executor, and K8s OpenHands Executor images once from the current checkout, resolve immutable image IDs, and pass only those references to tests.

## 2. RED — pin container-only E2E behavior

- [ ] Generate `qa-e2e/containerized-runtime-services/tests/v0017-runtime-service-images.spec.ts` from `v0017.1` with exact test titles `v0017.1 / state-registry uses a fresh owned-image container`, `v0017.1 / docker executor uses a fresh owned-image container`, and `v0017.1 / k8s executor uses a fresh owned-image pod`.
- [ ] Add a guard test that scans `qa-e2e/` and fails on real-service host launch paths such as `exec.Command`, `go run`, host-built service binaries, or imports of `executor_binary`; allow in-process code only for test doubles that cannot replace the service under test.
- [ ] Run `npm --prefix qa-e2e/containerized-runtime-services test -- --grep "v0017.1"` and record behavior-specific RED failures for missing State Registry/Docker Executor Containerfiles, K8s `Dockerfile` naming, host-binary launch paths, absent published-port routing, absent fresh-instance teardown, and unsafe Docker socket access.

## 3. GREEN — service-owned production images

- [ ] Add `svc/state-registry/Containerfile` as a multi-stage build with a minimal fixed non-root runtime image and service-owned configuration/migration assets.
- [ ] Add `executor/docker_openhands/Containerfile` as a multi-stage build with a fixed non-root runtime user and no baked-in or default host socket path.
- [ ] Rename `executor/k8s-openhands/Dockerfile` to `executor/k8s-openhands/Containerfile`, update every build reference, and preserve its non-root Pod contract.
- [ ] Implement Docker socket preflight: resolve the explicit socket path, require Unix-socket type and group read/write bits, read its numeric GID, mount only that path, add only that supplemental GID, and reject root, privileged mode, `0666`, owner-only sockets, or extra host mounts.
- [ ] Migrate every `qa-e2e/` path that starts a real State Registry or Executor host binary to the image lifecycle fixtures. Build images before each suite; create a fresh service container or Pod per test; route requests only through its published/test-exposed port; remove it in teardown. Test-only dependency fakes may remain in-process.
- [ ] Run `npm --prefix qa-e2e/containerized-runtime-services test -- --grep "v0017.1"` until all three image tests and the host-binary guard pass.

## 4. REFACTOR and completion

- [ ] Remove obsolete E2E host-binary builders and imports after every consumer uses the container fixtures; retain host execution only in unit or non-E2E developer workflows.
- [ ] Reuse only root test orchestration helpers; keep each production Containerfile, runtime configuration, and duplicated service wire code inside its owning service.
- [ ] Fill the moved `qa-e2e/test-cases/v0017.1-runtime-services-use-owned-container-images.md` with the runnable file, exact test titles, image IDs, RED failure evidence, and GREEN pass command/output.
- [ ] Run `go test ./...`, every affected existing Playwright suite, `npm --prefix qa-e2e/containerized-runtime-services test`, and `npx -y @fission-ai/openspec@1.5.0 validate --all --strict --no-interactive`; require no skipped v0017 test and no real-service host launch under `qa-e2e/`.
