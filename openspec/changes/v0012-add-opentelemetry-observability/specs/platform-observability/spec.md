## ADDED Requirements

### Requirement: Platform workloads export telemetry through a platform-owned OpenTelemetry Collector

FlowAI SHALL support an optional platform-owned OpenTelemetry Collector ingress
for telemetry emitted by State Registry, every concrete Executor, and each
instrumented agent runtime. Workloads SHALL use OTLP and SHALL NOT require a
vendor-specific exporter or credential. Collector or backend unavailability
SHALL NOT change readiness, task assignment, lifecycle, authorization, API
responses, or durable state; bounded telemetry may be dropped instead.

#### Scenario: Configured workloads export through one ingress

- **WHEN** State Registry, an Executor, and its OpenHands runtime run with the configured Collector reachable
- **THEN** the Collector receives OTLP telemetry identifying all three service resources without any workload connecting directly to the selected storage backend

#### Scenario: Collector is unavailable

- **WHEN** the configured Collector is unreachable while a task is claimed and completed
- **THEN** the task follows the same canonical lifecycle and API outcomes as telemetry-disabled operation, and telemetry export failure does not make either service unready

#### Scenario: Telemetry is disabled

- **WHEN** no telemetry endpoint is configured
- **THEN** FlowAI services use no-op providers, the Executor injects no OpenHands export endpoint, and normal business behavior is unchanged

### Requirement: FlowAI propagates trace context without treating it as authority

FlowAI SHALL extract and inject W3C `traceparent` and `tracestate` on
instrumented synchronous HTTP boundaries, including mTLS connections, and SHALL
create bounded spans for meaningful service, database, Docker/runtime, and
external-client operations. Trace context and baggage SHALL be observational
only and SHALL NOT establish or broaden identity, team ownership,
authorization, scope tokens, or task eligibility.

#### Scenario: Executor calls State Registry over mTLS

- **WHEN** an instrumented Executor calls State Registry with an active sampled span
- **THEN** State Registry creates a server span under the propagated W3C parent while continuing to derive authorization exclusively from the authenticated mTLS identity

#### Scenario: Caller sends forged baggage

- **WHEN** a caller supplies baggage naming another team, operator, task, or Executor
- **THEN** authorization and response visibility remain identical to a request without that baggage and no baggage value overrides canonical identity

### Requirement: Durable task phases use finite spans and explicit correlation

FlowAI SHALL NOT keep an ingestion, discovery, or claim span open while a task
waits durably or executes asynchronously. Each phase SHALL end when its operation
ends. Task execution SHALL use a finite execution span carrying opaque
`flowai.task.id`, `flowai.command.id`, and `flowai.executor.id` attributes and
SHALL link to available producer or claim context rather than claiming false
synchronous parentage.

#### Scenario: Task waits before claim

- **WHEN** a task remains pending longer than the originating ingestion request
- **THEN** the ingestion span ends with that request and later claim and execution spans remain searchable by stable task identifiers without one span covering the wait interval

#### Scenario: Executor starts asynchronous execution

- **WHEN** a successful claim starts the task goroutine after the claim request completes
- **THEN** the execution span has a valid link to available claim context and contains the canonical task, command, and Executor identifiers

### Requirement: Concrete Executors configure agent telemetry from trusted platform state

Each concrete Executor SHALL own the telemetry configuration supplied to its
agent runtime. For an OpenHands runtime, the Executor SHALL inject the trusted
OTLP endpoint and protocol, `service.name`, and bounded opaque resource
attributes for task, command, Executor, runtime, and tool. It SHALL strip or
override task environment values for `OTEL_*`, `LMNR_*`, and every supported
OpenHands telemetry alias before container or Pod creation. Telemetry backend
credentials SHALL NOT originate from team-owned environment values.

#### Scenario: Executor starts an OpenHands task

- **WHEN** an Executor with telemetry enabled creates an OpenHands runtime for a claimed task
- **THEN** the runtime receives the trusted Collector configuration and bounded FlowAI resource attributes and its built-in conversation, agent-step, LLM, and tool spans reach that Collector

#### Scenario: Task environment attempts telemetry redirection

- **WHEN** an opened task environment contains an OTLP endpoint, exporter header, Laminar key, or another reserved telemetry setting
- **THEN** the Executor removes or replaces it with trusted platform configuration before runtime creation and no telemetry is sent to the task-selected destination

#### Scenario: OpenHands does not adopt the inbound parent

- **WHEN** the pinned OpenHands Agent Server starts an independent conversation trace despite receiving W3C context
- **THEN** FlowAI correlates the execution and conversation traces through stable task, command, Executor, and conversation identifiers without changing lifecycle behavior

### Requirement: Telemetry excludes sensitive and content-bearing data by default

FlowAI telemetry SHALL use an allow-list of metadata and SHALL NOT export task
payloads, prompts, model inputs or outputs, tool inputs or outputs, request or
response bodies, environment plaintext, secrets, credentials, scope tokens,
authorization headers, session API keys, ciphertext, nonce or authentication-tag
bytes, key material, raw SQL, or raw dependency error bodies. Error telemetry
SHALL expose only a bounded error class and sanitized description. The Collector
SHALL enforce equivalent filtering before forwarding agent-runtime telemetry.

#### Scenario: Task contains a unique secret marker

- **WHEN** a task and its environment contain a unique marker across prompt, secret, authorization, and tool-output fields and the task succeeds or fails
- **THEN** no exported span, metric, log correlation field, Collector diagnostic output, or export error contains that marker

#### Scenario: Dependency returns a sensitive error body

- **WHEN** an instrumented dependency returns a body containing credentials or environment plaintext
- **THEN** telemetry records a bounded dependency error class and status without recording the raw body

### Requirement: Logs and metrics correlate safely with traces

FlowAI services SHALL add valid `trace_id` and `span_id` fields to structured
logs emitted with an active sampled span and SHALL omit or use documented empty
values when no valid span exists. Service metrics SHALL use bounded labels only.
Task, command, conversation, Executor, operator, source-system, source, project,
environment, secret, request, and team identifiers or names SHALL NOT be metric
labels. Metric exemplars MAY contain a valid trace identifier without changing
the metric label set.

#### Scenario: Request emits a correlated log

- **WHEN** an instrumented request emits a structured log inside a sampled span
- **THEN** the log contains matching valid trace and span identifiers and no sensitive body or credential field

#### Scenario: Many tenants and tasks emit metrics

- **WHEN** many distinct teams, tasks, conversations, and Executors exercise the same normalized operation
- **THEN** the metric series differ only by the documented bounded labels and none of those identifiers or names appears as a label value

### Requirement: Every service owns telemetry lifecycle and implementation

State Registry and every concrete Executor SHALL initialize, flush, and shut
down their own telemetry providers within their service boundary and SHALL NOT
import a shared FlowAI telemetry package. Shutdown SHALL stop accepting new work
before flushing and SHALL bound the flush by configuration. A flush timeout or
export failure SHALL be reported through a rate-limited sanitized operational
message and SHALL NOT prevent process shutdown after the deadline.

#### Scenario: Service shuts down with buffered telemetry

- **WHEN** a FlowAI service receives its shutdown signal with telemetry buffered
- **THEN** it stops new work, attempts a bounded flush, and exits after successful flush or the configured deadline without leaking telemetry credentials

#### Scenario: A future concrete Executor adds telemetry

- **WHEN** a new concrete Executor implements this capability
- **THEN** its telemetry packages, configuration, tests, and runtime injection remain entirely inside that service directory and import no internal package from another service
