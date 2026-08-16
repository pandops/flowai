## 1. Implementation-start transition

- [x] Move `specs/test-cases/v0017.1-runtime-services-use-owned-container-images.md` unchanged to `qa-e2e/test-cases/` before writing runnable E2E or production code; verify the source directory contains no `.md` file and the destination ID was not overwritten.
- [x] Create `qa-e2e/containerized-runtime-services/` with `package.json`, Playwright configuration, suite-level image builder, per-test Docker lifecycle fixture, per-test k3d Pod lifecycle fixture, failure-safe teardown, and a static host-binary guard.
  - Verify: `npm --prefix qa-e2e/containerized-runtime-services test` exercises cleanup-backed Docker, TLS-PostgreSQL/Registry, and k3d namespace/Pod fixtures; final GREEN result: `4 passed (3.2m)`.
- [x] Configure suite setup to build State Registry, Docker OpenHands Executor, and K8s OpenHands Executor images once from the current checkout, resolve immutable image IDs, and pass only those references to tests.
  - Verify: `.images.json` from the final GREEN run recorded immutable IDs `sha256:b45112c2...`, `sha256:d8557906...`, and `sha256:734debcb...`; tests consume the IDs, and the k3d helper derives a local import tag from its supplied ID.

## 2. RED — pin container-only E2E behavior

- [x] Generate `qa-e2e/containerized-runtime-services/tests/v0017-runtime-service-images.spec.ts` from `v0017.1` with exact test titles `v0017.1 / state-registry uses a fresh owned-image container`, `v0017.1 / docker executor uses a fresh owned-image container`, and `v0017.1 / k8s executor uses a fresh owned-image pod`.
- [x] Add a guard test that scans `qa-e2e/` and fails on real-service host launch paths such as `exec.Command`, `go run`, host-built service binaries, or imports of `executor_binary`; allow in-process code only for test doubles that cannot replace the service under test.
- [x] Run `npm --prefix qa-e2e/containerized-runtime-services test -- --grep "v0017.1"` and record behavior-specific RED failures for missing State Registry/Docker Executor Containerfiles, K8s `Dockerfile` naming, host-binary launch paths, absent published-port routing, absent fresh-instance teardown, and unsafe Docker socket access.
  - RED (before production edits): same command returned `4 failed`; failures named missing `svc/state-registry/Containerfile`, missing `executor/docker_openhands/Containerfile`, forbidden/missing K8s `Containerfile`, and host launch paths in `registry_worker.ts` / `executor_binary.ts` and their consumers. The generated tests also pinned dynamic port discovery, two fresh Registry instances, cleanup, and socket `0660`/GID preflight before GREEN.

## 3. GREEN — service-owned production images

- [x] Add `svc/state-registry/Containerfile` as a multi-stage build with a minimal fixed non-root runtime image and service-owned configuration/migration assets.
- [x] Add `executor/docker_openhands/Containerfile` as a multi-stage build with a fixed non-root runtime user and no baked-in or default host socket path.
- [x] Rename `executor/k8s-openhands/Dockerfile` to `executor/k8s-openhands/Containerfile`, update every build reference, and preserve its non-root Pod contract.
- [x] Implement Docker socket preflight: resolve the explicit socket path, require Unix-socket type and group read/write bits, read its numeric GID, mount only that path, add only that supplemental GID, and reject root, privileged mode, `0666`, owner-only sockets, or extra host mounts.
  - Verify: GREEN inspect asserts fixed non-root user, `Privileged=false`, exactly one bind/mount, exact resolved socket source/target, and exactly one supplemental GID; preflight rejects empty, non-socket, missing group `rw`, and `0666` modes.
- [x] Migrate every `qa-e2e/` path that starts a real State Registry or Executor host binary to the image lifecycle fixtures. Build images before each suite; create a fresh service container or Pod per test; route requests only through its published/test-exposed port; remove it in teardown. Test-only dependency fakes may remain in-process.
  - Verify: `executor_binary.ts` and its compiled-binary test were removed; consumers use `executor_container.ts`; `registry_worker.ts` now owns a production Registry image plus TLS PostgreSQL containers; static guard passes.
- [x] Run `npm --prefix qa-e2e/containerized-runtime-services test -- --grep "v0017.1"` until all three image tests and the host-binary guard pass.
  - GREEN: `4 passed (1.5m)` with no skip.

## 4. REFACTOR and completion

- [x] Remove obsolete E2E host-binary builders and imports after every consumer uses the container fixtures; retain host execution only in unit or non-E2E developer workflows.
- [x] Reuse only root test orchestration helpers; keep each production Containerfile, runtime configuration, and duplicated service wire code inside its owning service.
- [x] Fill the moved `qa-e2e/test-cases/v0017.1-runtime-services-use-owned-container-images.md` with the runnable file, exact test titles, image IDs, RED failure evidence, and GREEN pass command/output.
- [x] Run `go test ./...`, every affected existing Playwright suite, `npm --prefix qa-e2e/containerized-runtime-services test`, and `npx -y @fission-ai/openspec@1.5.0 validate --all --strict --no-interactive`; require no skipped v0017 test and no real-service host launch under `qa-e2e/`.
  - Verify: `go test ./...` passed; the containerized-runtime suite passed `4 passed (3.2m)` with no skips; OpenSpec strict validation passed `16 passed, 0 failed`; the host-launch and skipped-test searches returned no matches; `git diff --check` passed. The State Registry suite exercised all 125 tests (`124 passed` plus the timestamp-precision regression, then that exact regression passed focused); its real Docker Mission Control smoke passed. K8s exercised all 17 tests, and the final corrected runtime/reconcile/UI subset passed `6/6` across the last two focused runs (`5 passed`, then `v0005.9` passed); all remaining K8s cases passed in the full-suite runs. No change was archived.
  - Shared-cluster refactor: `npm --prefix qa-e2e test` now owns exactly one k3d cluster for the complete E2E run and passes its name and kubeconfig to both K8s-capable suites. A sequential proof against the same cluster passed the v0017 K8s image test (`1 passed (32.4s)`) and the existing K8s Executor registration test (`1 passed (1.2m)`); `k3d cluster list` was empty afterward. Direct package runs retain their isolated-cluster fallback.
