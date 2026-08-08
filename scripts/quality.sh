#!/usr/bin/env bash
set -euo pipefail

mode="${1:-check}"
repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

mapfile -d '' staged < <(
  git diff --cached --name-only --diff-filter=ACMR -z
)

go_files=()
prettier_files=()
markdown_files=()
for file in "${staged[@]}"; do
  [[ -f "$file" ]] || continue
  case "$file" in
    *.go) go_files+=("$file") ;;
    *.ts|*.tsx|*.js|*.cjs|*.mjs|*.json|*.yaml|*.yml|*.md)
      prettier_files+=("$file")
      [[ "$file" == *.md ]] && markdown_files+=("$file")
      ;;
  esac
done

if [[ "$mode" == "format" ]]; then
  (("${#go_files[@]}" == 0)) || gofmt -w "${go_files[@]}"
  (("${#prettier_files[@]}" == 0)) || npx prettier --write "${prettier_files[@]}"
  (("${#markdown_files[@]}" == 0)) || npx markdownlint-cli2 --fix "${markdown_files[@]}"
  exit 0
fi

unformatted_go=""
if (("${#go_files[@]}" > 0)); then
  unformatted_go="$(gofmt -l "${go_files[@]}")"
fi
if [[ -n "$unformatted_go" ]]; then
  printf 'gofmt required:\n%s\nRun: make format\n' "$unformatted_go" >&2
  exit 1
fi

(("${#prettier_files[@]}" == 0)) ||
  npx prettier --check "${prettier_files[@]}"
(("${#markdown_files[@]}" == 0)) ||
  npx markdownlint-cli2 "${markdown_files[@]}"

if [[ "$mode" == "precommit" ]]; then
  if ! golangci-lint version | grep -q 'version 2\.11\.4 '; then
    echo "golangci-lint v2.11.4 is required; run make install-tools" >&2
    exit 1
  fi
  golangci-lint run ./...
  git diff --cached --check
fi
