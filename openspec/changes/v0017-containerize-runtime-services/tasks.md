## 1. RED — pin container-only runtime behavior

- [ ] Add image-identity assertions and host-process rejection to `v0017.1`.
- [ ] Run the targeted State Registry, Docker Executor, and K8s Executor suites
      and record behavior-specific RED failures.

## 2. GREEN — service-owned production images

- [ ] Add `svc/state-registry/Containerfile` with a non-root runtime image.
- [ ] Add `executor/docker_openhands/Containerfile` with explicit runtime
      socket configuration.
- [ ] Replace or rename the K8s Executor build definition with
      `executor/k8s-openhands/Containerfile` and preserve its Pod contract.
- [ ] Change integration and cross-service E2E harnesses to build and start the
      exact service-owned images.
- [ ] Run `v0017.1` until State Registry and both Executor paths pass.

## 3. REFACTOR and completion

- [ ] Reuse only root test orchestration helpers; keep production build and
      runtime configuration inside each owning service.
- [ ] Record image identities and RED/GREEN evidence in `v0017.1`.
- [ ] Run full Go, integration, Playwright, and strict OpenSpec validation.
