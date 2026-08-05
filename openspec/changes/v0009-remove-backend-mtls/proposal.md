## Why

Backend service-to-service mTLS adds certificate provisioning, rotation, and
identity-binding complexity that the selected deployment topology does not
need. External HTTPS and user authentication terminate at the Ingress and API
Gateway, while backend services communicate on the trusted internal network.

## What Changes

- **BREAKING:** remove client-certificate authentication and TLS from every
  State Registry HTTP and WebSocket listener and from every backend client.
- Run State Registry, Executors, listeners, and API Gateway-to-Registry traffic
  over plain HTTP inside the backend network; external HTTPS terminates at the
  Ingress.
- Keep Executors and listeners connected directly to State Registry without
  authentication. Their configured `team_id`, `scope`, `authorized_tag`, and
  `source_system_id` become request data validated against canonical Registry
  records, not claims authenticated from a service identity.
- Keep `scope = system` with no `team_id` and cross-team tag-based eligibility.
- Keep operator authentication at API Gateway. State Registry trusts forwarded
  Gateway context; preventing direct bypass of Gateway-only/admin routes is an
  external network-policy concern outside this change.
- Keep the secure State Registry-to-PostgreSQL connection and server-certificate
  verification unchanged.
- Accept legacy backend HTTP TLS/mTLS configuration keys for compatibility but
  ignore them and never load certificate files.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `state-registry`: replace authenticated service identities and mutually
  authenticated backend transport with unauthenticated internal HTTP and
  record/request-based scoping.
- `executor`: remove State Registry client certificates and derive Executor
  scope from configured registration data plus the server-generated cached
  `executor_id`.

## Impact

- Affected APIs: every State Registry HTTP/WebSocket endpoint and OpenAPI
  security declaration.
- Affected services: `state_registry`, `executor_docker_openhands`, planned
  `executor_k8s_openhands`, API Gateway, and listener clients.
- Affected configuration: backend TLS listener/client fields become ignored
  compatibility inputs; no certificate Secret or file mount is required.
- Affected deployment: Ingress remains the HTTPS boundary; backend network
  reachability and Gateway-route isolation are deployment responsibilities.
- Affected tests: existing v0002 transport, identity, OpenAPI, listener,
  Gateway/admin, and environment cases plus existing v0005 registration,
  system-scope, restart, Docker, and kind-based K8s cases. This change adds no
  duplicate test definition where those cases already cover the behavior.

## Out of scope

- Designing or enforcing network policy that prevents direct calls to
  Gateway-only or admin State Registry endpoints.
- Replacing mTLS with another backend authentication mechanism.
- Removing TLS or server-certificate verification from PostgreSQL connections.
- Changing external Ingress HTTPS or Gateway user-authentication behavior.
