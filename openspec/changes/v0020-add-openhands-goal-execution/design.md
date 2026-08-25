## Context

OpenHands Goal is an optional strategy on a normal agent-server conversation.
The server starts a background `GoalController`, sends the objective into the
same conversation, runs the agent, asks an independent judge LLM to audit
evidence, and either completes, caps, or injects judge feedback for another
iteration. Goal state is persisted as `ConversationStateUpdateEvent` entries;
the API deliberately has no `GET /goal` endpoint.

FlowAI must remain authoritative for task ownership, assignment, lifecycle,
controls, and operator projections. OpenHands owns only the inner conversation
and judge loop. Executors still talk only to State Registry and their owned
runtime; Web UI still talks only to API Gateway.

## Goals / Non-Goals

**Goals:**

- Opt eligible tasks into the native OpenHands Goal driver.
- Persist bounded goal configuration and ordered progress in State Registry.
- Map native Goal terminal outcomes deterministically to FlowAI lifecycle.
- Reuse v0010 pause/continue for Goal stop/resume and reconcile restarts.

**Non-Goals:**

- Reimplement the Goal judge or continuation loop in FlowAI.
- Expose judge credentials, full prompts, hidden reasoning, or raw conversation
  events to State Registry or Web UI.
- Create child tasks, fork conversations, retain work after v0010 cleanup, or
  change v0011 hibernation.
- Treat `capped` as successful completion.
- Let listeners choose an unbounded iteration count.

## Decisions

1. **Goal eligibility belongs to task type.** Admin task-type registration gains
   immutable `execution_mode = single | openhands_goal` and, for Goal mode, a
   required positive `goal_max_iterations_limit`. Each ingested Goal task must
   carry a non-empty `goal_objective` and may request `goal_max_iterations`
   within the task-type limit; omission uses the limit. This prevents listeners
   from creating unbounded loops.
2. **Goal configuration becomes immutable task state.** Registry stores the
   validated objective and effective cap on the task and returns them only in
   the assigned claim and same-team detail projection. Discovery and admin task
   summaries omit the objective. Executor passes the values verbatim to the
   native start endpoint after creating the conversation.
3. **OpenHands remains the Goal engine.** Executor calls the documented start,
   stop, and resume endpoints and watches persisted conversation events with
   `key = "goal"`. It never recreates judge prompts or calls a judge LLM itself.
   Runtime images pin an OpenHands agent-server version supporting this API and
   configure the judge through existing scoped launch parameters/secrets.
4. **Registry stores normalized goal events.** The assigned Executor converts
   each native status update into an idempotent `task_goal_event` carrying
   provider event ID, `running | complete | capped | interrupted`, iteration,
   cap, occurred time, and a bounded content-safe verdict summary. Registry
   orders them, enforces legal transitions, updates a goal projection, audits
   identifiers only, and streams same-team frames.
5. **Lifecycle mapping is explicit.** First native `running` leads to the normal
   task `running` event. `complete` leads to exactly one `finished`; `capped`
   leads to exactly one `failed` with `failure_reason =
"openhands_goal_iteration_cap_reached"`. Goal status never becomes a new
   FlowAI task lifecycle state.
6. **v0010 controls own human interruption.** For a Goal-mode task, pause uses
   native `/goal/stop`; accepted native `interrupted` participates in v0010's
   paused projection and deadline. Continue uses `/goal/resume` before that
   deadline. Interrupt remains permanent and cleans up. A normal user message is
   not used because OpenHands would interrupt the Goal implicitly.
7. **Restart reconciliation reads event history.** Executor reconnects to the
   owned conversation, scans the persisted OpenHands events for the latest Goal
   update, deduplicates already mirrored provider event IDs, and resumes
   observation or the v0010 control outcome. Missing or inconsistent history
   fails the task content-free; it does not guess or start a second Goal.
8. **Goal and v0019 are orthogonal.** The Goal loop stays within one FlowAI task
   and one OpenHands conversation. A native Goal terminal event carries no
   v0019 continuation envelope and creates no child.

## Risks / Trade-offs

- [Judge falsely confirms completion] → Keep the independent native judge,
  expose its bounded verdict summary, and retain ordinary execution evidence.
- [Judge never confirms completion] → Enforce the immutable task-type cap and
  map `capped` to failure.
- [Duplicate or reordered provider events] → Deduplicate by provider event ID,
  validate monotonic iteration/status transitions, and fail inconsistencies.
- [Executor crashes during a judge call] → Reconcile from persisted conversation
  events before taking action and never start a second Goal blindly.
- [Objective or verdict leaks sensitive data] → Omit objectives from discovery,
  bound/redact verdict summaries, and keep logs/audit content-free.
- [OpenHands API changes] → Pin compatible images and cover the exact native API
  with container-native contract tests.

## Migration Plan

1. Add nullable Goal fields and event/projection tables; existing task types and
   tasks remain `single`.
2. Deploy Registry and Gateway contracts with Goal mode disabled.
3. Deploy Goal-capable Docker and K8s OpenHands Executor images and clients.
4. Enable Goal task-type registration, then Web UI progress and controls.
5. Roll back by disabling new Goal task types; allow claimed Goals to reach a
   terminal outcome or interrupt them through existing controls.

## Open Questions

None.
