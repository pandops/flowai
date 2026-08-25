## Why

Operators need to take over cancelled or failed OpenHands work in a local
coding agent, inspect the preserved workspace, and continue or repair it
manually. OpenHands sessions cannot be resumed natively by OpenCode, Codex, or
Claude Code because their session formats and identifiers are incompatible.

## What Changes

- Preserve one immutable handoff source for every eligible `cancelled`,
  `goal_failed`, or `runtime_failed` OpenHands task.
- Capture one bounded session/workspace source after OpenHands accepts a
  cancellation and before cleanup, or before cleanup of Goal/runtime failures.
- Let a same-team operator or system administrator request a target-specific
  manual handoff package for `opencode`, `codex`, or `claude_code`.
- Package the verified workspace, content-safe task/session summary, failure or
  cancellation/failure context, visible execution evidence, and target-specific bootstrap
  instructions; exclude credentials, secrets, hidden reasoning, raw judge
  prompts, storage locators, and provider-private OpenHands state that the
  target cannot consume safely.
- Download the package through API Gateway or the admin path, extract it in a
  user-selected local directory, and start a new target-tool session for manual
  continuation or repair.
- Keep the source task and lifecycle immutable. Manual work after download is
  outside FlowAI execution, assignment, audit, and synchronization.
- Add idempotent export generation, authorization, integrity metadata,
  retention, range download, and Web UI actions for all three targets.

## Capabilities

### New Capabilities

- `terminal-session-handoff`: Target-specific manual handoff of cancelled and
  failed OpenHands work to OpenCode, Codex, or Claude Code.

### Modified Capabilities

- `state-registry`: Own handoff-source metadata, target-package generation,
  authorization, retention, audit, and streaming.
- `executor`: Capture a safe portable handoff source before failed-runtime
  cleanup.
- `web-ui`: Let authorized operators generate and download a manual handoff for
  one selected target tool.

## Impact

State Registry schema, private storage and APIs; API Gateway streaming;
Docker/K8s OpenHands cancellation/cleanup; v0020 failure classification;
target templates for OpenCode/Codex/Claude Code; Web UI task detail; retention;
audit; configuration; and container-native E2E tests are affected.
