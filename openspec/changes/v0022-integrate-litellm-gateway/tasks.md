## 1. Upstream qualification and contracts

- [ ] 1.1 Move every `v0022.*` definition from this change's `specs/test-cases/` into `autotests/test-cases/` before implementation edits, preserving IDs and leaving no change-local test-case Markdown files.
- [ ] 1.2 Generate the RED runnable E2E tests referenced by `v0022.1` through `v0022.5` for image pinning, separate password authentication, persistent UI-managed model/A2A targets, plane isolation, and no-fallback routing.
- [ ] 1.3 Evaluate exact official LiteLLM releases, current security advisories, and license boundaries; reject versions affected by unresolved administrator-authentication or configuration-escalation advisories; record the selected version and immutable image digest; and observe all upstream qualification tests fail or pass for the expected behavior-specific reasons before accepting the dependency.
- [ ] 1.4 Define versioned OpenAPI schemas for typed safe target references, immutable revisions, capability compatibility, and sanitized LiteLLM failure classes.

## 2. Container-native LiteLLM service

- [ ] 2.1 Create `svc/litellm-gateway/Containerfile` from the qualified official digest plus owned non-secret configuration, startup validation, health/readiness probes, and container tests.
- [ ] 2.2 Add a dedicated durable PostgreSQL database/schema, database-backed dynamic configuration, migrations/startup behavior, backup/restore, and restart tests without mixed mutable YAML model definitions.
- [ ] 2.3 Add secret-managed non-default UI username/password, distinct master key, metadata-reader identity, credential-encryption material, and per-team runtime virtual keys with rotation/revocation tests.
- [ ] 2.4 Add separate TLS administrator and workload data-plane routes, bounded sessions, login rate limiting, network policies, and management-path denial from workload networks.
- [ ] 2.5 Make `v0022.1` through `v0022.5` GREEN against the built production image and record their implementation references.

## 3. State Registry target catalog and snapshots

- [ ] 3.1 Generate RED `v0022.6` and `v0022.7` E2E tests for exact alias/kind validation, team isolation, immutable revisions, snapshot pinning, and assigned-only environment access.
- [ ] 3.2 Add State Registry migrations and constraints for team-owned typed LiteLLM targets, one per-team `agent_id` namespace shared with local agents, immutable kind discriminators, safe capability metadata, enabled state, pending/active/orphaned credential bindings, immutable revisions, and opaque version-pinned OpenBao references.
- [ ] 3.3 Implement bounded read-only LiteLLM metadata validation plus target-scoped virtual-key provisioning and immediate OpenBao storage using separate least-privilege service identities, exact alias/kind matching, allowlisted response fields, compensating key revocation, non-revealing failures, and no transaction held during network I/O.
- [ ] 3.4 Narrow the `v0013` model catalog contract to model-kind LiteLLM target references and add A2A-kind target compatibility without changing task types into agent definitions.
- [ ] 3.5 Snapshot target revision, kind, alias, capabilities, and secret-version reference at ingestion; expose resolved runtime configuration only to the assigned Executor through the scoped environment-open path.
- [ ] 3.6 Make `v0022.6` and `v0022.7` GREEN and prove task, discovery, event, audit, admin, and log outputs contain no credential or secret reference.

## 4. Gateway and Web UI

- [ ] 4.1 Generate RED `v0022.8` through `v0022.10` E2E tests for safe target management, compatibility validation, external administrator navigation, verified team context, and absence of LiteLLM proxy routes.
- [ ] 4.2 Implement State Registry-backed target list/create/revise/enable/disable operations in API Gateway with verified identity context and non-revealing team authorization.
- [ ] 4.3 Implement typed target management and task-type selection in Web UI using safe metadata only, including clear model/A2A distinctions and client-plus-server compatibility errors.
- [ ] 4.4 Add an optional deployment-configured external administrator link that opens the separate HTTPS origin without forwarding FlowAI sessions, tokens, or cookies.
- [ ] 4.5 Make `v0022.8` through `v0022.10` GREEN and assert API Gateway exposes no LiteLLM login, management, inference, or A2A proxy path.

## 5. Executor and agent-runtime integration

- [ ] 5.1 Generate RED `v0022.11` E2E coverage in both Docker OpenHands and K8s OpenHands environments for assigned-only configuration, runtime redaction, explicit failure, and provider-direct egress denial.
- [ ] 5.2 Independently implement the duplicated LiteLLM environment mapping in both concrete Executors without shared service code, preserving their State-Registry-only control-plane contract.
- [ ] 5.3 Configure OpenHands task workloads for exact model aliases or qualified A2A targets and add egress controls that prevent provider-direct fallback.
- [ ] 5.4 Sanitize LiteLLM/provider failures before task events, logs, probes, and retained runtime artifacts while preserving an operator-actionable failure class.
- [ ] 5.5 Make `v0022.11` GREEN in Docker and k3d production-image suites and record its implementation reference.

## 6. Verification and release

- [ ] 6.1 Run all affected unit, integration, Playwright, Docker-image, and k3d E2E suites with credential canaries and restart/rollback cases.
- [ ] 6.2 Exercise backup/restore and rollback as one compatible LiteLLM image/database unit, then verify new target selection remains disabled until health and data-plane checks pass.
- [ ] 6.3 Validate `v0022-integrate-litellm-gateway` and the complete OpenSpec baseline strictly, render the change review, and resolve every Gopnik claim-review finding before acceptance.
- [ ] 6.4 Update AGENTS.md service catalog, connection matrix, ownership matrix, and deployment documentation only after implementation evidence establishes the accepted topology.
