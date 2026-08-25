## 1. Contracts and E2E definitions

- [ ] 1.1 Add implementation-active test-case definitions with `v0019.*` IDs for OpenCode, Codex, and Claude Code capture/resume, atomic continuation creation, retries, limits, cross-provider rejection, tenancy, FIFO, and context secrecy.
- [ ] 1.2 Update State Registry and generic Executor OpenAPI contracts with continuation metadata, upload/download operations, terminal decision, relationship projection, and claim-bound context handle.
- [ ] 1.3 Pin regression tests for wire names `executor_docker_opencode`, `executor_docker_codex`, and `executor_docker_claudecode` and provider values `opencode`, `codex`, and `claude_code`.

## 2. State Registry persistence and storage

- [ ] 2.1 Add RED migration/integration tests for immutable parent/root/provider/depth fields, unique one-child-per-parent constraint, generated continuation source IDs, opaque bundle metadata, and rollback compatibility.
- [ ] 2.2 Implement Registry migrations and repository types for continuation relationships, stable decisions, provider bundle metadata, and private locators.
- [ ] 2.3 Add RED tests for resumable upload, digest and byte/file limits, unsupported provider/version, partial cleanup, and absence of bytes/session IDs/locators from logs and audit.
- [ ] 2.4 Implement the private durable-context storage adapter, configured limits, verified commit, abort, and retention cleanup inside `svc/state-registry/`.

## 3. Atomic completion and child intake

- [ ] 3.1 Add RED tests proving assigned-Executor-only continuation, atomic parent finish plus child creation, same-event retry idempotency, conflicting retry rejection, and valid finish with stable rejected continuation.
- [ ] 3.2 Implement terminal-event validation and one-transaction continuation decision, relationship persistence, and pending child creation.
- [ ] 3.3 Add RED tests proving inherited team/task-type/source-system/tag/environment/provider/image inputs, generated source dedupe identity, fresh `ingested_at`, and unchanged FIFO eligibility.
- [ ] 3.4 Implement canonical inheritance and content-free authorized relationship projections and committed-task frames.

## 4. Claim-bound context delivery

- [ ] 4.1 Add RED tests for compatible claim handles bound to child/parent/root/team/Executor/command/provider/version/size/digest, idempotent claim retry, expiry, replay, and non-revealing denials.
- [ ] 4.2 Implement handle issuance and assigned-Executor-only digest-verified context streaming without exposing the private locator.
- [ ] 4.3 Verify discovery, admin summaries, Gateway reads, WebSocket frames, errors, logs, and audit never contain context bytes or provider session IDs.

## 5. Docker OpenCode Executor

- [ ] 5.1 Scaffold self-contained `executor/docker_opencode/` with production `Containerfile`, command/config, duplicated platform/http/logging/client code, and wire-value regression tests.
- [ ] 5.2 Add RED adapter tests for documented OpenCode JSON session export/import, explicit continuation only, safe bundle allow-list, digest, retry, and context-free logging.
- [ ] 5.3 Implement OpenCode capture/upload and fresh-runtime claim/download/import/resume behavior, then keep targeted unit and integration tests green.

## 6. Docker Codex Executor

- [ ] 6.1 Scaffold self-contained `executor/docker_codex/` with production `Containerfile`, command/config, duplicated service internals, and wire-value regression tests.
- [ ] 6.2 Add RED adapter tests for stable Codex session ID capture, allow-listed portable artifacts, explicit `codex resume` behavior, mismatch handling, and context-free logging.
- [ ] 6.3 Implement Codex capture/upload and fresh-runtime claim/download/restore/resume behavior, then keep targeted unit and integration tests green.

## 7. Docker Claude Code Executor

- [ ] 7.1 Scaffold self-contained `executor/docker_claudecode/` with production `Containerfile`, command/config, duplicated service internals, and wire-value regression tests.
- [ ] 7.2 Add RED adapter tests for JSON-result `session_id`, allow-listed portable artifacts, explicit Claude Code resume-by-ID behavior, mismatch handling, and context-free logging.
- [ ] 7.3 Implement Claude Code capture/upload and fresh-runtime claim/download/restore/resume behavior, then keep targeted unit and integration tests green.

## 8. Cross-service verification

- [ ] 8.1 Add container-native Playwright suites under `qa-e2e/executor_docker_opencode/`, `qa-e2e/executor_docker_codex/`, and `qa-e2e/executor_docker_claudecode/` covering two-step continuation and ordinary stop.
- [ ] 8.2 Add adversarial E2E cases for duplicate completion, oversized/tampered/unsafe bundle, maximum depth, cross-provider claim, foreign-team access, expired/replayed handle, process restart, and private-data leakage.
- [ ] 8.3 Build and run each service exclusively from its production image and verify existing State Registry and Executor suites remain green.
- [ ] 8.4 Run `npx -y @fission-ai/openspec@1.5.0 validate v0019-add-dynamic-task-continuation --strict` and `npx -y @fission-ai/openspec@1.5.0 validate --all --strict`.
