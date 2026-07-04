# infra/helm

Helm charts for the FlowAI v2 services. Full chart authoring (shared chart,
event-broker, OIDC dev provider, state-store, and per-service templates) is
deferred to Task 7 (k3d + Helm foundation) per the execution plan.

This directory is intentionally empty at scaffold time. `helm lint` in
`scripts/helm-lint.sh` is therefore expected to be a no-op until charts land.
