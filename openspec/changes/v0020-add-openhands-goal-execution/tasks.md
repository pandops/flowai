## 1. Contracts and test definitions

- [ ] 1.1 Add implementation-active E2E definitions with `v0020.*` IDs for Goal start, iterative progress, complete, capped, pause/resume, restart reconciliation, authorization, and content secrecy.
- [ ] 1.2 Update task-type registration, ingestion, task detail, claim, Goal-event append/read, stream-frame, and control OpenAPI contracts.
- [ ] 1.3 Pin the supported OpenHands agent-server Goal API and runtime image versions in Docker and K8s Executor contract tests.

## 2. State Registry Goal configuration

- [ ] 2.1 Add RED migration/integration tests for immutable task-type execution mode and cap, immutable task objective/effective cap, legacy `single` defaults, and rollback compatibility.
- [ ] 2.2 Implement schema, repository, and wire types for Goal task-type policy and task configuration inside `svc/state-registry/`.
- [ ] 2.3 Add RED ingestion tests for missing/empty/oversized objective, invalid cap, cap above policy, foreign task type, dedupe retry, and objective omission from discovery/admin summaries.
- [ ] 2.4 Implement validation, persistence, same-team detail projection, and assigned-claim delivery of Goal configuration.

## 3. State Registry Goal progress

- [ ] 3.1 Add RED tests for assigned-Executor-only Goal events, stable provider-event idempotency, legal status/iteration ordering, terminal immutability, and cap enforcement.
- [ ] 3.2 Implement Goal event append/read storage, atomic current projection, audit, opaque cursor replay, and same-team WebSocket frames.
- [ ] 3.3 Add adversarial tests proving objectives, raw verdicts, hidden reasoning, prompts, and credentials never enter discovery, admin summaries, errors, logs, audit, counts, or foreign responses.
- [ ] 3.4 Implement bounded verdict normalization and content-safe operator projection.

## 4. Docker OpenHands Goal execution

- [ ] 4.1 Add RED OpenHands-client tests for native Goal start/stop/resume endpoints, immediate start response, conflicts, validation errors, and unsupported endpoint behavior.
- [ ] 4.2 Implement the Goal API client and pinned Goal-capable runtime configuration in `executor/docker_openhands/`.
- [ ] 4.3 Add RED Executor tests for one conversation, no parallel ordinary run, running observation, progress mirroring, complete-to-finished, capped-to-failed, and no v0019 child.
- [ ] 4.4 Implement Docker Goal supervision and stable lifecycle mapping while preserving ordinary single-run behavior.

## 5. Controls and restart reconciliation

- [ ] 5.1 Add RED tests proving v0010 pause calls Goal stop, continue calls Goal resume before cleanup, deadline cleanup wins races, and interrupt remains permanent.
- [ ] 5.2 Implement Goal-aware v0010 control handling without using normal user messages or extending cleanup deadlines.
- [ ] 5.3 Add RED restart tests for persisted native Goal events in running, interrupted, complete, capped, missing, duplicate, and inconsistent histories.
- [ ] 5.4 Implement event-history reconciliation, provider-event dedupe, resumed supervision, and fail-closed handling without any `GET /goal` call or duplicate Goal start.

## 6. K8s OpenHands Goal parity

- [ ] 6.1 Add RED K8s Executor contract/E2E tests for the same Goal start, mapping, control, reconciliation, and cleanup behavior inside the Pod-owned runtime.
- [ ] 6.2 Duplicate the service-owned Goal client and supervision behavior under `executor/k8s-openhands/` without importing Docker Executor code.
- [ ] 6.3 Verify Goal-capable K8s Pods retain all existing labels, assignment checks, restart rules, and image-execution constraints.

## 7. Web UI and Gateway

- [ ] 7.1 Add RED Web UI tests for Goal configuration, live iteration updates, complete/capped presentation, refresh/replay, empty verdict, and separation from logs/lifecycle/control events.
- [ ] 7.2 Implement API Gateway allowlists and Web UI Goal task detail using only Gateway-to-Registry calls and same-team frames.
- [ ] 7.3 Add RED tests for Goal pause/continue availability, pending controls, cleanup-expired disablement, foreign-team isolation, and direct OpenHands/Executor call prohibition.
- [ ] 7.4 Implement Goal-aware control presentation without adding direct Goal endpoints to the browser.

## 8. Container-native verification

- [ ] 8.1 Extend the OpenHands test image with a deterministic Goal-capable agent-server and independent judge fixture that emits multiple audit rounds.
- [ ] 8.2 Run Docker cross-service E2E for complete, capped, pause/resume, restart, duplicate events, unsupported API, and content leakage using production service images only.
- [ ] 8.3 Run K8s k3d cross-service E2E for equivalent Goal behavior using built Pod images only.
- [ ] 8.4 Run all affected Go tests, Playwright suites, image builds, and OpenSpec strict validation for the change and full baseline.
