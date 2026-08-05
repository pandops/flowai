# Proposed ADRs for v0012-add-opentelemetry-observability

## ADR: Route platform telemetry through OpenTelemetry Collector

### Status

Proposed

### Context

FlowAI services and OpenHands need a common standard ingress without embedding
backend credentials or vendor SDK choices in every workload.

### Decision

Use a platform-owned OpenTelemetry Collector as the OTLP ingress. Workloads
export to it directly; the Collector owns filtering, sampling, credential
injection, and backend routing. The Collector is operational infrastructure and
does not alter the application connection matrix.

### Consequences

Services remain vendor-neutral and Collector failure is isolated from business
behavior. The deployment must secure workload-to-Collector access and operate a
bounded queue and redaction policy.

## ADR: Represent durable task causality with finite spans and links

### Status

Proposed

### Context

Task ingestion, waiting, claim, and execution are durable asynchronous phases;
one long-lived parent span would misstate latency and leak resources.

### Decision

End each span with its operation, propagate W3C context synchronously, and link
later task execution to available producer/claim context. Use opaque task,
command, Executor, and conversation identifiers for secured trace search.

### Consequences

Trace backends may display multiple linked traces rather than one tree. Queries
and UI conventions must understand links and stable identifiers.

## ADR: Keep telemetry configuration and content policy platform-owned

### Status

Proposed

### Context

OpenHands enables tracing from environment variables and may capture agent
inputs and outputs. Team-owned environments could otherwise redirect sensitive
telemetry to an arbitrary endpoint.

### Decision

Executors reserve all supported telemetry variables, inject only trusted
Collector settings, and default telemetry to metadata allow-lists. Collector
processors enforce redaction before backend export, and backend credentials stay
outside task runtimes whenever possible.

### Consequences

Teams cannot independently select a telemetry backend through task environment
definitions. Enabling content capture or Laminar browser replay requires a
separate explicit privacy and authorization change.
