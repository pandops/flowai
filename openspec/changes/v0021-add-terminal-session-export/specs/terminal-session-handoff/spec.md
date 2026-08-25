## ADDED Requirements

### Requirement: Every eligible source supports every manual target

FlowAI SHALL support the Cartesian product of source kinds `cancelled`,
`goal_failed`, and `runtime_failed` with target providers `opencode`, `codex`,
and `claude_code` whenever the portable source is available. One combination
SHALL NOT mutate, consume, or disable another combination.

#### Scenario: Cancelled work is handed to Codex

- **WHEN** an authorized operator selects Codex for a session whose cancellation control completed
- **THEN** FlowAI produces a Codex-targeted manual package without changing the cancellation or task lifecycle outcome

#### Scenario: Goal failure is handed to Claude Code

- **WHEN** an authorized operator selects Claude Code for an available Goal-failed task
- **THEN** FlowAI produces a Claude-Code-targeted manual repair package and leaves the source failed

#### Scenario: Runtime failure is handed to OpenCode

- **WHEN** an authorized operator selects OpenCode for an available runtime-failed task
- **THEN** FlowAI produces an OpenCode-targeted manual repair package and leaves the source failed

### Requirement: Handoff starts a new target-tool session

A handoff package SHALL contain a portable workspace and target-specific
bootstrap context for a new local session. It SHALL NOT claim or attempt to
resume, import, or forge the original OpenHands session as a native OpenCode,
Codex, or Claude Code session.

#### Scenario: Operator starts manual work

- **WHEN** the operator extracts a package, opens its workspace in the selected tool, and supplies the handoff document as initial context
- **THEN** the selected tool starts a new local session from the preserved work and the operator may continue or repair it manually

#### Scenario: Native session identity is incompatible

- **WHEN** an OpenHands conversation ID is present in private source metadata
- **THEN** the package does not present it as a target provider session ID or resume token

### Requirement: Manual work remains outside FlowAI lifecycle

After package download, FlowAI SHALL create no task, Executor assignment,
continuation relationship, target session, local credential, or synchronization
channel. Local manual changes SHALL NOT alter the source task, archive, events,
audit history, or terminal lifecycle.

#### Scenario: Operator repairs files locally

- **WHEN** the operator changes the extracted workspace using OpenCode, Codex, or Claude Code
- **THEN** FlowAI records no task transition and does not import or merge those changes automatically

### Requirement: Handoff content is explicit and safe

Every package SHALL include source kind, task/objective summary, visible
evidence, workspace, checksums, target instructions, and an omissions list. It
SHALL exclude secret plaintext, credentials, hidden reasoning, raw Goal judge
prompts, private storage locators, unsafe paths, links, and devices.

#### Scenario: Package is inspected before use

- **WHEN** an operator opens `FLOWAI_HANDOFF.md` and `START.md`
- **THEN** the files identify provenance, target, manual startup steps, known cancellation/failure context, and omitted sensitive categories

#### Scenario: Unsafe source entry is found

- **WHEN** package generation encounters a forbidden path, link, device, digest mismatch, or configured limit violation
- **THEN** generation fails closed and returns no partial downloadable package
