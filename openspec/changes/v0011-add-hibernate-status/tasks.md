## 1. Hibernate Contract and Authorization

- [ ] 1.1 Add RED tests for same-team operator/admin hibernate, foreign-team non-disclosure, non-user denial, audit, and idempotency.
- [ ] 1.2 Extend State Registry lifecycle, controls, OpenAPI, and projections with independent terminal `hibernate` and in-progress control state.
- [ ] 1.3 Add RED tests proving hibernate stops execution immediately and never retains runtime resources for direct continuation.

## 2. Durable Session Storage

- [ ] 2.1 Add Registry archive metadata, private durable-storage adapter, encryption/configuration, resumable partial-upload cleanup, and strict format/size/count limits.
- [ ] 2.2 Implement authenticated Executor upload with digest verification and atomic `hibernate` projection only after durable commit.
- [ ] 2.3 Implement failure handling that projects `failed`, releases resources, and never exposes a partial archive as recoverable.

## 3. Later Runs

- [ ] 3.1 Add RED tests for later run with new query, `hibernated_from_task_id`, retry idempotency, non-hibernate rejection, and cross-team denial.
- [ ] 3.2 Implement the operator/admin later-run API and creation of one ordinary pending continuation task.
- [ ] 3.3 Implement opaque bound archive handles and assigned-Executor-only verified download streaming.

## 4. Executor and UI

- [ ] 4.1 Implement immediate stop, safe archive creation/upload, runtime removal, and capacity release for Docker and planned K8s Executors.
- [ ] 4.2 Implement safe restore to a fresh workspace and submission of the new query for a claimed continuation task.
- [ ] 4.3 Update Gateway allowlists/pass-through fixtures and Web UI hibernate/later-run controls without exposing archive bytes.

## 5. End-to-End Verification

- [ ] 5.1 Add E2E coverage: run task, hibernate, verify runtime release and archive, start later run with new query, restore session, and finish.
- [ ] 5.2 Add failure E2E coverage for corrupt/partial archives, duplicate actions, non-hibernated later run, and cross-team access.
- [ ] 5.3 Run migration, Go, security, OpenAPI, Playwright, and strict full OpenSpec validation.
