## Why

FlowAI currently has structured request logs but no end-to-end distributed
tracing, and its OpenHands runtime emits no traces unless telemetry variables
are explicitly injected. Operators therefore cannot follow a task across State
Registry, an Executor, its OpenHands conversation, and external dependencies or
reliably diagnose latency and failure boundaries.

## What Changes

- Add a platform-owned OpenTelemetry configuration and Collector ingress for
  traces from FlowAI services and OpenHands task runtimes.
- Instrument inbound and outbound HTTP boundaries and meaningful asynchronous
  task operations while preserving W3C trace context across synchronous calls
  and using span links plus stable identifiers across durable task handoffs.
- Make each concrete Executor inject trusted OTLP configuration and bounded
  FlowAI resource attributes into its agent runtime, overriding or rejecting
  task-provided `OTEL_*`, OpenHands telemetry aliases, `LMNR_*`, and other
  platform-reserved telemetry settings.
- Correlate structured logs with valid trace and span identifiers and define
  low-cardinality service metrics without using task, command, conversation,
  Executor, operator, source, or team identifiers as metric labels.
- Require telemetry to exclude secrets, credentials, scope tokens, task
  payloads, prompts, model/tool inputs and outputs, environment plaintext, and
  raw dependency error bodies by default.
- Add graceful telemetry shutdown, explicit disabled behavior, sampling at the
  Collector, and externally observable E2E coverage for export, correlation,
  reservation, redaction, and collector-unavailable behavior.

## Capabilities

### New Capabilities

- `platform-observability`: Cross-service OpenTelemetry tracing, metric and log
  correlation, OpenHands telemetry configuration, safe attribute policy,
  sampling, export, and failure behavior.

### Modified Capabilities

- None. The new capability defines cross-cutting behavior without changing the
  existing Executor or State Registry wire contracts.

## Impact

- Change type: development.
- Affected specs: new `platform-observability` capability.
- Affected ADRs: the platform-owned Collector, signal ownership, asynchronous
  trace continuity, and telemetry-data boundary decisions in `specs/adrs.md`.
- Affected diagrams: new proposed C4 container and task trace sequence diagrams
  under `specs/diagrams/`.
- Affected test cases: new `v0012.*` E2E definitions under
  `specs/test-cases/`.
- Affected code: per-service telemetry packages and startup wiring in
  `state-registry/`, `executor_docker_openhands/`, and later concrete services;
  Executor-owned OpenHands container environment construction; local/E2E
  Collector configuration; service logs and metrics.
- Affected dependencies: OpenTelemetry Go SDK, OTLP exporters, HTTP and database
  instrumentation, and an OpenTelemetry Collector deployment artifact.

## Out of Scope

- Selecting or operating a long-term vendor backend such as Laminar, Tempo,
  Jaeger, Honeycomb, Datadog, or New Relic.
- Browser session replay, prompt/model-output capture, user analytics, profiling,
  alert rules, SLO policy, and dashboards.
- Changing task lifecycle, tenancy, authorization, service topology, or public
  State Registry and Executor APIs.
- Guaranteeing that an unmodified OpenHands Agent Server adopts an inbound
  FlowAI span as its parent when the pinned upstream version starts an
  independent conversation root; stable correlation and span links remain the
  required fallback.
