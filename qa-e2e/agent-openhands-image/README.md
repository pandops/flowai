# agent-openhands-image

Real OpenHands V1 agent-server image shared by Docker and K8s Executor smoke
tests. Deterministic agent API contract and failure tests use a compatible
mock; this image is reserved for the real-runtime task gate. Built from the official
[ghcr.io/openhands/agent-server:latest-python](https://github.com/OpenHands/software-agent-sdk)
release.

The smoke gate runs this real agent-server against a local deterministic
OpenAI-compatible mock LLM. The mock scripts the minimal tool-call sequence
that writes a unique workspace marker and then completes the conversation.
Both Docker and `kind` use the same mock behavior; no external LLM endpoint or
real API key is required.

## Endpoints exposed

| Method | Path                           | Purpose (used by executor_docker_openhands)     |
| ------ | ------------------------------ | ----------------------------------------------- |
| GET    | /health                        | Health probe before submitting the prompt       |
| POST   | /api/conversations             | Submit the task prompt; returns conversation_id |
| POST   | /api/conversations/{id}/pause  | Router interrupt_task handler                   |
| POST   | /api/conversations/{id}/events | Router append_task_message handler              |

Full OpenAPI spec is at GET /api/v1/openapi.json on a running container.

## Build

    DOCKER_HOST=unix:///run/user/1000/podman/podman.sock \
      docker build -t agent-openhands-image:latest .

Offline variant (pre-loaded image):

    DOCKER_HOST=unix:///run/user/1000/podman/podman.sock \
      docker build -t agent-openhands-image:latest \
        --build-arg AGENT_SERVER_IMAGE=localhost/openhands/agent-server:latest-python .

## Use in qa-e2e

The Docker and K8s smoke tests use the same built image; the K8s harness loads
it into `kind`. With this image, the executor's full code path
runs against a real V1 agent-server:

1. Image pull
2. Container create + start
3. /health poll -> 200
4. POST /api/conversations submits the prompt -> returns conversation_id
5. task.started + task.start_message journaled to the mocked State Registry
6. Container reachable on the host port; the executor streams events from it
7. Cleanup on SIGTERM (or test end) drains the container

The harness supplies the local mock LLM base URL and a non-secret placeholder
token through the agent-server's supported model configuration. Outbound LLM
network access is denied so an accidental fallback cannot consume credentials
or make the smoke test non-deterministic.

## Run standalone

    docker run --rm -p 8000:8000 agent-openhands-image:latest
    curl http://localhost:8000/health

## Size

~3.8 GB (the upstream image carries the full V1 SDK + Chromium + Playwright
binaries for browser-using agents).
