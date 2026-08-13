#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
git -C "$repo_root" config core.hooksPath .hooks
printf 'Installed repository hooks from %s/.hooks\n' "$repo_root"
