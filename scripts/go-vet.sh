#!/usr/bin/env bash
# scripts/go-vet.sh - run `go vet` on every Go service. Exits 0 if nothing to vet.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
GO_SERVICES=("lifecycle-manager" "router" "secret-service")

status=0
for svc in "${GO_SERVICES[@]}"; do
  svc_dir="$ROOT_DIR/apps/$svc"
  if [ ! -d "$svc_dir" ]; then
    continue
  fi
  if [ ! -f "$svc_dir/go.mod" ]; then
    echo "[go-vet] skip $svc (no go.mod yet)"
    continue
  fi
  echo "[go-vet] -> apps/$svc"
  if ! (cd "$svc_dir" && go vet ./...); then
    status=1
  fi
done

exit "$status"
