## Why

The Docker and planned K8s OpenHands Executors currently treat one agent run as
one FlowAI task run. OpenHands now provides a resumable Goal driver that audits
evidence with an independent judge and continues the same conversation until
the objective is complete or its iteration cap is reached; FlowAI needs an
explicit, durable contract for using that mechanism.

## What Changes

- Add an opt-in OpenHands Goal execution mode with an immutable objective and a
  bounded `max_iterations` value on eligible tasks.
- Start the claimed task through OpenHands
  `POST /api/conversations/{conversation_id}/goal` instead of the ordinary
  single-run message path.
- Mirror OpenHands Goal progress into State Registry as ordered, idempotent,
  team-owned goal events and a canonical content-safe projection.
- Map OpenHands `complete` to FlowAI `finished` and `capped` to FlowAI `failed`
  with a stable content-free failure reason.
- Integrate v0010 pause/continue with OpenHands Goal `stop`/`resume` while the
  runtime and conversation remain available.
- Reconcile Goal state after Executor or agent-server restart from the latest
  persisted OpenHands conversation goal event; do not invent a `GET /goal`
  dependency.
- Show objective-safe status, audit iteration, configured cap, latest verdict
  summary, and available pause/continue controls in the task detail UI.
- Keep one OpenHands Goal inside one FlowAI task and one conversation; this
  change creates no v0019 continuation child and does not use v0011 hibernation.

## Capabilities

### New Capabilities

- `openhands-goal-execution`: Opt-in judge-driven, self-continuing OpenHands
  Goal execution and its FlowAI lifecycle mapping.

### Modified Capabilities

- `state-registry`: Validate and persist Goal configuration, goal events,
  projections, authorization, audit, and lifecycle outcomes.
- `executor`: Start, observe, stop, resume, and reconcile OpenHands Goal runs.
- `web-ui`: Render same-team Goal progress and use existing authorized task
  controls for pause and continue.

## Impact

State Registry schema, task-type/task ingestion and claim contracts, OpenAPI,
Docker OpenHands Executor and planned K8s OpenHands Executor behavior, OpenHands
client APIs, API Gateway allowlists, Web UI task detail, configuration, audit,
metrics, migrations, and cross-service E2E tests are affected.
