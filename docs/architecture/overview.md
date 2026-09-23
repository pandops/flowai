# Current architecture overview

FlowAI contains five runtime services, each with its own production Containerfile:

| Service                                                                 | Current responsibility                                                                                                                |
| ----------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| [State Registry](../../svc/state-registry/README.md)                    | PostgreSQL-backed canonical task/resource state, FIFO assignment, lifecycle events, environments, secrets, controls, and audit        |
| [Docker OpenHands Executor](../../executor/docker_openhands/README.md)  | Bounded execution of claimed tasks in Docker containers                                                                               |
| [Kubernetes OpenHands Executor](../../executor/k8s-openhands/README.md) | Execution of claimed tasks in Kubernetes Pods with persistent recovery state                                                          |
| [API Gateway](../../svc/api-gateway/)                                   | OIDC login, encrypted browser-session state, identity-to-team mappings, one-team working tokens, and operator REST/WebSocket proxying |
| [Web UI](../../svc/web-ui/)                                             | Browser operator interface served as an embedded artifact                                                                             |

## Request and execution boundaries

The Web UI sends operator requests to API Gateway. Gateway authenticates the
operator, resolves the selected canonical team using its own identity mappings,
and forwards verified context to State Registry. Gateway uses its configured
OIDC provider and service-owned session/mapping database; State Registry remains
the authority for platform teams, resource ownership, and task history.

Listeners submit team-bound tasks to State Registry. Executors discover eligible
pending tasks and atomically claim the oldest eligible task before starting
execution. Each uses the Registry-resolved image and scoped environment,
supervises OpenHands in its runtime, and reports task events to State Registry.
Executor capacity is enforced locally. Immutable `team_id`, rather than a display
name or image reference, is the resource-authorization boundary.

Runtime services run from built images. External HTTPS terminates at the ingress;
backend HTTP caller boundaries rely on deployment network policy. There is no
Router, legacy mocked task server, or separate Env Registry. Test-only fixtures
are not production services.

## Contracts and history

- [State Registry contract](../../openspec/specs/state-registry/spec.md)
- [Executor contract](../../openspec/specs/executor/spec.md)
- [Gateway contract](../../openspec/specs/api-gateway/spec.md)
- [Authentication contract](../../openspec/specs/auth/spec.md)
- [Web UI contract](../../openspec/specs/web-ui/spec.md)
- [Planned changes](../README.md#planned-changes)
- [Archived changes](../README.md#archived-changes)

The change archive records the Docker bootstrap, durable registry, Kubernetes
Executor, Web UI, backend transport changes, service containerization, and
provider-neutral authentication. See each archived change's tasks and verification
records for its delivered scope. New proposals, including OpenBao and AX, do not
change this current-state overview until accepted and synced.
