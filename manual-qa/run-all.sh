#!/usr/bin/env bash
# run-all.sh — drive 01..04 + 99 in order with a single prompt arg.
#
# This script is a thin orchestrator; it does NOT hide the required
# LLM input. Either OPENHANDS_LLM_MODEL + OPENHANDS_LLM_API_KEY +
# OPENHANDS_LLM_USAGE_ID or OPENHANDS_AGENT_PROFILE_ID must be set
# BEFORE run-all.sh is invoked; 02-start-executor.sh will fail closed
# otherwise. The prompt is argv[1].
#
# Cleanup behaviour:
#   * The EXIT trap runs 99-cleanup.sh unless RUN_ALL_NO_CLEANUP=1.
#     A successful run therefore tears down Postgres + State Registry +
#     Executor at the end, so a re-run starts from a clean directory.
#   * INT/TERM are NOT routed to the cleanup trap. They translate
#     directly to exit codes 130 / 143 (the conventional shell codes
#     for SIGINT / SIGTERM) so a signal cannot be silently swallowed
#     as a successful exit 0.
#   * The one-shot re-entry guard (MQ_RUN_ALL_CLEANED) prevents the
#     cleanup script's own exit from looping back into the EXIT trap.
#
# Usage:
#   OPENHANDS_LLM_MODEL=... \
#   OPENHANDS_LLM_API_KEY=... \
#   OPENHANDS_LLM_USAGE_ID=... \
#   ./run-all.sh "Inspect /etc/hostname and report the value."
#
# Optional env:
#   RUN_ALL_NO_CLEANUP=1    skip the EXIT-trap cleanup (advanced).

set -o errexit
set -o pipefail
set -o nounset
set -o errtrace
umask 0077

MQ_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
# shellcheck disable=SC1091
source "$MQ_SCRIPT_DIR/lib.sh"
mq::enter_strict_mode
mq::load_manual_qa_dotenv "$MQ_SCRIPT_DIR"

PROMPT="${1:-${PROMPT:-}}"
if [[ -z "$PROMPT" ]]; then
  printf 'usage: %s "your prompt here"\n' "$0" >&2
  printf '       OPENHANDS_LLM_* / OPENHANDS_AGENT_PROFILE_ID must be exported.\n' >&2
  exit 2
fi

# One-shot re-entry guard so 99-cleanup.sh's own exit cannot loop back
# into the EXIT trap.
MQ_RUN_ALL_CLEANED=0
cleanup() {
  local rc=$?
  if [[ "${MQ_RUN_ALL_CLEANED:-0}" -ne 1 ]]; then
    MQ_RUN_ALL_CLEANED=1
    if [[ "${RUN_ALL_NO_CLEANUP:-0}" -ne 1 ]]; then
      "$MQ_SCRIPT_DIR/99-cleanup.sh" || true
    fi
  fi
  exit "$rc"
}
# Signal handlers translate directly to the conventional exit codes
# so a signal-driven termination cannot become exit 0.
trap 'exit 130' INT
trap 'exit 143' TERM
trap cleanup EXIT

"$MQ_SCRIPT_DIR/01-prepare.sh"
"$MQ_SCRIPT_DIR/02-start-executor.sh"
PROMPT="$PROMPT" "$MQ_SCRIPT_DIR/03-submit-task.sh"
"$MQ_SCRIPT_DIR/04-watch-task.sh"