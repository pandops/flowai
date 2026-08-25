## ADDED Requirements

### Requirement: State Registry owns handoff sources and packages

State Registry SHALL persist at most one immutable source per eligible task and
immutable derived packages keyed by source task, target provider, actor scope,
and idempotency key. It SHALL store private objects outside the database with
versions, digests, byte/file counts, source kind, target provider, status,
timestamps, retention, and content-free failure reasons. Identical retries
SHALL return the original result; conflicting retries SHALL be rejected.

#### Scenario: Cancelled source is committed

- **WHEN** the assigned Executor commits a verified source after a cancellation control completes
- **THEN** State Registry records one `cancelled` handoff source without changing the control or canonical task lifecycle

#### Scenario: Target package retry is idempotent

- **WHEN** an actor repeats the same target request and idempotency key
- **THEN** State Registry returns the original package status and identifier without regenerating it

### Requirement: State Registry generates target-specific manual packages

For an available source, State Registry SHALL validate source integrity and
content policy before rendering a fixed versioned package for exactly one of
`opencode`, `codex`, or `claude_code`. It SHALL include no executable launcher,
native target session ID, provider credentials, or claim that the target can
resume OpenHands history. Generation SHALL be atomic: only a complete verified
package becomes downloadable.

#### Scenario: Codex package is generated

- **WHEN** an authorized request selects `codex`
- **THEN** State Registry creates a verified package whose `START.md` explains manual startup of a new Codex session from the extracted workspace and handoff document

#### Scenario: Unsupported target is requested

- **WHEN** a request names any provider outside the three-value target enum
- **THEN** State Registry rejects it without reading source bytes or creating package metadata

### Requirement: State Registry authorizes generation and download

State Registry SHALL allow package generation and content access only to an
authenticated same-team operator through trusted Gateway context or an
authenticated system administrator through the admin path. Authorization SHALL
precede source/package lookup, storage reads, generation, range processing, and
response shaping. Listener and Executor identities SHALL NOT use manual handoff
APIs. Foreign and unknown task IDs SHALL share one non-revealing response.

#### Scenario: Same-team operator downloads package

- **WHEN** trusted Gateway context requests an available package for its task's team
- **THEN** State Registry streams it through Gateway with a sanitized target-specific filename and content-free audit

#### Scenario: Foreign operator probes handoff

- **WHEN** an operator names another team's task, package, target, or byte range
- **THEN** State Registry reveals no source/package status, size, target, expiry, timing, cursor, or existence signal

### Requirement: State Registry retains and streams packages safely

State Registry SHALL apply configured source/package retention, stream without
whole-object buffering, support at most one
valid byte range, and never expose or redirect to a private locator. Invalid
ranges SHALL be rejected before object reads. Expired packages MAY be regenerated
only from an available source under a new idempotency key.

#### Scenario: Download resumes by range

- **WHEN** an authorized caller requests one valid range of an available package
- **THEN** State Registry returns exactly that range with no locator or credential

#### Scenario: Cancelled source expires

- **WHEN** a cancelled source and every derived package pass retention with no active stream
- **THEN** State Registry deletes their private objects and preserves content-free metadata without changing cancellation history
