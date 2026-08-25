## ADDED Requirements

### Requirement: State Registry atomically commits provider continuation

State Registry SHALL accept an optional continuation envelope only on a valid
`finished` event from the authenticated Executor assigned to the parent. The
envelope SHALL identify `opencode`, `codex`, or `claude_code`, provider session,
adapter version, byte size, and digest. In the terminal-event transaction,
Registry SHALL record one stable continuation decision and, when valid, create
exactly one unassigned `pending` child with immutable parent, root, provider,
and depth metadata. Retrying the same `event_id` SHALL return the original
outcome without another event or child.

#### Scenario: Completion and child commit together

- **WHEN** the assigned Executor appends a valid continuation-bearing `finished` event after uploading its verified provider bundle
- **THEN** State Registry atomically finishes the parent, commits the context metadata, and creates exactly one pending child

#### Scenario: Completion is retried

- **WHEN** the assigned Executor retries the same terminal event
- **THEN** State Registry returns the committed decision and child ID without duplication

#### Scenario: Unauthorized caller submits context

- **WHEN** a browser, listener, unassigned Executor, or foreign-team Executor submits continuation
- **THEN** State Registry applies its existing non-revealing denial and creates no event, context, relationship, child, or existence signal

### Requirement: State Registry preserves continuation authority

State Registry SHALL derive the child's immutable team, task type, source
system, required tag, environment reference, provider, image inputs, and other
launch inputs from the parent. It SHALL generate a continuation-specific
`source_id` from the parent task ID and SHALL reject every continuation field
that attempts to select authority, scheduling, assignment, or a different
provider. The child SHALL enter ordinary FIFO discovery with its own committed
`ingested_at` and `task_id`.

#### Scenario: Context cannot redirect a child

- **WHEN** a continuation envelope names a different team, provider, task type, source system, tag, image, environment, Executor, or command
- **THEN** State Registry finishes the valid parent, records a rejected decision, and creates no child

#### Scenario: Child uses normal FIFO

- **WHEN** a child commits while older eligible pending tasks exist
- **THEN** it is discovered and claimed only under existing eligibility and FIFO `(ingested_at ASC, task_id ASC)` rules

#### Scenario: Source dedupe cannot collide

- **WHEN** a child inherits its parent's source system
- **THEN** its server-generated continuation source ID does not reuse the parent's external dedupe key

### Requirement: State Registry bounds and contains provider context

State Registry SHALL validate envelope and adapter versions, provider identity,
committed bundle digest, configured byte/file limits, and maximum depth before
child creation. An invalid continuation SHALL NOT undo a valid `finished` event:
Registry SHALL record a stable rejected decision and create no child. Context
bytes, provider session IDs, and private locators SHALL NOT appear in discovery,
task summaries, WebSocket frames, logs, audit records, error bodies, counts,
cursors, or relationship projections.

#### Scenario: Bundle exceeds a limit

- **WHEN** a completion references a provider bundle beyond a configured limit
- **THEN** State Registry commits `finished`, records a content-free rejected decision, schedules partial data for deletion, and creates no child

#### Scenario: Unsupported provider or version

- **WHEN** a completion names an unsupported provider, envelope version, or adapter version
- **THEN** State Registry commits `finished`, records rejection, and creates no child

#### Scenario: Authorized relationship read omits private context

- **WHEN** a same-team operator reads a related task or receives its committed-task frame
- **THEN** State Registry may return parent, root, depth, and provider but returns no session ID, bytes, digest, or locator

### Requirement: State Registry serves context only through a claim-bound handle

After a continuation child is successfully claimed, State Registry SHALL issue
the assigned compatible Executor a short-lived opaque handle bound to child,
parent, root, team, Executor, command, provider, adapter version, byte size, and
digest. Registry SHALL stream the private bundle only after verifying every
binding and the committed digest. Discovery, Gateway, operator, listener, and
other Executor paths SHALL receive no bundle or locator.

#### Scenario: Assigned Executor downloads context

- **WHEN** the assigned Executor presents an unexpired handle whose canonical bindings match
- **THEN** State Registry streams the digest-verified provider bundle and records a content-free audit event

#### Scenario: Claim retry remains idempotent

- **WHEN** the original Executor retries the same `(task_id, command_id)` claim
- **THEN** State Registry returns an equivalent bound handle without changing assignment or context

#### Scenario: Handle replay fails closed

- **WHEN** another identity presents the handle or any canonical binding differs
- **THEN** State Registry returns no bundle, session identity, locator, timing, or existence signal

#### Scenario: Discovery omits context

- **WHEN** a team-owned or system-owned Executor discovers a pending continuation child
- **THEN** its summary contains no provider session ID, bundle data, digest, or payload-derived value
