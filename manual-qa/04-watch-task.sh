#!/usr/bin/env bash
# 04-watch-task.sh — read the canonical task and its event history
# through the trusted-Gateway mTLS surface until the task reaches a
# terminal state (finished | failed), or until a deadline elapses.
#
# Usage:
#   ./04-watch-task.sh                 # use the task_id persisted by 03-submit-task.sh
#   ./04-watch-task.sh <task_id> [timeout_seconds]
#
# Default timeout: 600 seconds (10 minutes). The Executor can take a
# while to boot the OpenHands V1 agent-server, talk to the LLM, and
# emit the terminal event.
#
# After the task reaches a terminal state, the script also points the
# operator at the matching Podman container (filtered by
# flowai.task_id=$TASK_ID and flowai.executor_id=exec-local-openhands)
# and the per-process logs.

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

# Placeholders so ShellCheck treats these as assigned; overridden at runtime.
: "${FLOWAI_STATE_REGISTRY_URL:=}"

# --- required tools -------------------------------------------------------

mq::require_cmd podman
mq::require_cmd openssl

# --- state init FIRST so every later mq::state_path is safe ----------------

mq::state_init

# --- input validation -----------------------------------------------------

TASK_ID="${1:-}"
if [[ -z "$TASK_ID" ]]; then
  TASK_ID_FILE="$(mq::state_path state/task.id)"
  if [[ -f "$TASK_ID_FILE" ]]; then
    TASK_ID="$(cat "$TASK_ID_FILE")"
  fi
fi
[[ -n "$TASK_ID" ]] || mq::fail "04-watch-task: provide a task_id as argv[1] or run 03-submit-task.sh first"

TIMEOUT_S="${2:-600}"
if ! [[ "$TIMEOUT_S" =~ ^[0-9]+$ ]] || [[ "$TIMEOUT_S" -le 0 ]]; then
  mq::fail "04-watch-task: timeout must be a positive integer (got '$TIMEOUT_S')"
fi
if [[ "$TIMEOUT_S" -gt 86400 ]]; then
  mq::fail "04-watch-task: timeout capped at 86400 seconds (got $TIMEOUT_S)"
fi

mq::state_require_env

CA_DIR="$(mq::state_path state/certs)"
[[ -f "$CA_DIR/gateway.crt" ]] || mq::fail "04-watch-task: gateway cert missing; run 01-prepare.sh first"

# --- terminal-state detection ---------------------------------------------

# We poll the task point-read surface; once current_state hits a
# terminal value we print the canonical event history.
REG_URL="${FLOWAI_STATE_REGISTRY_URL:-https://localhost:18443}"
POINT_URL="$REG_URL/v1/tasks/$TASK_ID"
EVENTS_URL="$REG_URL/v1/tasks/$TASK_ID/events"

DEADLINE=$(( $(date +%s) + TIMEOUT_S ))
LAST_STATE=""
LAST_BODY=""

while :; do
  if [[ $(date +%s) -ge $DEADLINE ]]; then
    mq::fail "04-watch-task: task did not reach a terminal state within ${TIMEOUT_S}s (last state: ${LAST_STATE:-unknown})"
  fi

  RESP="$(mq::curl_gateway_get "$POINT_URL")" \
    || mq::fail "GET /v1/tasks/$TASK_ID: $RESP"
  STATUS="${RESP%%$'\n'*}"
  BODY="${RESP#*$'\n'}"
  if [[ "$STATUS" != "200" ]]; then
    # 404 is expected briefly between ingestion and the gateway's
    # eventual-consistency window; tolerate a bounded number of them.
    if [[ "$STATUS" == "404" ]]; then
      sleep 0.5
      continue
    fi
    mq::fail "GET /v1/tasks/$TASK_ID returned $STATUS body=$BODY"
  fi

  LAST_BODY="$BODY"
  LAST_STATE="$(printf '%s' "$BODY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["current_state"])')" \
    || mq::fail "extract current_state: $BODY"

  case "$LAST_STATE" in
    finished|failed)
      break
      ;;
    pending|created|running)
      sleep 1
      continue
      ;;
    *)
      mq::fail "task entered unknown state: $LAST_STATE"
      ;;
  esac
done

# --- print canonical task row + ordered event history --------------------

printf '\n=== task (gateway point read) ===\n'
printf '%s\n' "$LAST_BODY" | python3 -m json.tool

printf '\n=== event history ===\n'
RESP="$(mq::curl_gateway_get "$EVENTS_URL")" || mq::fail "GET events: $RESP"
STATUS="${RESP%%$'\n'*}"
BODY="${RESP#*$'\n'}"
if [[ "$STATUS" != "200" ]]; then
  mq::fail "GET events returned $STATUS body=$BODY"
fi

printf '%s\n' "$BODY" | python3 -c '
import json, sys
page = json.load(sys.stdin)
items = page.get("items", [])
print(f"event_count={len(items)}")
hdr = "{:36}  {:28}  occurred_at".format("event_id", "type")
print(hdr)
print("-" * 96)
for ev in items:
    row = "{:36}  {:28}  {}".format(
        ev.get("event_id", ""),
        ev.get("event_type", ""),
        ev.get("occurred_at", ""),
    )
    print(row)
'

# --- companion diagnostics ------------------------------------------------

printf '\n=== runtime diagnostics ===\n'
printf 'task_id=%s\n' "$TASK_ID"
printf 'state-registry: pid=%s log=%s\n' \
  "$(cat "$(mq::state_path state/registry.pid)")" \
  "$(mq::state_path logs/state-registry.log)"
printf 'executor:       pid=%s log=%s\n' \
  "$(cat "$(mq::state_path state/executor.pid)")" \
  "$(mq::state_path logs/executor.log)"

# Show the Podman container(s) tied to this specific task. The
# runtime sets flowai.executor_id=exec-local-openhands on every slot
# and flowai.task_id=<task_id> per task, so we filter on BOTH labels.
RT="$(mq::detect_runtime)"
printf 'openhands container (may be gone if task already cleaned up):\n'
"$RT" ps -a \
  --filter "label=flowai.executor_id=exec-local-openhands" \
  --filter "label=flowai.task_id=$TASK_ID" \
  --format 'table {{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Ports}}' \
  2>/dev/null || true

printf '\nfinal state: %s\n' "$LAST_STATE"