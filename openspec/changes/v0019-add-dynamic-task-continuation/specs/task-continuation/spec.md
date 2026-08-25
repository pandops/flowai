## ADDED Requirements

### Requirement: Supported coding agents may explicitly request continuation

FlowAI SHALL accept continuation context from provider adapters identified
exactly as `opencode`, `codex`, or `claude_code`. A request SHALL contain a
supported envelope version, provider session identity, adapter format version,
and one bounded opaque resume bundle. FlowAI SHALL NOT infer continuation from
logs, reasoning output, prose, or a failed result.

#### Scenario: Supported provider requests continuation

- **WHEN** OpenCode, Codex, or Claude Code finishes with a valid explicit continuation envelope
- **THEN** the assigned Executor submits the envelope with the idempotent `finished` event

#### Scenario: Ordinary completion stops

- **WHEN** a supported provider finishes without an explicit continuation envelope
- **THEN** FlowAI finishes the task and creates no continuation child

#### Scenario: Text is not a request

- **WHEN** output text asks to continue but the terminal provider result contains no continuation envelope
- **THEN** FlowAI treats the text only as output and creates no child

### Requirement: Continuation stays with one provider

Every child SHALL preserve its parent's provider and SHALL be claimed and
resumed only by a compatible concrete Executor. FlowAI SHALL NOT translate
OpenCode, Codex, or Claude Code context into another provider's format.

#### Scenario: OpenCode continues as OpenCode

- **WHEN** a child is created from OpenCode session context
- **THEN** its provider remains `opencode` and a compatible Executor restores it through the OpenCode adapter

#### Scenario: Cross-provider resume is rejected

- **WHEN** an Executor or envelope attempts to resume a provider session through a different provider adapter
- **THEN** FlowAI starts no provider process and exposes no context bytes

### Requirement: Continuation chains are fresh, linear, and bounded

Each accepted continuation SHALL create one new ordinary pending task with a
new identifier and immutable parent, root, provider, and depth metadata. Root
depth SHALL be zero and child depth SHALL be parent depth plus one. State
Registry SHALL enforce configured context-size and maximum-depth limits. The
child SHALL use a fresh runtime; only the provider session context is restored.

#### Scenario: Chain advances within limit

- **WHEN** a child requests another continuation below the configured maximum depth
- **THEN** FlowAI creates exactly one next pending child linked to the same root

#### Scenario: Maximum depth is reached

- **WHEN** a task at maximum depth requests continuation
- **THEN** FlowAI finishes the parent, records a rejected continuation decision, and creates no child

#### Scenario: Child runtime is fresh

- **WHEN** a compatible Executor claims a continuation child
- **THEN** it starts a new runtime and restores only the verified provider context instead of retaining the parent's runtime

### Requirement: Dynamic continuation is distinct from session controls

Dynamic continuation SHALL occur only after successful provider completion and
SHALL NOT alter v0010 pause/continue or v0011 OpenHands hibernate/later-run
semantics.

#### Scenario: Live continue remains same-task

- **WHEN** an operator continues a paused task under v0010
- **THEN** FlowAI uses the existing same-task behavior and creates no dynamic child

#### Scenario: Hibernated later run remains independent

- **WHEN** an operator starts a v0011 later run from a hibernated OpenHands task
- **THEN** FlowAI uses session recovery rather than provider-directed dynamic continuation
