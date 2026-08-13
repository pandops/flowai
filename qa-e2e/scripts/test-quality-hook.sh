#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT

git -C "$tmp_dir" init -q
git -C "$tmp_dir" config user.name test
git -C "$tmp_dir" config user.email test@example.invalid
printf 'package sample\nfunc main(){println("x")}\n' >"$tmp_dir/main.go"
git -C "$tmp_dir" add main.go

if (cd "$tmp_dir" && bash "$repo_root/.hooks/quality.sh" check) >/dev/null 2>&1; then
  echo "quality check unexpectedly accepted unformatted Go" >&2
  exit 1
fi

env -u GOROOT gofmt -w "$tmp_dir/main.go"
git -C "$tmp_dir" add main.go
(cd "$tmp_dir" && bash "$repo_root/.hooks/quality.sh" check)
echo "precommit rejects unformatted staged Go: PASS"
