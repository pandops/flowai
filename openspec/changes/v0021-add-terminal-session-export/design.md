## Context

The current cancellation flow records a completed control separately from task
lifecycle, then terminates through an existing terminal lifecycle state. Failed
Goal/runtime tasks also lose their workspace at cleanup. Operators want to move
any of these stopped sessions into OpenCode, Codex, or Claude Code and continue
or repair the work manually.

The target tools cannot import an OpenHands conversation as the same native
session. OpenCode imports its own session JSON, while Codex and Claude Code
resume their own saved session IDs. Therefore the interoperable boundary is a
new target session bootstrapped from a portable workspace and a content-safe
handoff document, not fabricated native history.

## Goals / Non-Goals

**Goals:**

- Support every source kind crossed with every target provider.
- Preserve workspace and useful visible evidence for manual takeover.
- Generate safe, deterministic, target-specific handoff packages.
- Keep authorization, integrity, retention, and source lifecycle intact.

**Non-Goals:**

- Claim that the target resumes the original OpenHands session ID.
- Automatically run a target Executor or create a FlowAI continuation task.
- Synchronize local edits back into FlowAI.
- Include secret plaintext, credentials, hidden reasoning, or raw Goal judge
  prompts.
- Make a failed FlowAI task non-terminal.

## Decisions

1. **One portable source per terminal task.** Registry stores at most one
   immutable source with `kind = cancelled | goal_failed | runtime_failed` and
   `status = capturing | available | unavailable | expired`. A cancelled source
   is captured after OpenHands accepts cancellation and before cleanup. Failure
   sources are captured once within hard limits before cleanup; capture failure
   never changes the control outcome or task lifecycle.
2. **Portable source is provider-neutral.** It contains an allow-listed
   workspace snapshot, versioned manifest, source task/objective metadata,
   content-safe cancellation/failure summary, visible work log excerpts and event
   evidence, checksums, and an explicit list of omitted material. It excludes
   OpenHands credentials, secrets, hidden reasoning, raw judge prompts, private
   locators, and unsafe archive entries.
3. **Each request selects one target.** Operator submits `target_provider` from
   `opencode | codex | claude_code` plus an idempotency key. Registry creates or
   returns one immutable derived package keyed by `(source_task_id,
target_provider, actor_scope, idempotency_key)` and never changes the source.
4. **Packages bootstrap a new manual session.** Each archive contains
   `workspace/`, `FLOWAI_HANDOFF.md`, `flowai-handoff.json`, checksums, and a
   target-specific `START.md`. Instructions tell the operator to inspect the
   package, open `workspace/`, start the selected tool, and provide the handoff
   document as the first prompt. No package contains an auto-executing script or
   claims native session import compatibility.
5. **Generation happens in State Registry.** Registry reads the verified
   private source, validates every path/digest/limit, renders a fixed
   target-specific instruction template, and stores the derived immutable
   package. This keeps Executors offline after cleanup and adds no service.
6. **Manual mode ends platform control.** After authorized download, the user
   extracts the package into a chosen local directory and works directly in the
   target tool. FlowAI creates no assignment/task, receives no local tool
   credentials, and does not observe, audit, or merge local changes.
7. **Downloads stay proxied.** Web UI calls API Gateway, which streams from
   Registry with backpressure and one validated byte range. Registry authorizes
   before source/package lookup, never redirects to object storage, uses a
   sanitized filename, and audits content-free metadata.
8. **Retention is independent of lifecycle.** Source and derived-package
   retention are configurable. Expiry never changes the completed cancellation
   control or source task. Expired derived packages may be regenerated only
   while the source remains available and a new idempotency key is used.

## Risks / Trade-offs

- [Handoff is lossy] → State explicitly that a new target session starts and
  include source provenance, workspace, visible evidence, and omissions.
- [Archive contains secrets] → Apply allow-lists, secret-pattern checks,
  content-free metadata, private storage, authorization, and retention.
- [Bootstrap prompt causes unsafe actions] → Make instructions informational,
  require user inspection, and include no executable launcher.
- [Failed capture delays cleanup] → Enforce hard timeout/size/file limits and
  proceed to failed cleanup after one attempt.
- [Target CLI changes] → Keep package contract independent of CLI flags and
  version target-specific human instructions separately.
- [Large package overloads Gateway] → Stream with backpressure and single-range
  resume without whole-object buffering.

## Migration Plan

1. Add handoff source/package schema; existing tasks have no source backfill.
2. Deploy Registry generation/streaming with target actions disabled.
3. Deploy Docker/K8s failed-source capture.
4. Enable OpenCode, Codex, and Claude Code packages and Web UI actions.
5. Roll back package generation/download first; retain private sources until
   their configured retention permits deletion.

## Open Questions

None.
