#!/usr/bin/env bash
# scripts/go-test.sh - run `go test ./...` on every Go service.
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
    echo "[go-test] skip $svc (no go.mod yet)"
    continue
  fi
  echo "[go-test] -> apps/$svc"
  if ! (cd "$svc_dir" && go test ./...); then
    status=1
  fi
done

exit "$status"
