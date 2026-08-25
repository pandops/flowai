## Context

Tasks end when their assigned Executor appends `finished` or `failed`.
OpenCode, Codex, and Claude Code each expose resumable sessions, but their
session identifiers and persisted formats differ. FlowAI needs one canonical
continuation contract without interpreting or converting provider-private data.

Official interfaces establish the adapter boundary: OpenCode exports/imports
session JSON; Codex resumes a saved session by ID; Claude Code JSON output
exposes `session_id` and resumes it by ID. A fresh runtime also needs the
allow-listed provider artifacts behind that ID, so an ID alone is not treated as
portable context or authority.

## Goals / Non-Goals

**Goals:**

- Receive resumable context from `opencode`, `codex`, or `claude_code`.
- Atomically create at most one pending child after successful completion.
- Preserve provider context durably, idempotently, and tenant-safely.
- Resume the same provider session in a fresh compatible runtime.

**Non-Goals:**

- Convert a session between providers.
- Let provider output choose tenancy, scheduling, image, or assignment fields.
- Add a workflow engine, Router, broker, branching fan-out, or live-runtime
  retention.
- Infer continuation from logs, prose, or hidden reasoning.
- Replace v0010 live `continue` or v0011 OpenHands hibernate/later-run recovery.

## Decisions

1. **Adapters normalize metadata, not context.** Each provider adapter emits
   envelope version, provider, provider session ID, adapter format version,
   bytes, size, and digest. Registry treats bytes as opaque. OpenCode uses its
   documented export/import JSON boundary. Codex and Claude Code adapters
   package only allow-listed local session artifacts needed with their
   documented resume-by-ID mechanisms.
2. **Continuation is explicit terminal output.** Only a valid `finished` append
   from the assigned Executor may carry the envelope. Logs and arbitrary model
   text never trigger a child.
3. **Child creation is in the terminal-event transaction.** The transaction
   persists the parent event, continuation decision and metadata, and exactly
   one pending child. Retrying the same `event_id` returns the same outcome.
4. **Invalid continuation does not undo successful work.** Registry finishes
   the parent, records a stable rejected decision, and creates no child for an
   unsupported provider/version, invalid digest, excessive size/depth, or
   authority-bearing field.
5. **The child inherits authority, not assignment.** Team, task type, source
   system, tag, environment, provider, and launch inputs come from the parent.
   Registry generates a continuation-specific `source_id`. The child has a new
   `task_id`, no Executor/command, a fresh `ingested_at`, and normal FIFO claim.
6. **Context is private blob data.** Registry stores provider, session ID,
   format, digest, size, and private locator. A successful claim returns only a
   short-lived handle bound to child, parent, root, team, Executor, command,
   provider, format, size, and digest. Only the assigned compatible Executor can
   stream the verified bytes. No browser, Gateway, listener, discovery, log, or
   audit surface receives bytes, session ID, or locator.
7. **Chains are linear and bounded.** Root depth is zero; child depth is parent
   depth plus one. Registry enforces one child per parent and configured maximum
   bytes/depth.
8. **Resume occurs in a fresh runtime.** The Executor verifies the bundle,
   restores allow-listed files into a fresh workspace, and invokes only the
   matching provider's explicit session-resume interface. A version mismatch
   fails the child without best-effort conversion.
9. **The first bindings are three Docker concrete Executors.** Following the
   one-directory-per-service and concrete naming rules, implementation adds
   self-contained `executor_docker_opencode`, `executor_docker_codex`, and
   `executor_docker_claudecode` services. `Claude Code` remains the product
   spelling; `claudecode` is the lowercase alphanumeric tool token. Kubernetes
   bindings require a later explicit change instead of sharing adapter code.

## Risks / Trade-offs

- [Context contains secrets] → Use private durable storage, claim-bound
  transfers, content-free logs/audit, and configured retention.
- [Automatic loop consumes capacity] → Enforce one child per parent and an
  immutable Registry-owned depth limit.
- [Provider format changes] → Version adapters, pin provider versions in images,
  and fail closed on mismatch.
- [Completion retry duplicates work] → Couple creation to the idempotent
  terminal event and a unique parent relationship.
- [Archive extraction is unsafe] → Reject traversal, links, devices, unexpected
  files, digest mismatch, and configured file/byte limits before resume.

## Migration Plan

1. Add relationship columns, private context metadata/storage, constraints, and
   disabled size/depth configuration.
2. Deploy Registry completion, upload/download, and claim-handle support.
3. Deploy the three Docker concrete Executors with duplicated, service-owned,
   versioned OpenCode, Codex, and Claude Code capture/resume adapters.
4. Enable conservative limits and run provider-specific cross-service E2E tests.
5. Roll back by disabling new child creation; existing children remain ordinary
   tasks and committed parent history remains readable.

## Open Questions

None.
