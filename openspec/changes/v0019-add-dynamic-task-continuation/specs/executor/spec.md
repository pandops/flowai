## ADDED Requirements

### Requirement: Compatible Executors capture provider continuation context

A compatible concrete Executor SHALL use a versioned adapter for `opencode`,
`codex`, or `claude_code`. On explicit continuation, the adapter SHALL capture
the stable provider session ID and only allow-listed portable session artifacts,
produce size and digest metadata, upload the bundle through State Registry, and
submit its metadata with the parent's stable `finished` event. It SHALL NOT
parse logs or create a child task directly.

#### Scenario: OpenCode context is captured

- **WHEN** OpenCode explicitly requests continuation
- **THEN** its adapter captures the documented session export as the opaque bundle and submits provider `opencode`

#### Scenario: Codex context is captured

- **WHEN** Codex explicitly requests continuation
- **THEN** its adapter records the resumable session ID and allow-listed session artifacts and submits provider `codex`

#### Scenario: Claude Code context is captured

- **WHEN** Claude Code explicitly requests continuation
- **THEN** its adapter records the JSON-result `session_id` and allow-listed session artifacts and submits provider `claude_code`

#### Scenario: Completion response is lost

- **WHEN** an Executor cannot determine whether the continuation-bearing completion committed
- **THEN** it retries the same event identifier and metadata and starts no child without a successful later claim

### Requirement: Compatible Executors resume only verified matching context

After a successful child claim, the assigned Executor SHALL download context
only through the claim-bound handle, verify provider, adapter version, size,
digest, and safe archive structure, restore it into a fresh workspace, and
invoke the matching provider's explicit resume-by-session interface. It SHALL
not log context, session identity, or storage locator.

#### Scenario: OpenCode child resumes

- **WHEN** an OpenCode-compatible Executor claims a child with verified `opencode` context
- **THEN** it imports the session bundle and continues the recorded session in one fresh runtime

#### Scenario: Codex child resumes

- **WHEN** a Codex-compatible Executor claims a child with verified `codex` context
- **THEN** it restores the bundle and resumes the recorded Codex session by ID in one fresh runtime

#### Scenario: Claude Code child resumes

- **WHEN** a Claude-Code-compatible Executor claims a child with verified `claude_code` context
- **THEN** it restores the bundle and resumes the recorded Claude Code session by ID in one fresh runtime

#### Scenario: Provider or format mismatches

- **WHEN** the claimed provider is unsupported or the bundle version, digest, or structure is invalid
- **THEN** the Executor starts no provider process, emits one content-free `failed` event, and exposes no session data

#### Scenario: Ordinary task remains unchanged

- **WHEN** a claim contains no continuation relationship or context handle
- **THEN** the Executor follows its existing ordinary startup path
