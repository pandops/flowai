#!/usr/bin/env bash
set -euo pipefail

readonly gitleaks_image="ghcr.io/gitleaks/gitleaks:v8.29.1"
readonly trufflehog_image="docker.io/trufflesecurity/trufflehog:3.96.0"

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

scan_dir="$(mktemp -d)"
chmod 700 "$scan_dir"
trap 'rm -rf "$scan_dir"' EXIT

git diff --cached --no-ext-diff --binary --diff-filter=ACMR >"$scan_dir/staged.diff"
if [[ ! -s "$scan_dir/staged.diff" ]]; then
  echo "secret scan: no staged content"
  exit 0
fi

run_gitleaks() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "container runtime is missing; install Docker/Podman compatibility" >"$scan_dir/gitleaks.status"
    return 2
  fi
  if ! docker image inspect "$gitleaks_image" >/dev/null 2>&1; then
    echo "gitleaks image is missing; run make install-tools" >"$scan_dir/gitleaks.status"
    return 2
  fi
  if docker run --rm --interactive "$gitleaks_image" \
    stdin --redact=100 --no-banner \
    <"$scan_dir/staged.diff" >"$scan_dir/gitleaks.log" 2>&1; then
    echo "gitleaks: clean" >"$scan_dir/gitleaks.status"
    return 0
  fi
  echo "gitleaks: blocked staged content or failed" >"$scan_dir/gitleaks.status"
  return 1
}

run_trufflehog() {
  if ! command -v docker >/dev/null 2>&1; then
    echo "container runtime is missing; install Docker/Podman compatibility" >"$scan_dir/trufflehog.status"
    return 2
  fi
  if ! docker image inspect "$trufflehog_image" >/dev/null 2>&1; then
    echo "trufflehog image is missing; run make install-tools" >"$scan_dir/trufflehog.status"
    return 2
  fi
  if docker run --rm --interactive "$trufflehog_image" \
    --json --no-update --fail --fail-on-scan-errors stdin \
    <"$scan_dir/staged.diff" >"$scan_dir/trufflehog.log" 2>&1; then
    echo "trufflehog: clean" >"$scan_dir/trufflehog.status"
    return 0
  fi
  echo "trufflehog: blocked staged content or failed" >"$scan_dir/trufflehog.status"
  return 1
}

run_gitleaks &
gitleaks_pid=$!
run_trufflehog &
trufflehog_pid=$!

set +e
wait "$gitleaks_pid"
gitleaks_exit=$?
wait "$trufflehog_pid"
trufflehog_exit=$?
set -e

cat "$scan_dir/gitleaks.status" "$scan_dir/trufflehog.status"
if ((gitleaks_exit != 0 || trufflehog_exit != 0)); then
  echo "secret scan failed closed; candidate values were suppressed" >&2
  exit 1
fi

echo "parallel secret scan: PASS"
