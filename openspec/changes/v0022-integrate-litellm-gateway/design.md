## Context

`v0013-add-task-sources-interface` introduces team-owned model records and task-type execution settings, but deliberately leaves provider integration open. LiteLLM provides an OpenAI-compatible model gateway, an A2A agent gateway, authenticated management endpoints, virtual keys, and an upstream Admin UI. The upstream project also has version-specific regressions around database-backed model editing, so “an upstream UI exists” is not sufficient evidence that an arbitrary latest image is safe to deploy.

FlowAI's topology remains authoritative: Web UI talks only to API Gateway; API Gateway talks only to State Registry; Executors use State Registry for platform coordination. The process running inside an assigned task container or Pod is an agent runtime, not the Executor service itself, and may call the LiteLLM data plane using task-scoped configuration. LiteLLM administration is a separate operator plane.

## Goals / Non-Goals

**Goals:**

- Reuse a qualified official LiteLLM Admin UI and management API.
- Configure providers, model deployments, and supported A2A agent endpoints dynamically and durably.
- Expose LiteLLM administration at a separate password-protected origin.
- Let FlowAI teams register safe references to centrally configured LiteLLM targets and select them from task types.
- Keep all administrator, provider, and runtime credentials out of task, event, audit, and browser projections.
- Route model and supported A2A calls through LiteLLM with explicit failure and no provider-direct fallback.

**Non-Goals:**

- Forking, re-skinning, embedding, or reproducing the LiteLLM Admin UI.
- Treating an LLM model as a FlowAI agent implementation. `agent_id` selects the FlowAI agent runtime; a LiteLLM target supplies that runtime with a model endpoint or an explicitly A2A-compatible remote agent endpoint.
- Giving team operators LiteLLM administrator access in the first release.
- Supporting every LiteLLM feature, provider, endpoint, or Enterprise-only capability.
- Sending Web UI or API Gateway traffic directly to LiteLLM.
- Allowing a task to override the target selected by its immutable task-type revision.

## Decisions

### Reuse the official UI after qualifying a pinned release

The implementation starts with a compatibility spike against an exact LiteLLM release and immutable image digest. The gate verifies dedicated-password login, unauthenticated rejection, database-backed create/update/disable operations, secret masking, restart persistence, model invocation, and A2A invocation. Production manifests use the exact accepted digest and do not track `latest` or `main-stable`.

If any mandatory gate fails, implementation stops at the dependency gate: the team selects another upstream release or contributes/fixes upstream and repeats qualification. This change does not silently expand into a custom management UI.

Alternative considered: build management screens in FlowAI Web UI. Rejected because it duplicates a maintained upstream UI, broadens FlowAI's secret-handling surface, and couples the API Gateway to provider-specific schemas.

### Run LiteLLM as a self-contained FlowAI service wrapper

`svc/litellm-gateway/Containerfile` derives from the qualified official image by digest and owns FlowAI-specific configuration and probes. The service owns a separate durable PostgreSQL database/schema. Database-backed model storage is enabled; YAML contains bootstrap and non-secret settings only and is not mixed with independently mutable model definitions after bootstrap.

Alternative considered: use an unwrapped upstream image directly from deployment manifests. Rejected because every FlowAI runtime service must own a production `Containerfile` and be reproducibly testable as its deployed artifact.

### Separate the administrator and data planes

The administrator plane is published at a dedicated origin such as `litellm-admin.<deployment-domain>`. A deployment-owned ingress password gate requires a non-default administrator username and independently generated password before any upstream UI byte is returned; it provides bounded authentication, origin-scoped Secure/HttpOnly/SameSite cookies, and login rate limiting. Behind it, the qualified upstream UI also uses explicit `UI_USERNAME` and `UI_PASSWORD`. Neither password equals the independently generated LiteLLM master key. TLS is mandatory outside local E2E.

The data plane is reachable only from authorized task workload networks and health/test infrastructure. Requests require a LiteLLM virtual key. Management paths and the Admin UI are not reachable through the data-plane route. API Gateway never proxies either plane.

Alternative considered: link or iframe LiteLLM under the FlowAI origin. Rejected because it confuses authentication domains, increases credential leakage risk, and violates the separate-access requirement.

### Model and remote-agent targets are distinct typed references

LiteLLM owns routable upstream deployments. FlowAI owns team-visible references:

- immutable `target_id` and `team_id`; model targets expose that ID as canonical `model_id`, while A2A targets expose it as canonical `agent_id`;
- `kind` in `{model, a2a_agent}`;
- immutable LiteLLM public alias for the revision;
- display name and safe capability metadata;
- server-owned opaque, version-pinned OpenBao reference to a team-and-target-scoped LiteLLM virtual key;
- enabled state and immutable revision metadata.

Provider URL, provider name when sensitive, provider API key, LiteLLM master key, UI password, and virtual-key plaintext are excluded from the record and all projections. State Registry validates a new revision through an allowlisted LiteLLM metadata endpoint with a read-only identity, matching exact alias and kind. It then uses a distinct narrowly scoped key-provisioner identity to create a virtual key authorized only for that team and alias, immediately persists the value as an OpenBao-backed secret version, discards plaintext after encryption, and commits the target revision only after both operations succeed. The operator never supplies an opaque key reference. Unknown and foreign targets use the same non-revealing response.

`v0013`'s canonical `model_id` is the model-kind specialization of `target_id`; its required canonical `agent_id` is either an existing local runtime agent or the agent-kind specialization for a LiteLLM A2A target. Existing task types continue to carry both canonical IDs. There is no implicit conversion between model and agent IDs. The first A2A profile is protocol version `1.0`, HTTP+JSON transport, non-streaming request/response, with the agent card and message wire schema owned by the A2A 1.0 specification as implemented by the pinned LiteLLM release. Any additional protocol version, transport, or streaming mode requires a later qualified profile and explicit safe capability value.

Alternative considered: copy complete LiteLLM deployment JSON into State Registry. Rejected because it creates competing authorities and copies credentials/provider details into FlowAI.

### Snapshot target identity, resolve credentials only at execution

Task ingestion snapshots each selected target ID, immutable target revision, kind, public alias, and key secret-version reference. It never stores a key value. After claim, the assigned Executor obtains the existing `OpenEnvironmentResponse.values` string map from State Registry. State Registry resolves pinned secrets through OpenBao and adds exact keys `LITELLM_BASE_URL`, `LITELLM_MODEL_ALIAS`, `LITELLM_MODEL_KEY`, and, for an A2A agent, `LITELLM_A2A_ALIAS`, `LITELLM_A2A_KEY`, `LITELLM_A2A_PROTOCOL_VERSION=1.0`, and `LITELLM_A2A_TRANSPORT=http+json`. No top-level response field changes. Neither discovery responses nor unassigned Executors receive these values.

The workload calls LiteLLM directly. The Executor continues to use only State Registry for claims, events, controls, and environment opens. Egress policy denies provider endpoints for workloads configured to use LiteLLM so a failure cannot become a direct-provider fallback.

### Fail closed on configuration drift

Removing or disabling an upstream alias does not rewrite historical task snapshots. A later execution receives a stable typed error from LiteLLM/FlowAI, records a failed task event without secret-bearing response bodies, and does not select another alias. New task-type revisions cannot select disabled FlowAI target revisions.

## Risks / Trade-offs

- **Upstream UI regressions** → pin version and digest; run the qualification suite before upgrades and after database migrations.
- **OSS/Enterprise boundary changes** → use only capabilities present outside LiteLLM's `enterprise/` tree unless a license is explicitly procured and recorded; review the license at every upgrade.
- **One administrator account is coarse-grained** → isolate the origin, rotate a dedicated password, rate-limit login, and defer team self-service upstream administration until a separate authorization design.
- **Alias drift between systems** → immutable FlowAI target revisions, exact-kind validation, explicit disable state, and no automatic fallback.
- **Virtual-key leakage into a workload** → scope keys by team/target, pin secret versions, restrict network egress, redact logs, and revoke compromised keys; a workload necessarily receives only the runtime key it must use.
- **State Registry gains a LiteLLM metadata dependency** → bound timeouts, read-only credentials, no secret-bearing response fields, and transaction-free validation before committing a revision.
- **A2A support evolves rapidly** → qualify exact protocol versions and target providers; unsupported capabilities fail closed.

## Migration Plan

1. Select an exact upstream LiteLLM release, verify its license boundary, pin its image digest, and run the qualification suite against disposable infrastructure.
2. Add the self-contained service image, database, secret inputs, separate admin/data routes, probes, and backup/restore procedure.
3. Create dedicated administrator, master, metadata-reader, and per-team runtime credentials; store all secrets in the deployment secret store/OpenBao.
4. Add typed target records and revisions to State Registry, then add Gateway/Web UI operations for safe team-scoped references.
5. Update task-type and task-snapshot contracts, followed by both concrete Executors and agent-runtime environment wiring.
6. Run red-green unit, integration, and cross-service E2E suites, including restart, rollback, and non-disclosure cases.
7. Enable task-type selection only after the data-plane checks pass in the target environment.

Rollback disables new target selection, drains or explicitly fails affected tasks, restores the previous FlowAI services and LiteLLM image/database backup as one compatibility unit, and revokes credentials created for the failed release. Historical target revisions remain readable but never reveal secrets.

## Open Questions

- Which exact LiteLLM release and image digest passes qualification at implementation time?
- Which A2A providers pass the first fixed A2A 1.0 HTTP+JSON non-streaming qualification profile?
- What deployment-specific ingress component enforces TLS and login rate limiting?
