## Context

State Registry and `executor_docker_openhands` emit JSON logs and request IDs,
but neither initializes an OpenTelemetry SDK. OpenHands has built-in trace
instrumentation through Laminar and exports standard OTLP when its process
receives the documented `OTEL_*` variables; those variables are not currently
provided by the Executor. A claimed task is also an asynchronous boundary:
ingestion, discovery, claim, runtime execution, and terminal reporting are not
one long-lived HTTP request.

The design must preserve the hard service topology and the rule that every
service owns a self-contained implementation. Telemetry is an operational
output, not a new business-service call: workloads export to a platform-owned
Collector, while application calls remain on their existing paths.

## Goals / Non-Goals

**Goals:**

- Produce correlated traces for State Registry, each concrete Executor, and
  OpenHands runs through one standard OTLP ingress.
- Preserve synchronous W3C context and represent durable/asynchronous causality
  without keeping spans open while tasks wait in storage.
- Correlate logs and bounded metrics with traces while preventing secret and
  payload disclosure or high-cardinality metric labels.
- Make telemetry optional, fail-open for business processing, testable, and
  cleanly flushed during shutdown.

**Non-Goals:**

- Select the long-term trace, metric, or log backend.
- Add a telemetry control plane, user analytics, browser replay, profiles,
  dashboards, alerts, or SLOs.
- Change public APIs, authorization, tenancy, lifecycle, event ordering, or
  durable domain state.
- Fork OpenHands solely to force its conversation root beneath an inbound span.

## Decisions

1. **Use a platform-owned OpenTelemetry Collector as the only telemetry
   ingress.** Services and agent runtimes export OTLP directly to the Collector;
   the Executor does not proxy or parse agent spans. The Collector owns batching,
   filtering, sampling, credential injection, and backend routing. Direct
   per-service vendor exporters were rejected because they duplicate secrets and
   couple every service to one backend.

2. **Duplicate a small telemetry bootstrap per service.** State Registry and
   each concrete Executor receive their own `internal/telemetry` package, SDK
   resource, exporter, propagator, middleware, tests, and configuration view.
   No shared Go telemetry package is introduced. The packages follow the same
   external contract but may evolve independently.

3. **Use W3C Trace Context and Baggage for synchronous edges; use links for
   durable handoffs.** Incoming HTTP extracts `traceparent`/`tracestate`, and
   outgoing HTTP injects them through transports that wrap, rather than replace,
   mTLS. Task ingestion and claim spans end with their request. A new
   `executor.task.run` root records stable FlowAI identifiers and links to
   available claim/producer context. No remote context becomes authorization.
   Durable propagation metadata, if added to task state, is opaque telemetry
   metadata with retention and validation bounds rather than a lifecycle event.

4. **Treat OpenHands as an independently instrumented workload.** The Executor
   injects Collector endpoint/protocol, `OTEL_SERVICE_NAME=openhands-agent-server`,
   and bounded resource attributes for the task container. Its instrumented HTTP
   client sends W3C context to Agent Server. If the pinned server accepts that
   parent, traces join naturally; otherwise `flowai.task.id`,
   `flowai.command.id`, `flowai.executor.id`, and the returned conversation ID
   correlate the independent OpenHands trace. An implementation spike must prove
   the actual parent behavior before any upstream adapter is considered.

5. **Make telemetry configuration platform-owned and fail closed against
   redirection.** Runtime environment definitions cannot control `OTEL_*`,
   `LMNR_*`, or other documented OpenHands telemetry aliases. The Executor
   strips them and applies its trusted values after opening the environment.
   Backend credentials stay in the Collector whenever network topology permits;
   they are never obtained from team secret/environment values.

6. **Default to metadata-only telemetry.** Spans and logs may contain operation
   names, normalized routes, status/error class, durations, and opaque FlowAI
   identifiers. They do not contain bodies, prompts, model/tool inputs or
   outputs, environment values, tokens, credentials, ciphertext, key material,
   raw SQL, or raw dependency error bodies. OpenHands input/output capture must
   be disabled or scrubbed at Collector ingress before export when the pinned SDK
   cannot enforce this locally.

7. **Separate trace attributes from metric labels.** Opaque task/team/command/
   conversation/executor identifiers are permitted on secured traces subject to
   retention policy but forbidden as metric labels. Metrics use bounded labels
   such as service, operation, method, normalized route, outcome, task state,
   runtime, and tool type. Exemplars may carry trace correlation without adding
   label cardinality.

8. **Telemetry is optional and non-blocking.** With no endpoint configured, SDK
   providers are no-op and OpenHands receives no export variables. An unavailable
   Collector never changes HTTP results, claims, events, task terminal state, or
   readiness. Export failures are rate-limited operational warnings. Shutdown
   stops new work first, then flushes providers within a bounded deadline.

## Risks / Trade-offs

- [OpenHands starts a separate conversation trace] → Verify the pinned Agent
  Server and use stable correlation plus span links; propose an upstream adapter
  only if one tree is operationally necessary.
- [Agent spans disclose prompts or tool output] → Disable capture where
  supported and enforce Collector allow-list/redaction processors before export.
- [Task-controlled environment redirects telemetry] → Reserve and overwrite all
  supported telemetry configuration names after environment open.
- [Identifier attributes expose tenant metadata] → Restrict backend access,
  retention, and search permissions; never use display names or secret values.
- [Collector outage creates backpressure] → Use bounded asynchronous queues,
  dropping telemetry rather than blocking business operations.
- [Duplicate service bootstraps drift] → Pin compatible OTel versions and verify
  the common external contract independently per service without shared code.

## Migration Plan

1. Add an E2E-only Collector with an in-memory/debug test exporter and land the
   RED tests before service instrumentation.
2. Add per-service no-op-by-default SDK bootstrap, inbound/outbound HTTP tracing,
   correlated logs, bounded metrics, and graceful shutdown.
3. Add Executor-owned OpenHands telemetry environment injection and reservation,
   then verify parent-context behavior against the pinned image.
4. Deploy a Collector with export disabled or a discard backend, validate volume
   and redaction, then enable the selected backend and sampling policy.
5. Roll back by removing the telemetry endpoint; providers become no-op and
   business behavior remains unchanged. Collector/backend rollback never
   requires domain-data migration.

## Open Questions

- Does the exact pinned OpenHands Agent Server extract inbound W3C context for
  `POST /api/conversations`, or must its conversation trace remain linked only?
- Which production backend, retention period, and tenant access model will be
  selected after volume and redaction tests?
- Should durable producer context be stored on a future internal task column, or
  is claim-to-run linking plus stable identifiers sufficient for the first
  implementation?
