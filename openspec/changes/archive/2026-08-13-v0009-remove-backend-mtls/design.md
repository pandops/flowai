## Context

State Registry currently terminates mTLS and derives listener, Executor, and
Gateway service identities from client certificates. Docker Executor already
contains an mTLS client; v0005 plans the same for K8s. The selected deployment
instead treats Ingress as the TLS boundary and the API Gateway as the user
authentication boundary. Backend HTTP runs on a trusted internal network.

## Goals / Non-Goals

**Goals:**

- Remove TLS listeners, client certificates, certificate loading, certificate
  mounts, and certificate-derived identity from all backend HTTP clients.
- Preserve team/system scope, FIFO claims, immutable assignments, event
  ordering, Gateway-forwarded operator context, and listener source scoping.
- Preserve secure PostgreSQL transport and server identity validation.
- Make legacy backend TLS configuration harmless during rollout.

**Non-Goals:**

- Add replacement service authentication, bearer credentials, API keys, or
  request signing.
- Prevent network-level bypass of Gateway-only/admin endpoints.
- Change external Ingress HTTPS or Gateway user authentication.

## Decisions

### Backend HTTP is unauthenticated

State Registry listens on HTTP and WS; all backend clients use `http://` and
`ws://`. No TLS configuration object is constructed and no certificate file is
read. HTTPS terminates at Ingress. Alternative rejected: one-way backend HTTPS,
because the selected topology explicitly places TLS termination at Ingress.

### Authorization uses canonical records and submitted configuration

Registration uses the existing `PUT /v1/executors/{executor_id}` lifecycle and
contains configured `scope`, optional `team_id`, and tag. Refresh and task
operations resolve that Executor-supplied ID to the canonical Executor record
and apply its persisted scope/team/tag predicates. The identifier proves no
caller identity. Listener ingestion uses
submitted configured `team_id` and `source_system_id`, validated only for
existence, relationship, and payload consistency. Alternative rejected: a new
token or key, because replacement backend authentication is out of scope.

### Gateway context remains trusted input

Gateway authenticates users and forwards operator/team/request context over
HTTP. State Registry consumes that context without authenticating the Gateway.
Ingress/network policy preventing direct spoofed requests is explicitly an
external deployment responsibility.

### Legacy TLS configuration is ignored

Existing HTTP TLS/mTLS keys remain parseable but do not load files, alter the
listener, or affect readiness. This supports staged configuration cleanup.
New examples and manifests omit the keys. A later change may remove them from
the schema after deployments converge.

### PostgreSQL security is independent

Database TLS settings and server certificate-chain/hostname verification stay
active. Removing backend HTTP mTLS must not change the database transport.

## Risks / Trade-offs

- [Any network peer can impersonate a backend service] → document the trusted
  network assumption and leave network-policy enforcement to deployment.
- [`executor_id` can be replayed] → retain assignment, scope, team, tag, FIFO,
  and lifecycle checks, while explicitly not treating the ID as a credential.
- [Gateway/admin headers can be spoofed through direct access] → record this as
  an accepted out-of-scope deployment risk.
- [Ignored keys hide stale configuration] → log one plaintext-free deprecation
  warning naming keys only; never access referenced certificate paths.

## Migration Plan

1. Add HTTP-only State Registry and clients while accepting ignored legacy
   fields; update OpenAPI and deployment manifests.
2. Deploy backend services and switch service URLs to `http://`/`ws://`.
3. Remove certificate Secrets, volumes, and mounts after all clients are HTTP.
4. Keep Ingress HTTPS and PostgreSQL TLS enabled throughout.
5. Roll back by redeploying the prior binaries and certificate mounts together;
   a partial rollback is unsupported because HTTP and mTLS endpoints differ.

## Open Questions

None. Network isolation and any future service authentication are separate
changes.
