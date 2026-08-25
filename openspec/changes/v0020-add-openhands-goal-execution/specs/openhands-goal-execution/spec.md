## ADDED Requirements

### Requirement: One OpenHands Goal executes inside one FlowAI task

An OpenHands Goal task SHALL use one claimed FlowAI task, one assigned Executor,
one runtime, and one OpenHands conversation for every audit iteration. The
native Goal driver SHALL send judge feedback and continue in that same
conversation. FlowAI SHALL NOT create a v0019 child, fork a conversation, or
reassign the task between Goal iterations.

#### Scenario: Judge requests more work

- **WHEN** the Goal judge reports missing evidence below the iteration cap
- **THEN** OpenHands injects its follow-up into the same conversation and the same FlowAI task remains `running`

#### Scenario: Goal does not create a child

- **WHEN** an OpenHands Goal performs multiple audit iterations
- **THEN** State Registry contains one task and no dynamic-continuation relationship for those iterations

### Requirement: OpenHands Goal outcomes map deterministically

Native Goal `complete` SHALL produce exactly one FlowAI `finished` lifecycle
event. Native Goal `capped` SHALL produce exactly one FlowAI `failed` event with
`failure_reason = "openhands_goal_iteration_cap_reached"`. Native Goal status
SHALL NOT add a new FlowAI lifecycle state.

#### Scenario: Judge confirms completion

- **WHEN** OpenHands emits `complete`
- **THEN** the assigned Executor appends exactly one `finished` event and releases capacity under the ordinary terminal cleanup rule

#### Scenario: Goal reaches its cap

- **WHEN** OpenHands emits `capped`
- **THEN** the assigned Executor appends exactly one content-safe `failed` event with the stable cap reason

### Requirement: Goal pause and continuation use native stop and resume

For a Goal-mode task, v0010 pause SHALL use OpenHands Goal stop and v0010
continue SHALL use OpenHands Goal resume while the runtime remains available
and the cleanup deadline has not won. A normal user message SHALL NOT be used to
resume a Goal.

#### Scenario: Operator pauses a Goal

- **WHEN** the assigned Executor applies an accepted v0010 pause control to a running Goal
- **THEN** it calls native Goal stop, mirrors `interrupted`, and follows the v0010 paused-runtime deadline contract

#### Scenario: Operator continues a Goal

- **WHEN** v0010 continue wins before cleanup for a resumable interrupted Goal
- **THEN** the Executor calls native Goal resume and returns the same task and conversation to running work

#### Scenario: Runtime has been cleaned up

- **WHEN** a Goal's v0010 pause deadline has expired and its runtime is gone
- **THEN** direct Goal resume is rejected and neither v0011 recovery nor v0019 continuation is inferred
