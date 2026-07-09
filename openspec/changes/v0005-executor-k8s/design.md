# Design: K8s Executor

## Runtime Shape

- K8s Executor runs inside or against a Kubernetes cluster.
- It registers with Router as executor type `k8s`.
- It creates one Kubernetes Pod per assigned task.
- It observes Pods it started and reports running child count to Router.

## Boundaries

- Router never calls Kubernetes; only K8s Executor controls Pods.
- K8s Executor does not run Docker containers.

## Proposed Diagrams

- `specs/diagrams/01-k8s-executor-topology.puml`
- `specs/diagrams/02-k8s-task-lifecycle.puml`
