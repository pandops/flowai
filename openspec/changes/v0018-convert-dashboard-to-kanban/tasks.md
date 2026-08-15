## 1. Start implementation and establish RED

- [ ] 1.1 Move all `v0018.*` definitions from this change into
      `qa-e2e/test-cases/` before editing production code; preserve their IDs and
      `Supersedes` pointers.
- [ ] 1.2 Build every service image used by the E2E suites from the current
      source and configure tests to address services only through published
      container ports; do not start repository binaries on the host.
- [ ] 1.3 Add
      `qa-e2e/web-ui/tests/v0018-kanban-dashboard.spec.ts` with exact tests
      `v0018.1 dashboard renders one card in its canonical status column`,
      `v0018.3 reconnect and team switch rebuild an isolated board`, and
      `v0018.4 kanban is read-only responsive and keyboard accessible`.
- [ ] 1.4 Add
      `qa-e2e/executor_k8s_openhands/tests/v0018-kanban-live.spec.ts` with exact
      test `v0018.2 live lifecycle events move one card exactly once`; run it only
      against freshly built images in a fresh k3d environment and stop that
      environment after the test.
- [ ] 1.5 Add `qa-e2e/web-ui/tests/v0018-statistics.spec.ts` with exact test
      `v0018.5 statistics preserves the former live calendar dashboard`.
- [ ] 1.6 Run the exact tests and record expected RED failures before
      production edits:
      `npm --prefix qa-e2e/web-ui test -- --grep "v0018\\."` and
      `npm --prefix qa-e2e/executor_k8s_openhands test -- --grep "v0018\\."`.

## 2. Move the existing dashboard to Statistics

- [ ] 2.1 Add separate `Dashboard` and `Statistics` navigation items and route
      the unchanged calendar composition to `/statistics`.
- [ ] 2.2 Preserve week/month selection, URL and browser-history state, totals,
      status counts, chart, zero-count buckets, keyboard date drill-down, and
      exactly-once same-team live updates on Statistics.
- [ ] 2.3 Ensure route changes close or generation-fence the inactive live
      projection and that the existing calendar backend contract stays unchanged.

## 3. Implement the bounded read-only Kanban

- [ ] 3.1 Replace the `/dashboard` calendar/chart composition in `svc/web-ui/`
      with ordered lifecycle columns and remove calendar controls from that route.
- [ ] 3.2 Add immutable-`task_id` card projection, canonical card metadata,
      accessible task-detail links, per-status totals, 25-card initial windows,
      and independent cursor pagination/loading/error/empty states.
- [ ] 3.3 Ensure no board component exposes drag/drop or sends a lifecycle
      mutation request; keep existing explicit controls on their established
      authorized surfaces.
- [ ] 3.4 Replace route-specific runnable v0006 dashboard assertions with
      `v0018.1` and `v0018.5` without deleting historical test-case files or
      dropping the calendar behavior now verified on Statistics.

## 4. Implement live projection and isolation

- [ ] 4.1 Establish the selected-team stream boundary before status snapshots,
      subscribe from `after`, and apply replay/live frames in cursor order.
- [ ] 4.2 Deduplicate by cursor and `task_id`; move a card and adjust source and
      destination totals atomically while preserving bounded column windows.
- [ ] 4.3 Resume after the last applied cursor; when continuity cannot be
      proved, discard the projection and rebuild from a new boundary and snapshot.
- [ ] 4.4 On selected-team change, abort or generation-fence old requests and
      subscriptions, clear cards/cursor, and reject every stale or foreign-team
      response and frame.

## 5. Complete responsive and accessible behavior

- [ ] 5.1 Preserve lifecycle order on wide and narrow viewports, add usable
      horizontal column navigation, and prevent cards from collapsing below their
      usable width.
- [ ] 5.2 Expose status names/counts and descriptive card links to assistive
      technology; retain deterministic focus order and announce live moves without
      stealing focus.
- [ ] 5.3 Respect reduced-motion preferences and verify that no animation is
      required to understand a state change.

## 6. Reach GREEN and verify the change

- [ ] 6.1 Run `npm --prefix qa-e2e/web-ui test -- --grep "v0018\\."` until all
      exact Web UI tests are GREEN; each test SHALL start a new service container
      from the prebuilt image and stop it afterward.
- [ ] 6.2 Run
      `npm --prefix qa-e2e/executor_k8s_openhands test -- --grep "v0018\\."` until
      the exact integration test is GREEN; each test SHALL use a fresh k3d
      workload from prebuilt images and tear it down afterward.
- [ ] 6.3 Run all affected service unit/integration suites and the complete
      browser E2E suites with no repository binary executed on the host.
- [ ] 6.4 Run
      `npx -y @fission-ai/openspec@1.5.0 validate v0018-convert-dashboard-to-kanban --strict`,
      `npx -y @fission-ai/openspec@1.5.0 validate --all --strict`,
      `git diff --check`, and `npm run precommit`.
