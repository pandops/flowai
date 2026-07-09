# Change: v0004-router

## Why

After Docker Executor, State Registry, and Env Registry exist independently, FlowAI needs the real Router to replace mocked scheduling and become the only broker.

## What Changes

- Add Router as the task queue manager and only broker.
- Replace mocked Executor scheduling with Router registration, ready, pull, status, and running-child-count APIs.
- Keep Router isolated from State Registry, Env Registry, API Gateway, and Web UI.

## Impact

- Automation submits tasks to Router.
- Docker Executor pulls work from Router instead of the mocked task server.
- Router owns transient queue, wait-list, assignment, and Executor-pool state.

## Non-Goals

- Implement K8s Executor.
- Implement Web UI or auth.
- Store historical records, env values, or secret values in Router.
