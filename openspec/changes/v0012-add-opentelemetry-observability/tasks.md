## 1. Implementation start and E2E telemetry harness

- [ ] 1.1 Move every `specs/test-cases/v0012.*.md` definition to `autotests/test-cases/` without copying or renumbering, verify the source directory contains no Markdown files, and keep each implementation reference blank until its runnable test exists.
- [ ] 1.2 Add a self-contained E2E OpenTelemetry Collector configuration and inspectable test exporter under `autotest/`, pin all images and dependencies, and verify the existing non-telemetry E2E suite remains green.
- [ ] 1.3 Add service-local OpenTelemetry Go dependencies and record the resolved versions while preserving the repository's single root `go.mod` and no-shared-service-code rule.

## 2. RED — export, propagation, and durable task correlation

- [ ] 2.1 Generate the runnable E2E test for `v0012.1-services-and-openhands-export-correlated-traces`, fill its implementation reference, run it before production changes, and record failure because State Registry, Executor, and OpenHands telemetry is absent.
- [ ] 2.2 Generate the runnable E2E test for `v0012.2-durable-task-phases-use-links-not-open-waits`, fill its implementation reference, run it before production changes, and record failure because finite task phase spans and links are absent.
- [ ] 2.3 Add supplementary service-local unit tests for W3C extraction/injection, mTLS transport wrapping, link construction, no-op configuration, and resource attributes; run them and retain their expected RED failures without treating them as E2E evidence.

## 3. GREEN — service tracing and task spans

- [ ] 3.1 Implement `svc/state-registry/internal/telemetry` with configuration validation, resource identity, OTLP trace export, W3C propagators, bounded batching/sampling, no-op behavior, HTTP server instrumentation, sanitized diagnostics, and bounded shutdown.
- [ ] 3.2 Instrument State Registry's meaningful handler, store/database, and dependency operations with normalized names and bounded metadata while preserving authorization, API bodies, transactions, and error taxonomy.
- [ ] 3.3 Implement the independent `executor/docker_openhands/internal/telemetry` package with the same external behavior but no import from State Registry or any shared FlowAI package.
- [ ] 3.4 Wrap Executor health HTTP handling, State Registry backend HTTP transport, OpenHands HTTP transport, and meaningful Docker/runtime operations; add finite `executor.task.run` spans and links without holding polling, claim, or ingestion spans across durable waits.
- [ ] 3.5 Re-run the E2E tests from 2.1 and 2.2 and require GREEN evidence for service export, W3C parentage, finite spans, canonical identifier attributes, links, and unchanged task lifecycle; then run related Go tests.

## 4. RED/GREEN — trusted OpenHands telemetry configuration

- [ ] 4.1 Generate the runnable E2E test for `v0012.3-task-cannot-redirect-or-authorize-with-telemetry-context`, fill its implementation reference, run it, and record RED because task environments can currently provide telemetry variables and OpenHands receives no trusted platform overlay.
- [ ] 4.2 Add supplementary Executor unit tests covering the complete reserved telemetry variable set, deterministic trusted overlay, telemetry-disabled omission, resource-attribute escaping/bounds, and absence of backend credentials in task values; retain expected RED results.
- [ ] 4.3 Implement Executor-owned stripping and injection for every documented OpenHands `OTEL_*`, telemetry alias, and `LMNR_*` setting; inject trusted endpoint/protocol, service name, and bounded task/command/Executor/runtime/tool resource attributes after environment open.
- [ ] 4.4 Run a pinned-image integration spike that sends a known `traceparent` to OpenHands conversation creation, records whether the server adopts it, and implement the specified stable-identifier/link fallback without forking OpenHands when it does not.
- [ ] 4.5 Re-run the E2E test from 4.1 and require GREEN evidence that forged baggage grants no authority, trusted Collector export succeeds, and the task-selected endpoint receives no connection; then run related unit/integration tests.

## 5. RED/GREEN — content safety, correlated logs, and bounded metrics

- [ ] 5.1 Generate the runnable E2E test for `v0012.4-telemetry-redacts-content-and-secrets`, fill its implementation reference, run it, and record RED against unique markers before adding the allow-list/redaction implementation.
- [ ] 5.2 Generate the runnable E2E test for `v0012.5-logs-correlate-and-metrics-remain-bounded`, fill its implementation reference, run it, and record RED because trace-aware log fields and bounded service metrics are absent.
- [ ] 5.3 Implement service-local trace-aware `slog` enrichment, metadata allow-lists, sanitized error classification, and Collector processors that remove content-bearing or credential fields from OpenHands and service telemetry before export.
- [ ] 5.4 Implement service-local HTTP/runtime/task metrics with documented bounded label sets and optional trace exemplars; add supplementary cardinality and secret-marker tests without using identifiers or names as labels.
- [ ] 5.5 Re-run the E2E tests from 5.1 and 5.2 and require GREEN evidence across raw received/exported telemetry, Collector diagnostics, JSON logs, and scraped metric series; then run related Go tests.

## 6. RED/GREEN — outage and shutdown behavior

- [ ] 6.1 Generate the runnable E2E test for `v0012.6-collector-outage-and-shutdown-do-not-break-work`, fill its implementation reference, run it, and record RED because bounded service telemetry lifecycle behavior is not implemented.
- [ ] 6.2 Add supplementary service-local tests for bounded queues, rate-limited sanitized exporter warnings, shutdown ordering, flush success, flush timeout, and disabled providers; retain expected RED results.
- [ ] 6.3 Implement bounded non-blocking export failure behavior and shutdown ordering in each service: stop new work, flush within the configured deadline, sanitize/rate-limit failure logs, and exit after success or timeout.
- [ ] 6.4 Re-run the E2E test from 6.1 and require GREEN evidence for unchanged readiness/API/domain behavior and bounded exit with the Collector unavailable; then run related Go tests.

## 7. Refactor, documentation, and verification

- [ ] 7.1 Refactor only while all targeted E2E and service-local tests remain green; confirm no service imports another service's `internal/telemetry` package and no shared FlowAI observability code exists.
- [ ] 7.2 Update per-service configuration examples and READMEs plus `docs/architecture/overview.md` with enable/disable, Collector, sampling, reserved-variable, redaction, and operational troubleshooting guidance, without promoting proposed diagrams before acceptance.
- [ ] 7.3 Run formatting, `go test ./...`, race-enabled relevant Go tests, lint, every runnable `v0012.*` E2E test, the existing cross-service regression suite, and strict OpenSpec validation; record exact commands and results.
- [ ] 7.4 Audit all telemetry output and configuration for secret/payload leakage, unbounded labels or attributes, vendor credentials, public Collector exposure, cross-service imports, and business-topology changes; resolve every finding before acceptance.
