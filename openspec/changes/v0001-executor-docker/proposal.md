# Change: v0001-executor-docker

## Why

FlowAI needs the first runnable worker before registries, Router, Web UI, auth, or Kubernetes support. A Docker-only Executor gives the smallest local execution slice and proves child-container supervision.

## What Changes

- Add a Docker Executor for local execution only.
- Use a mocked task server for registration, polling, and status while Router does not exist.
- Run assigned tasks only as Docker containers.
- Observe child containers and report `running_child_count`.

## Impact

- Establishes the Executor process and local Docker execution contract.
- Leaves State Registry, Env Registry, Router, Web UI, auth, and K8s Executor for later numbered changes.

## Non-Goals

- Implement Kubernetes Pod execution.
- Implement State Registry or Env Registry integration.
- Implement Router scheduling.
- Implement operator UI or authentication.
