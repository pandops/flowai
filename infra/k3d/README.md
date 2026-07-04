# infra/k3d

k3d cluster manifests and smoke-test scripts for the FlowAI v2 dev/test
loop. Full k3d foundation (cluster definition, namespace layout, smoke
scripts) lands in Task 7.

The `k3d_smoke` command (`scripts/k3d-smoke.sh`) is wired both locally and
in `.gitlab-ci.yml` and tolerates runners without k3d/docker by exiting
skip code 78 (declared in `.omo/artifacts/command-manifest.yaml`).
