## 1. Contract and existing E2E coverage

- [x] 1.1 Map changed scenarios to existing cases `v0002.1`, `v0002.2`, `v0002.12`, `v0002.16`, `v0002.17`, `v0002.19`, `v0002.20`, `v0002.27`, `v0002.43`, `v0002.60`–`v0002.62`, `v0002.79`–`v0002.81`, `v0005.1`, `v0005.2`, `v0005.7`, and `v0005.9`–`v0005.11`; update those definitions and runnable assertions instead of creating duplicate v0009 tests.
- [x] 1.2 RED: change existing `v0002.16` OpenAPI assertions to require no mTLS security schemes or requirements and verify failure while `ExecutorMTLS`, `ListenerMTLS`, `GatewayMTLS`, or `SystemAdministratorMTLS` remains.
- [x] 1.3 GREEN: remove backend mTLS security declarations and certificate-derived descriptions from OpenAPI while preserving endpoint schemas, status codes, team/scope predicates, and secure PostgreSQL documentation.
- [x] 1.4 CLEANUP: remove every backend mTLS/client-certificate assertion from all pre-v0009 test definitions, runnable tests, fixtures, helpers, snapshots, and generated contracts, including archived-name v0001/v0002 coverage and active v0005 coverage. Rewrite covered assertions for HTTP and canonical-record validation; delete obsolete PKI fixtures rather than leaving skipped tests. Preserve only PostgreSQL TLS/server-certificate checks and external Ingress HTTPS checks.

## 2. State Registry HTTP transport

- [x] 2.1 RED: update existing `v0002.20` transport assertions for HTTP/no-credential startup and ignored nonexistent certificate paths; verify they fail while State Registry still constructs TLS or service-identity middleware.
- [x] 2.2 GREEN: serve State Registry HTTP/WS without TLS or client-certificate middleware; accept legacy TLS/mTLS keys but never read their paths, and emit at most one key-name-only deprecation warning.
- [x] 2.3 RED: add integration tests proving secure PostgreSQL server identity/chain failure still fails closed after backend HTTP TLS removal.
- [x] 2.4 GREEN: keep PostgreSQL TLS configuration and server verification independent from the HTTP listener, then pass the new database regression tests.
- [x] 2.5 REFACTOR: remove unused HTTP certificate loaders, identity context types, middleware, fixtures, and dependencies while keeping HTTP E2E and PostgreSQL TLS tests green.

## 3. Executor HTTP clients and persisted scope

- [x] 3.1 RED: update existing `v0002.17`, `v0002.19`, `v0002.27`, `v0005.1`, `v0005.2`, `v0005.7`, and `v0005.9`–`v0005.11` assertions for the implemented Docker Executor and planned K8s Executor; verify registration, restart, immutable team/system scope, FIFO discovery, claims, events, controls, and environment opens fail while clients require mTLS.
- [x] 3.2 GREEN: replace State Registry mTLS clients in `executor_docker_openhands` and planned `executor_k8s_openhands` behavior with HTTP clients; submit configured scope/team/tag, preserve the existing Executor ID lifecycle, and retain canonical-record ownership checks without treating the ID as a credential.
- [x] 3.3 RED: configure missing and malformed legacy certificate/key/CA paths for both Executors and verify startup currently fails or accesses the paths.
- [x] 3.4 GREEN: accept and ignore legacy Executor TLS/mTLS configuration without file access, certificate loading, watchers, Secrets, or certificate volumes.
- [x] 3.5 REFACTOR: delete Executor PKI helpers and certificate fixtures after all Docker unit/integration tests and both real OpenHands smoke tasks remain green.

## 4. Listener, Gateway, and admin paths

- [x] 4.1 RED: update existing `v0002.1`, `v0002.2`, `v0002.12`, `v0002.43`, `v0002.60`–`v0002.62`, and `v0002.79`–`v0002.81`; verify listener ingestion and Gateway/admin calls fail without service certificates in the current implementation.
- [x] 4.2 GREEN: remove listener/Gateway/admin service-identity authentication from State Registry; validate listener ownership from submitted related records and consume Gateway/admin context as trusted request data.
- [x] 4.3 RED: cover invalid listener team/source relationships, malformed Gateway context, team filtering, audit attribution, and admin validation to prove non-auth authorization and data-integrity checks remain enforced.
- [x] 4.4 GREEN: pass the retained ownership, filtering, audit, admin, pagination, environment, and secret suites over HTTP without adding replacement backend credentials.

## 5. Deployment and documentation

- [x] 5.1 RED: assert implemented Docker deployment assets and planned K8s deployment artifacts contain no backend certificate Secret, TLS port, certificate volume, or HTTPS Registry URL while Ingress HTTPS and PostgreSQL TLS remain configured.
- [x] 5.2 GREEN: update implemented configs, Compose, health probes, service URLs, examples, and planned K8s deployment requirements to backend HTTP; remove backend PKI generation/mounting and preserve Ingress and database security material.
- [x] 5.3 GREEN: update AGENTS.md topology/security matrices and active v0005/v0006/v0007/v0008 references so no final-target artifact claims State Registry backend mTLS or certificate-derived identity.
- [x] 5.4 VERIFY: run the implemented State Registry, Docker Executor, listener, Gateway, OpenAPI, Playwright, real OpenHands Docker smoke, and PostgreSQL TLS suites; validate the planned K8s Executor artifacts statically. Runtime K8s/kind smoke remains a v0005 implementation gate because this branch contains no `executor_k8s_openhands` service or kind harness.
  - GREEN evidence: `go test ./...` passes all State Registry and Docker Executor packages; explicit `golangci-lint run ./state-registry/... ./executor_docker_openhands/...` reports `0 issues`.
  - GREEN evidence: the full State Registry Playwright run passes `137` tests; targeted reruns pass `v0002.20` (plaintext backend HTTP plus PostgreSQL `verify-full` fail-closed) and real OpenHands Docker cases `v0002.52` and `v0002.53`.
  - GREEN evidence: strict all-change validation includes `v0005-executor-k8s` and passes, while its POST/Registry-generated UUID lifecycle remains owned by v0005 and is not implemented by v0009.
- [x] 5.5 VERIFY: run a repository-wide search across test code and test-case definitions and require zero affirmative backend `mTLS`, client-certificate, certificate-derived identity, or OpenAPI security-scheme requirements. Negative regression assertions may name removed schemes and legacy ignored keys; preserve only affirmative PostgreSQL TLS/server-certificate and external Ingress HTTPS assertions.
- [x] 5.6 VERIFY: render both v0009 PlantUML sources, build the complete local preview, run `openspec validate v0009-remove-backend-mtls --strict`, `openspec validate --all --strict`, and `git diff --check`.
  - GREEN evidence: `puml-verify` renders both sources to SVG and `puml-preview` renders both files into the ignored `.preview/` gallery with no partial-render warning.
  - GREEN evidence: strict v0009 validation passes and strict repository validation reports `11 passed, 0 failed`.
