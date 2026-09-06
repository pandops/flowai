## ADDED Requirements

### Requirement: FlowAI deploys a qualified LiteLLM gateway artifact

FlowAI SHALL build `litellm-gateway` from an exact official LiteLLM release pinned by immutable image digest in `svc/litellm-gateway/Containerfile`. Before accepting or upgrading that artifact, the qualification suite SHALL verify the required administrator, persistence, model, A2A, and data-plane behaviors against the built image. Deployment SHALL NOT use a floating upstream tag.

#### Scenario: Qualified image is deployed

- **WHEN** the qualification suite passes for an exact release and digest
- **THEN** production and E2E environments run the FlowAI-owned image built from that same digest

#### Scenario: Upstream image fails qualification

- **WHEN** any mandatory behavior fails for a candidate release
- **THEN** that release is rejected and no custom replacement UI or floating-tag deployment is substituted

### Requirement: LiteLLM administration has separate password-protected access

The official LiteLLM Admin UI SHALL be exposed on an origin separate from FlowAI Web UI and API Gateway. A deployment-owned ingress password gate SHALL require a non-default dedicated username and password and reject missing or invalid credentials before returning any upstream UI content. The gate SHALL use bounded origin-scoped Secure, HttpOnly, SameSite sessions and rate-limit repeated failed login attempts. The qualified upstream UI SHALL additionally use explicit non-default `UI_USERNAME` and `UI_PASSWORD`. Both passwords SHALL differ from the LiteLLM master key. API Gateway SHALL NOT proxy, embed, or authenticate the Admin UI.

#### Scenario: Unauthenticated administrator request is denied

- **WHEN** a browser requests a protected LiteLLM Admin UI resource without a valid session
- **THEN** the administrator origin returns only the login flow and no protected configuration data

#### Scenario: Dedicated credentials authenticate

- **WHEN** an administrator submits the configured username and password
- **THEN** LiteLLM creates a bounded administrator session without disclosing the password or master key

#### Scenario: FlowAI session is not accepted

- **WHEN** a browser presents only a valid FlowAI operator session to the LiteLLM administrator origin
- **THEN** the administrator origin requires its independent LiteLLM login

### Requirement: Upstream Admin UI manages persistent targets

The qualified official Admin UI SHALL create, update, disable, and inspect model deployments and A2A agent targets through LiteLLM's authenticated management APIs. The initial A2A profile SHALL be protocol version `1.0`, HTTP+JSON transport, and non-streaming request/response according to the A2A 1.0 schema implemented by the pinned LiteLLM release. Database-backed configuration SHALL survive a clean service restart. Secret inputs SHALL be write-only or masked after submission and SHALL NOT be returned in list or detail responses.

#### Scenario: Model persists across restart

- **WHEN** an administrator creates a valid model deployment, invokes it successfully, and restarts LiteLLM cleanly
- **THEN** the same public alias remains configured and routable without re-entering its provider credential

#### Scenario: A2A target persists across restart

- **WHEN** an administrator creates a supported A2A target and restarts LiteLLM cleanly
- **THEN** the same A2A alias and qualified protocol version remain available through the authenticated A2A route

#### Scenario: Disabled target rejects calls

- **WHEN** an administrator disables a previously routable target
- **THEN** new data-plane calls to that alias fail explicitly and LiteLLM does not select an unrelated target

### Requirement: LiteLLM separates management and authenticated data planes

Management routes and the Admin UI SHALL be reachable only through the administrator network route. Model and A2A data-plane routes SHALL require a valid LiteLLM virtual key and SHALL be reachable only from authorized workload networks. Provider credentials, administrator credentials, the master key, and virtual-key plaintext SHALL be redacted from logs and error bodies.

#### Scenario: Missing runtime key is rejected

- **WHEN** a workload calls a model or A2A route without a valid virtual key
- **THEN** LiteLLM rejects the call before contacting the upstream target

#### Scenario: Workload attempts management access

- **WHEN** a workload network identity calls an Admin UI or management route
- **THEN** network and application controls deny access without configuration disclosure

#### Scenario: Upstream returns a credential-bearing error

- **WHEN** an upstream failure contains request authorization material or secret values
- **THEN** the stored log and client-facing error omit those values

### Requirement: LiteLLM routes typed model and A2A targets without fallback

LiteLLM SHALL route a model alias through its qualified OpenAI-compatible endpoint and an A2A alias through its qualified A2A endpoint. A missing, disabled, incompatible, or unreachable target SHALL return an explicit bounded failure. FlowAI workloads SHALL NOT bypass LiteLLM, use provider-direct credentials, or silently choose a different alias.

#### Scenario: Model target succeeds

- **WHEN** an authorized workload calls a configured model alias with a valid virtual key
- **THEN** LiteLLM returns the qualified OpenAI-compatible response from that target

#### Scenario: A2A target succeeds

- **WHEN** an authorized compatible workload calls a configured A2A alias with its qualified protocol version and a valid virtual key
- **THEN** LiteLLM proxies the A2A exchange through that exact alias

#### Scenario: Gateway is unavailable

- **WHEN** a configured workload cannot reach LiteLLM
- **THEN** execution fails explicitly within its timeout and makes no provider-direct request
