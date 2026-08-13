#!/usr/bin/env bash
# Bootstrap a pinned non-sudo k3d binary next to this repository.
#
# The harness requires `k3d` to start a fresh temporary Kubernetes
# cluster per Playwright suite run. The platform pins
# `k3d v5.9.0` so E2E verification is reproducible across machines.
# Operators may opt to install k3d from a different channel; the
# harness will fall back to any `k3d` on `$PATH` reporting the
# pinned version, then require the suite-level cluster-lifecycle
# rules from autotest/executor_k8s_openhands to keep reproducible
# behaviour.
#
# This script never uses sudo and lives under the user's
# `~/.local/bin` directory. It is invoked once per machine and is
# idempotent: a binary with the expected version is a no-op re-run.

set -euo pipefail

K3D_VERSION="${K3D_VERSION:-v5.9.0}"
INSTALL_DIR="${K3D_INSTALL_DIR:-$HOME/.local/bin}"
BIN_NAME="${K3D_BIN_NAME:-k3d}"

if ! command -v curl >/dev/null 2>&1; then
  echo "install-k3d: curl is required to download the pinned k3d binary" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
target="$INSTALL_DIR/$BIN_NAME"
expected_version="k3d version $K3D_VERSION"

if [ -x "$target" ]; then
  if [ "$("$target" version 2>/dev/null | head -n1 | tr -d '\n')" = "$expected_version" ]; then
    echo "install-k3d: $target already at $expected_version; nothing to do"
    exit 0
  fi
fi

tmp="$(mktemp -t flowai-k3d.XXXXXX)"
trap 'rm -f "$tmp"' EXIT

url="https://github.com/k3d-io/k3d/releases/download/${K3D_VERSION}/k3d-linux-amd64"
echo "install-k3d: downloading $url"
curl --fail --silent --show-error --location --output "$tmp" "$url"
chmod +x "$tmp"

mv "$tmp" "$target"
echo "install-k3d: installed $target ($expected_version)"
