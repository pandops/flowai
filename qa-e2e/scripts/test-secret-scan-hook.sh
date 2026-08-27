#!/usr/bin/env bash
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

git -C "$fixture" init -q
git -C "$fixture" config user.name test
git -C "$fixture" config user.email test@example.invalid
mkdir -p "$fixture/.hooks" "$fixture/node_modules" "$fixture/bin"
cp "$repo_root/.hooks/pre-commit" "$fixture/.hooks/pre-commit"
cp "$repo_root/.hooks/secret-scan.sh" "$fixture/.hooks/secret-scan.sh"
git -C "$fixture" config core.hooksPath .hooks

cat >"$fixture/bin/make" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
cat >"$fixture/bin/golangci-lint" <<'EOF'
#!/usr/bin/env bash
echo 'golangci-lint has version 2.11.4 built with go1.26'
EOF
cat >"$fixture/bin/docker" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == image && "${2:-}" == inspect ]]; then exit 0; fi
image=''
for arg in "$@"; do
  [[ "$arg" == *gitleaks* || "$arg" == *trufflehog* ]] && image="$arg"
done
cat >/dev/null
if [[ "$image" == *gitleaks* ]]; then
  name=gitleaks
  result="${GITLEAKS_RESULT:-clean}"
  finding_exit=1
else
  name=trufflehog
  result="${TRUFFLEHOG_RESULT:-clean}"
  finding_exit=183
fi
printf '%s start %s\n' "$name" "$(date +%s%N)" >>"$SCANNER_EVENTS"
sleep 0.4
printf '%s end %s\n' "$name" "$(date +%s%N)" >>"$SCANNER_EVENTS"
[[ "$result" == clean ]] || { echo "$SECRET_SENTINEL"; exit "$finding_exit"; }
EOF
chmod +x "$fixture/bin/"*

printf 'clean\n' >"$fixture/input.txt"
git -C "$fixture" add input.txt
events="$fixture/events.log"
sentinel='must-not-appear-secret-value'
export SCANNER_EVENTS="$events" SECRET_SENTINEL="$sentinel"

run_hook() {
  (cd "$fixture" && PATH="$fixture/bin:$PATH" .hooks/pre-commit) 2>&1
}

clean_output="$(run_hook)"
starts_max="$(awk '$2=="start" {if ($3>max) max=$3} END {print max}' "$events")"
ends_min="$(awk '$2=="end" {if (!min || $3<min) min=$3} END {print min}' "$events")"
[[ -n "$starts_max" && -n "$ends_min" && "$starts_max" -lt "$ends_min" ]] || {
  echo 'secret scanners did not overlap' >&2
  cat "$events" >&2
  exit 1
}

: >"$events"
set +e
finding_output="$(GITLEAKS_RESULT=finding run_hook)"
finding_exit=$?
set -e
[[ "$finding_exit" -ne 0 ]] || { echo 'finding did not block commit' >&2; exit 1; }
[[ "$finding_output" != *"$sentinel"* ]] || { echo 'hook exposed secret value' >&2; exit 1; }
grep -q '^trufflehog end ' "$events" || { echo 'hook did not wait for TruffleHog' >&2; exit 1; }

: >"$events"
set +e
error_output="$(TRUFFLEHOG_RESULT=error run_hook)"
error_exit=$?
set -e
[[ "$error_exit" -ne 0 ]] || { echo 'scanner error did not block commit' >&2; exit 1; }
[[ "$error_output" != *"$sentinel"* ]] || { echo 'hook exposed scanner output' >&2; exit 1; }
grep -q '^gitleaks end ' "$events" || { echo 'hook did not wait for Gitleaks' >&2; exit 1; }

printf 'parallel secret-scanner pre-commit gate: PASS\n'
