## Why

FlowAI needs one supported way to connect models and A2A-compatible agent endpoints from different providers without embedding provider-specific configuration or credentials in task types. LiteLLM already ships an AI Gateway, management APIs, and an Admin UI, so the platform should qualify and reuse that upstream surface instead of building a second provider-management interface.

## What Changes

- Add a container-native `litellm-gateway` service, built from a version-and-digest-pinned official LiteLLM image and backed by its own durable database.
- Reuse the official LiteLLM Admin UI for dynamic provider, model, virtual-key, and supported A2A-agent configuration, subject to a pinned-release qualification suite; do not fork or reproduce that UI in FlowAI.
- Publish the Admin UI at a separate origin and protect it with dedicated username/password authentication. It is never embedded in Web UI and never proxied through API Gateway.
- Keep the LiteLLM administrator credential, master key, provider credentials, and generated virtual keys out of FlowAI browser responses, State Registry task data, logs, events, and audit records.
- Add a two-step onboarding flow: a LiteLLM administrator first configures a routable target in the separate Admin UI; a FlowAI team operator then registers a safe team-owned reference to its stable LiteLLM public alias in the existing model or agent catalog. State Registry provisions and stores the target-scoped virtual key without returning its plaintext to the operator.
- Let task-type revisions select only canonical same-team FlowAI catalog IDs: `model_id` for model targets and `agent_id` for agent targets. The resulting task execution snapshot pins both selected LiteLLM aliases and target kinds; task types never contain LiteLLM or provider credentials.
- Issue least-privilege runtime credentials outside the browser and deliver the LiteLLM data-plane URL, public alias, and credential to the assigned agent runtime through the existing authorized environment/secret-open path.
- Support LiteLLM model targets through its OpenAI-compatible data plane and LiteLLM A2A targets only where the pinned upstream release passes the same management, persistence, authentication, and invocation qualification gates.
- Fail closed when a target is absent, disabled, incompatible, or unreachable; do not bypass LiteLLM or silently fall back to a provider-direct credential.
- Add container, integration, and cross-service E2E coverage for authentication, persistence, model invocation, A2A invocation, team isolation, secret non-disclosure, restart recovery, and unavailable-target behavior.

## Capabilities

### New Capabilities

- `litellm-gateway`: Qualified upstream LiteLLM deployment, separate password-protected Admin UI, persistent dynamic target management, authenticated model/A2A data plane, and operational/security boundaries.

### Modified Capabilities

- `state-registry`: FlowAI catalog records become safe team-owned references to LiteLLM public aliases; task snapshots pin the selected target without storing provider or LiteLLM credentials.
- `web-ui`: Team operators register and select safe LiteLLM target references in FlowAI, while provider credentials and upstream target configuration remain exclusively in the separate LiteLLM Admin UI.
- `api-gateway`: Team-scoped catalog operations carry verified identity context without proxying any LiteLLM UI, management, or inference traffic.
- `executor`: An assigned task receives a pinned LiteLLM target and opens its runtime credential through the existing authorized secret path; agent runtime traffic uses LiteLLM and never falls back to direct provider access.

## Impact

- New self-contained service directory `svc/litellm-gateway/` with its own `Containerfile`, pinned upstream version/digest, configuration, database lifecycle, probes, and tests.
- New separately routed LiteLLM Admin UI origin and secret-managed `UI_USERNAME`, `UI_PASSWORD`, master key, database URL, and credential-encryption key.
- State Registry, API Gateway, Web UI, both concrete Executors, agent-runtime configuration, deployment manifests, OpenAPI contracts, and cross-service E2E environments gain LiteLLM integration work.
- `v0013-add-task-sources-interface` remains the owner of team-created task types and their execution settings, but its generic model catalog is narrowed during implementation to safe LiteLLM target references. LiteLLM remains authoritative for provider credentials and routable upstream deployment configuration.
- The pinned upstream release is an explicit implementation gate: its official Admin UI must successfully create/update/disable a model and supported A2A target, persist them across restart, deny unauthenticated access, mask secrets, and route authenticated calls. A failing gate blocks adoption of that release and does not authorize a custom FlowAI replacement UI.

## Stage 0 Behavior Matrix

| Axis                    | Representative cells                                                      | Required outcome                                                            | E2E       |
| ----------------------- | ------------------------------------------------------------------------- | --------------------------------------------------------------------------- | --------- |
| Admin authentication    | no session; wrong password; correct dedicated password                    | deny; deny; allow                                                           | `v0022.1` |
| Dynamic model lifecycle | create; update; disable; restart                                          | route new config; route updated config; reject; preserve state              | `v0022.2` |
| Dynamic A2A lifecycle   | supported target; unsupported capability; disabled target                 | invoke through `/a2a`; fail closed; reject                                  | `v0022.3` |
| FlowAI registration     | known alias; unknown alias; duplicate alias; foreign team reference       | create; reject; idempotent conflict policy; non-revealing reject            | `v0022.4` |
| Task execution          | valid model target; valid A2A target; incompatible agent/target kind      | route; route; reject before execution                                       | `v0022.5` |
| Runtime failure         | LiteLLM unavailable; provider auth failure; target removed after snapshot | fail task explicitly; fail explicitly; fail explicitly with no fallback     | `v0022.6` |
| Secret boundary         | browser; task/event/audit/log projections; assigned runtime               | no secrets; no secrets; least-privilege credential only                     | `v0022.7` |
| Network boundary        | Web UI; API Gateway; unassigned runtime; assigned runtime                 | no direct LiteLLM access; no proxy; reject; authenticated data-plane access | `v0022.8` |
