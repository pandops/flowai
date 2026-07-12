# Change: v0001-executor-docker

## Why

FlowAI needs the first runnable worker before registries, Router, Web UI, auth, or Kubernetes support. The MVP scope is intentionally narrow: a single Executor process that runs bounded OpenHands containers, controls their lifecycle, pulls work from the Router, and forwards state to the future State Registry.

## What Changes

- Add a Docker-based Executor process that runs multiple OpenHands containers concurrently up to configured capacity.
- Each task container runs the OpenHands agent runtime (`ghcr.io/openhands/agent-server:latest-python`), which provides a REST/WebSocket API on port 8000 inside the container.
- The Executor pulls the OpenHands image, starts containers up to configured capacity, exposes each container's port 8000 on a distinct host port, observes lifecycle via Docker events, and forwards state changes to the mocked task server's State Registry surface.
- On startup, the Executor generates a unique `executor_id` UID, registers the active Executor instance with the mocked State Registry, and lists Router tasks with `GET /v1/tasks?filter=<tag>` for task start, task interrupt, and task message delivery. Without `filter`, Router returns all tasks.
- The Executor exposes only its own localhost platform health probes (`GET /v1/livez` and `GET /v1/readyz`). It is not an OpenHands proxy and does not expose local task-control endpoints.
- The mocked State Registry surface records executor lifecycle information and task lifecycle events (`task.started`, `task.finished`, `task.failed`, etc.) in memory for the lifetime of the mocked task server process.
- The mocked task server imitates three eventually-separate services — Router, State Registry, Env Registry — preserving wire shapes so v0002/v0003/v0004 swap in as URL changes.
- All Go backend services in this change follow the stateless platform baseline: golang-standards layout, `net/http` + `chi`, `slog` JSON logs, YAML config, and `/v1/livez` + `/v1/readyz` probes.
- One Executor = one bounded worker process. Capacity is configured by properties; future Executors for different agent runtimes (e.g. Claude Code) are separate processes, not extensions of this one.

## Impact

- Establishes the Executor-to-OpenHands integration pattern for the platform.
- Locks in wire shapes for Router, State Registry, and Env Registry surfaces the Executor calls, so v0002/v0003/v0004 can replace the mocked server without Executor code changes.
- Proves the State Registry event-forwarding pattern end-to-end.
- Leaves K8s Executors, Web UI, auth, real registries, and other agent runtimes for later numbered changes.

## Non-Goals

- Run unbounded containers per Executor.
- Implement Kubernetes Pod execution.
- Implement real State Registry, Env Registry, or Router.
- Persist mocked Router, State Registry, or Env Registry data across mocked task server restarts; durable State Registry persistence belongs to `v0002-state-registry`.
- Implement operator UI or authentication.
- Proxy OpenHands through the Executor API.
- Expose Executor REST endpoints for task start, task interrupt, task messages, or OpenHands proxying.
- Support multiple agent runtimes inside a single Executor.
- Re-attach surviving containers across Executor restarts (Executor death ends owned containers).
