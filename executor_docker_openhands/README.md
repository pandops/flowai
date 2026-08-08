# Docker OpenHands Executor

`executor_docker_openhands` is the concrete FlowAI worker for Docker and
OpenHands. State Registry owns all canonical platform state.

## Runtime flow

1. Validate the backend HTTP State Registry URL and immutable scope inputs.
2. Remove orphaned containers carrying the stable cleanup identity.
3. Register one immutable scope (`team` or `system`) and one authorized tag.
4. Poll only while a local container slot is free.
5. Claim the oldest eligible task atomically with a stable `command_id`.
6. Use the claim's `resolved_image` verbatim; no local fallback exists.
7. Open an optional environment with the claim-issued scope token.
8. Pull/start Docker and create an OpenHands conversation.
9. Append `running`, followed by exactly one of `finished` or `failed`.
10. Remove the container and release its port and slot.

The Executor never contacts a mocked task server, Router, API Gateway, Web UI,
or separate Env Registry.

The checked-in example is
[configs/executor_docker_openhands.yaml](configs/executor_docker_openhands.yaml).
It requires a State Registry HTTP URL, one scope/tag, a team ID
only for team scope, Docker access, a bounded port range, and either an
OpenHands agent profile or complete LLM settings.

```bash
env -u GOROOT go run ./executor_docker_openhands/cmd/executor_docker_openhands \
  -config executor_docker_openhands/configs/executor_docker_openhands.yaml
env -u GOROOT go test ./executor_docker_openhands/...
```

Local probes are `GET /v1/livez` and `GET /v1/readyz`.
