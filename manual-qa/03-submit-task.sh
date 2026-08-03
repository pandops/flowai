#!/usr/bin/env bash
# 03-submit-task.sh — ingest a task via the listener mTLS surface.
#
# The prompt is taken from argv[1] (preferred) or the PROMPT env var.
# The body is built with the safe JSON encoder (jq or python3) so a
# prompt with quotes / newlines / backslashes cannot break the wire
# shape. The listener side rejects any listener-supplied required_tag
# at the header and JSON layers, so we never send it.
#
# Dedupe contract: the State Registry uses
# (team_id, source_system_id, source_id) as the idempotency key. We
# derive source_id from a SHA-256 of the prompt so:
#   * a retry of the SAME prompt in this runtime directory returns
#     the EXISTING canonical task_id (200 instead of 201), and
#   * two distinct prompts are distinct tasks.
# The dedupe is per-run, not just per-second: the source_id is stable
# for as long as the prompt text is stable, even across long delays
# or after a previous run within the same runtime directory.
#
# Usage:
#   ./03-submit-task.sh "List the contents of /etc/hostname and report."
#   PROMPT="..." ./03-submit-task.sh

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
: "${FLOWAI_TEAM_ID:=}"
: "${FLOWAI_SOURCE_SYSTEM_ID:=}"
: "${FLOWAI_TASK_TYPE_ID:=}"

# --- required tools -------------------------------------------------------

mq::require_cmd python3
mq::require_cmd openssl
mq::require_cmd curl

# --- input validation -----------------------------------------------------

PROMPT="${1:-${PROMPT:-}}"
if [[ -z "$PROMPT" ]]; then
  mq::fail "03-submit-task: provide the prompt as argv[1] or via the PROMPT env var"
fi
if [[ "${#PROMPT}" -gt 4096 ]]; then
  mq::fail "03-submit-task: prompt is ${#PROMPT} bytes; cap is 4096"
fi

mq::state_init
mq::state_require_env

# --- listener cert validation --------------------------------------------

# Surface a clear diagnostic when the listener cert lacks the team
# binding that 01-prepare.sh should have set up.
CA_DIR="$(mq::state_path state/certs)"
[[ -f "$CA_DIR/listener.crt" ]] || mq::fail "03-submit-task: listener cert missing; run 01-prepare.sh first"

# Read the cert subject fields and compare them against the persisted
# identifiers; the listener mTLS identity MUST match what the State
# Registry recorded at admin onboarding time.
LISTENER_CN="$(openssl x509 -in "$CA_DIR/listener.crt" -noout -subject \
  | sed -n 's/.*CN[[:space:]]*=[[:space:]]*\([^,/]*\).*/\1/p')"
LISTENER_OU="$(openssl x509 -in "$CA_DIR/listener.crt" -noout -subject \
  | sed -n 's/.*OU[[:space:]]*=[[:space:]]*\([^,/]*\).*/\1/p')"
LISTENER_O="$(openssl x509 -in "$CA_DIR/listener.crt" -noout -subject \
  | sed -n 's/.*O[[:space:]]*=[[:space:]]*\([^,/]*\).*/\1/p')"
LISTENER_SN="$(openssl x509 -in "$CA_DIR/listener.crt" -noout -subject \
  | sed -n 's/.*serialNumber[[:space:]]*=[[:space:]]*\([^,/]*\).*/\1/p')"

if [[ "$LISTENER_CN" != "listener-local" ]] || [[ "$LISTENER_OU" != "listener" ]]; then
  mq::fail "listener cert subject CN=$LISTENER_CN OU=$LISTENER_OU does not match listener-local/OU=listener"
fi
if [[ "$LISTENER_O" != "$FLOWAI_TEAM_ID" ]]; then
  mq::fail "listener cert O=$LISTENER_O does not match persisted FLOWAI_TEAM_ID=$FLOWAI_TEAM_ID"
fi
if [[ "$LISTENER_SN" != "$FLOWAI_SOURCE_SYSTEM_ID" ]]; then
  mq::fail "listener cert serialNumber=$LISTENER_SN does not match persisted FLOWAI_SOURCE_SYSTEM_ID=$FLOWAI_SOURCE_SYSTEM_ID"
fi

# --- build the JSON body safely -------------------------------------------

# source_id is the documented idempotency key. We derive it from a
# truncated SHA-256 of the prompt so the same prompt always maps to
# the same source_id within this runtime directory (and across
# separate runs of the same prompt against the same listener /
# source-system). We never accept this value from the caller because
# it must be stable per-task for the dedupe contract.
SOURCE_ID="$(printf '%s' "$PROMPT" | openssl dgst -sha256 -hex | awk '{print $2}' | cut -c1-32)"
mq::trace "submit" "source_id=$SOURCE_ID (derived from prompt sha256)"

BODY_FILE="$(mktemp "$(mq::state_path state)/body.XXXXXX")"
chmod 0600 "$BODY_FILE"

# Build the body via the shared safe JSON encoder. The prompt is
# rendered as {"prompt":"..."}; the listener requires payload to be a
# JSON object. We pre-encode the prompt with python so embedded
# quotes / newlines / backslashes cannot break the wire shape, then
# pass the resulting JSON literal to mq::json_encode as the payload
# value. mq::json_encode recognises it as JSON and emits it verbatim
# (--argjson) so the nested object survives intact.
PROMPT_JSON="$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$PROMPT")"
mq::json_encode \
  "team_id=$FLOWAI_TEAM_ID" \
  "source_system_id=$FLOWAI_SOURCE_SYSTEM_ID" \
  "source_id=$SOURCE_ID" \
  "task_type_id=$FLOWAI_TASK_TYPE_ID" \
  "payload={\"prompt\":$PROMPT_JSON}" \
  >"$BODY_FILE"

# --- POST /v1/tasks -------------------------------------------------------

REG_URL="${FLOWAI_STATE_REGISTRY_URL:-https://localhost:18443}"
RESP="$(mq::curl_listener_post "$REG_URL/v1/tasks" "$BODY_FILE")" \
  || mq::fail "POST /v1/tasks: $RESP"
STATUS="${RESP%%$'\n'*}"
BODY="${RESP#*$'\n'}"

rm -f "$BODY_FILE"

# 201 on the first insert, 200 on the documented (team, source, id)
# idempotent retry. Both responses carry the canonical TaskListEntry.
if [[ "$STATUS" != "201" && "$STATUS" != "200" ]]; then
  mq::fail "POST /v1/tasks returned $STATUS body=$BODY"
fi

# Parse the canonical task_id; tolerate either 200 or 201.
TASK_ID="$(printf '%s' "$BODY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["task_id"])')" \
  || mq::fail "extract task_id from POST /v1/tasks response: $BODY"

# Persist the latest task_id for 04-watch-task.sh and run-all.sh.
printf '%s\n' "$TASK_ID" > "$(mq::state_path state/task.id)"
chmod 0600 "$(mq::state_path state/task.id)"
printf '%s\n' "$STATUS" > "$(mq::state_path state/task.ingest_status)"
chmod 0600 "$(mq::state_path state/task.ingest_status)"

printf 'task_id=%s status=%s\n' "$TASK_ID" "$STATUS"
printf 'ingested at https://%s/v1/tasks/%s\n' "${REG_URL#https://}" "$TASK_ID" >&2