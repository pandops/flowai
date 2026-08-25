## ADDED Requirements

### Requirement: Web UI offers target-specific manual handoff

For a same-team task with an available handoff source from completed cancellation or failure, Web UI SHALL offer
exactly OpenCode, Codex, and Claude Code as manual targets. It SHALL show source
kind, availability, safe size/time metadata, package generation status, and a
clear notice that the target starts a new local session and FlowAI will not
track or synchronize manual work.

#### Scenario: Operator selects Claude Code

- **WHEN** an operator requests Claude Code handoff with an idempotency key
- **THEN** Web UI submits the authorized target request through API Gateway and shows generation progress without changing the source task

#### Scenario: Source is unavailable

- **WHEN** failed-session capture could not complete or the source expired
- **THEN** Web UI shows a content-free reason and offers no target generation action

### Requirement: Web UI downloads handoff only through API Gateway

Web UI SHALL download the complete target-specific package only through API
Gateway, preserve browser range resumption, and expose no source bytes, private
locator, digest, conversation ID, credential, or hidden reasoning. It SHALL NOT
call State Registry, an Executor, target tool, or object storage directly.

#### Scenario: Operator downloads OpenCode package

- **WHEN** an OpenCode-targeted package becomes available
- **THEN** Web UI downloads it with a sanitized filename and displays manual extract/open/bootstrap instructions

#### Scenario: Local manual session starts

- **WHEN** the operator follows `START.md` outside FlowAI
- **THEN** Web UI neither starts a platform task nor claims to observe, audit, or synchronize the local session
