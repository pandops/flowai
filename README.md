# FlowAI Platform

FlowAI runs OpenHands agent tasks in Docker and Kubernetes, with durable,
team-scoped task state and an authenticated operator interface.

## Current services

| Service                       | Responsibility                                                                                                     | Documentation                                                                                                                      |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| State Registry                | PostgreSQL-backed task intake, FIFO claims, assignments, events, environments, secrets, controls, and audit        | [Service guide](svc/state-registry/README.md) · [Contract](openspec/specs/state-registry/spec.md)                                  |
| Docker OpenHands Executor     | Claims eligible tasks and supervises OpenHands containers with local capacity limits                               | [Service guide](executor/docker_openhands/README.md)                                                                               |
| Kubernetes OpenHands Executor | Claims eligible tasks and supervises OpenHands Pods, with persistent recovery state                                | [Service guide](executor/k8s-openhands/README.md)                                                                                  |
| API Gateway                   | Provider-neutral OIDC authentication, browser sessions, team resolution, and authenticated REST/WebSocket proxying | [Gateway contract](openspec/specs/api-gateway/spec.md) · [Auth contract](openspec/specs/auth/spec.md) · [Source](svc/api-gateway/) |
| Web UI                        | Operator interface for tasks, Executors, events, environments, and secrets through the Gateway                     | [Contract](openspec/specs/web-ui/spec.md) · [Source](svc/web-ui/)                                                                  |

Web UI uses API Gateway as its operator backend. Gateway forwards platform
requests to State Registry and uses the configured identity provider for login.
Executors claim tasks from State Registry before starting work and report
lifecycle events back. State Registry owns canonical task and resource state;
Gateway owns its authentication sessions and identity-to-team mappings.

Each service has its own production `Containerfile`. Runtime services run from
built images in production and integration/E2E environments. There is no Router,
standalone Env Registry, or legacy mocked task server.

## Documentation and roadmap

Start with the [documentation index](docs/README.md) and
[current architecture overview](docs/architecture/overview.md).

- [Current contracts](openspec/specs/) describe the accepted baseline.
- [Planned changes](docs/README.md#planned-changes) track proposals and unfinished work, including OpenBao, task sources, lifecycle extensions, LiteLLM, and the deferred AX evaluation.
- [Archived changes](docs/README.md#archived-changes) preserve implementation and decision history, including superseded proposals.
- [Architecture decisions](docs/adr/README.md), [diagram sources](docs/architecture/diagrams/), and the [OpenHands baseline](docs/baselines/openhands-agent-sdk-baseline.md) provide supporting context.
- [Contribution guide](CONTRIBUTING.md) covers setup, service boundaries, OpenSpec, and verification.

An archived proposal is not automatically evidence of shipped behavior; use its
status, implementation, verification records, and current contracts together.

## Development and verification

Use the Go toolchain declared in [go.mod](go.mod), Node.js/npm, and a
Docker-compatible container runtime. The complete E2E run also requires k3d,
Kubernetes tooling, and Playwright browser dependencies; see
[CONTRIBUTING.md](CONTRIBUTING.md) for setup.

```bash
npm ci
env -u GOROOT go test ./...
npm --prefix qa-e2e test
```

The E2E command requires the individual suite dependencies to be installed first.
See the [E2E guide](qa-e2e/README.md) for suites and reports. Generated reports and
traces live under the Git-ignored `qa-e2e/reports/` directory.

To build a service image, run from the repository root, for example:

```bash
docker build -f svc/state-registry/Containerfile -t flowai/state-registry:dev .
```

Image configuration and external dependencies remain service-specific; building
one image does not deploy the full platform.

## License

FlowAI is licensed under the [MIT License](LICENSE).
