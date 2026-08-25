## Why

OpenCode, Codex, and Claude Code can preserve resumable sessions, but FlowAI
cannot durably receive that provider-specific context and use it to continue a
task in a later run. An external caller must currently reconstruct the session
and enqueue the next step manually.

## What Changes

- Let adapters for `opencode`, `codex`, and `claude_code` return a versioned
  continuation envelope with provider identity, session identity, and bounded
  opaque resume context.
- Persist the successful terminal result and atomically create exactly one
  ordinary `pending` child linked to the finished parent.
- Keep provider context in private durable storage and expose it only through a
  short-lived handle bound to the successful child claim.
- Make the child inherit the parent's immutable team, task type, source system,
  required tag, environment reference, provider, and image-resolution inputs.
- Restore the matching provider context in a fresh runtime and invoke that
  provider's explicit resume-by-session mechanism.
- Bound chains by context-size and continuation-depth limits; reject
  cross-provider continuation and unsupported adapter formats.
- Expose only content-free parent/root/depth/provider metadata through
  authorized task reads and streams.
- Add self-contained Docker concrete Executor services
  `executor_docker_opencode`, `executor_docker_codex`, and
  `executor_docker_claudecode`; no adapter code is shared between services.

## Capabilities

### New Capabilities

- `task-continuation`: Bounded task chains resumed from portable context
  produced by OpenCode, Codex, or Claude Code.

### Modified Capabilities

- `state-registry`: Persist continuation metadata, atomically create linked
  children, protect opaque provider context, and issue claim-bound handles.
- `executor`: Capture and restore provider-specific OpenCode, Codex, or Claude
  Code context through versioned adapters.

## Impact

State Registry schema, private context storage, completion API, claim response,
authorized task projections; three new Docker concrete Executor services and
their production images; OpenAPI
contracts; configuration; migrations; and cross-service E2E tests are affected.
The connection matrix and FIFO claim rules remain unchanged.
