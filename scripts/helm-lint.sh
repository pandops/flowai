#!/usr/bin/env bash
# scripts/helm-lint.sh - run `helm lint` over every chart under infra/helm.
# Exits 0 even when there are no charts yet (scaffold phase).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
HELM_DIR="$ROOT_DIR/infra/helm"

if [ ! -d "$HELM_DIR" ]; then
  echo "[helm-lint] infra/helm does not exist yet (deferred to infra task)"
  exit 0
fi

shopt -s nullglob
charts=("$HELM_DIR"/*/Chart.yaml "$HELM_DIR"/*/chart.yaml)
shopt -u nullglob

if [ "${#charts[@]}" -eq 0 ]; then
  echo "[helm-lint] no Helm charts yet (deferred to infra task)"
  exit 0
fi

if ! command -v helm >/dev/null 2>&1; then
  echo "[helm-lint] helm binary not found on PATH; skipping" >&2
  exit 78
fi

status=0
for chart in "${charts[@]}"; do
  echo "[helm-lint] -> $chart"
  if ! helm lint "$(dirname "$chart")"; then
    status=1
  fi
done

exit "$status"
