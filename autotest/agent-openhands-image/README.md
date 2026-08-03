# agent-openhands-image

Real OpenHands V1 agent-server image used by the executor_docker_opehands
Playwright e2e tests. Built from the official
[ghcr.io/openhands/agent-server:latest-python](https://github.com/OpenHands/software-agent-sdk)
release.

## Endpoints exposed

| Method | Path | Purpose (used by executor_docker_opehands) |
|---|---|---|
| GET | /health | Health probe before submitting the prompt |
| POST | /api/conversations | Submit the task prompt; returns conversation_id |
| POST | /api/conversations/{id}/pause | Router interrupt_task handler |
| POST | /api/conversations/{id}/events | Router append_task_message handler |

Full OpenAPI spec is at GET /api/v1/openapi.json on a running container.

## Build

    DOCKER_HOST=unix:///run/user/1000/podman/podman.sock \
      docker build -t agent-openhands-image:latest .

Offline variant (pre-loaded image):

    DOCKER_HOST=unix:///run/user/1000/podman/podman.sock \
      docker build -t agent-openhands-image:latest \
        --build-arg AGENT_SERVER_IMAGE=localhost/openhands/agent-server:latest-python .

## Use in autotest

The executor_docker_opehands Playwright e2e tests configure the executor to
use this image by default. With this image, the executor's full code path
runs against a real V1 agent-server:

1. Image pull
2. Container create + start
3. /health poll -> 200
4. POST /api/conversations submits the prompt -> returns conversation_id
5. task.started + task.start_message journaled to the mocked State Registry
6. Container reachable on the host port; the executor streams events from it
7. Cleanup on SIGTERM (or test end) drains the container

## Run standalone

    docker run --rm -p 8000:8000 agent-openhands-image:latest
    curl http://localhost:8000/health

## Size

~3.8 GB (the upstream image carries the full V1 SDK + Chromium + Playwright
binaries for browser-using agents).
