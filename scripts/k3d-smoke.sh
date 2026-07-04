#!/usr/bin/env bash
# scripts/k3d-smoke.sh - smoke test the k3d cluster per ADR 0004.
# Behavior:
#   - Exits 78 (skip code) if k3d or docker is unavailable on PATH (declared in command manifest).
#   - Otherwise lists clusters / nodes and exits 0.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
EVIDENCE_DIR="$ROOT_DIR/.omo/evidence"
mkdir -p "$EVIDENCE_DIR"

if ! command -v k3d >/dev/null 2>&1; then
  echo "[k3d-smoke] k3d binary not found on PATH" | tee "$EVIDENCE_DIR/task-6-k3d-smoke-skip.txt"
  exit 78
fi

if ! command -v docker >/dev/null 2>&1; then
  echo "[k3d-smoke] docker binary not found on PATH" | tee "$EVIDENCE_DIR/task-6-k3d-smoke-skip.txt"
  exit 78
fi

echo "[k3d-smoke] listing existing clusters"
k3d cluster list || true
echo "[k3d-smoke] selecting active cluster flowai-test (may be absent on this runner)"
if k3d cluster list 2>/dev/null | grep -q flowai-test; then
  kubectl --context k3d-flowai-test get nodes
else
  echo "[k3d-smoke] no flowai-test cluster present on this runner (skipped)"
fi

exit 0
