## ADDED Requirements

### Requirement: OpenHands Executors capture portable failed-session sources

When an owned OpenHands task must become `failed` after a conversation exists, the Docker or K8s Executor SHALL
freeze further work, classify it as `goal_failed` or `runtime_failed`, package
the allow-listed workspace and content-safe visible evidence into a
provider-neutral source, and make one bounded resumable upload attempt before
cleanup. It SHALL append `failed` exactly once after capture becomes available
or unavailable and SHALL NOT generate target-specific packages.

#### Scenario: Goal reaches its cap

- **WHEN** v0020 produces native `capped`
- **THEN** the Executor captures a `goal_failed` portable source before cleanup and then appends the stable failed event

#### Scenario: Runtime exits after session creation

- **WHEN** an owned container or Pod exits unexpectedly after creating a conversation
- **THEN** the Executor attempts `runtime_failed` source capture from the stopped runtime surface before removal

#### Scenario: Capture times out

- **WHEN** snapshot or upload exceeds its hard limit
- **THEN** the Executor records unavailable, appends failed exactly once, cleans up, and releases capacity

### Requirement: OpenHands Executors capture completed cancellations before cleanup

After OpenHands accepts cancellation and before removing the owned runtime, the assigned Docker or K8s Executor SHALL
freeze further work, capture and upload one provider-neutral source with kind
`cancelled`, and then append the completed control result and terminate through
the existing lifecycle path. Capture failure or timeout SHALL record the source
as unavailable but SHALL NOT turn an accepted cancellation into a failed control.

#### Scenario: Cancellation is accepted and captured

- **WHEN** OpenHands accepts the assigned task's cancellation request
- **THEN** the Executor captures one `cancelled` source before cleanup, completes the control, and uses the existing terminal lifecycle behavior

#### Scenario: Cancelled capture fails

- **WHEN** cancellation succeeds but source capture times out or fails validation or storage
- **THEN** the Executor records unavailable, still completes the cancellation control, cleans up, and does not introduce a cancelled lifecycle state

### Requirement: Portable capture excludes unsafe and private material

The assigned Executor SHALL bind upload to canonical task/team/Executor/command
and reject traversal, links, devices, unexpected files, invalid digest, and
configured limits. It SHALL exclude secret plaintext, credentials, hidden
reasoning, raw Goal judge prompts, and private locators and SHALL retry
ambiguous operations with stable identifiers.

#### Scenario: Failure precedes conversation creation

- **WHEN** image pull or runtime startup fails before a workspace/conversation source exists
- **THEN** the Executor uploads nothing and reports `unavailable_no_session` with the ordinary failed event

#### Scenario: Unsafe workspace entry exists

- **WHEN** capture finds a forbidden path, link, device, or oversized entry
- **THEN** the Executor aborts the source upload, reports unavailable content-free, and still completes failed cleanup
