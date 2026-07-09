# Change: 005 K8s Executor

## Why

After the Docker Executor and Router path exist, FlowAI needs a cluster Executor that runs tasks as Kubernetes Pods without changing Router ownership.

## What Changes

- Add K8s Executor as a separate executor type.
- Register K8s Executors with Router using executor type `k8s`.
- Run assigned tasks only as Kubernetes Pods.
- Observe child Pods and report `running_child_count`.

## Impact

- Router can schedule to Docker or K8s Executors by executor type and routing target.
- Kubernetes execution is isolated from the earlier local Docker implementation.

## Non-Goals

- Replace Docker Executor.
- Add Web UI or auth.
- Make Router call Kubernetes directly.
