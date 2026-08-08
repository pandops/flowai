#!/usr/bin/env bash
# 02-start-executor.sh — start executor_docker_openhands against the
# State Registry over backend HTTP with rootless Podman.
#
# Required runtime inputs (fail closed if any are missing):
#   * OPENHANDS_LLM_MODEL    + OPENHANDS_LLM_API_KEY + OPENHANDS_LLM_USAGE_ID
#     OR  OPENHANDS_AGENT_PROFILE_ID
#   * EXACTLY ONE of the two groups above must be set.
#
# Optional runtime inputs:
#   * DOCKER_SOCKET_PATH      default: rootless Podman socket
#   * OPENHANDS_HOST_PORT_START / OPENHANDS_HOST_PORT_END
#     (overridden in YAML; env wins per executor precedence)
#   * OPENHANDS_WORKSPACE     default: /workspace/project
#
# Important contract notes:
#   * The Executor's executor_id is pinned to "exec-local-openhands"
#     in the private YAML produced by 01-prepare.sh. The executor_id
#     field has NO env override in the production binary (verified in
#     executor.go:overrideEnv), so the YAML is the only knob.
#   * The current Executor does NOT bind-mount the host checkout into
#     the OpenHands child container; /workspace/project is only valid
#     inside the container's filesystem.
#   * One slot is used (executor_max_containers: 1) so the host port
#     range 18000-18010 is enough for one task at a time.
#   * The Executor's own platform health surface is plaintext HTTP on
#     127.0.0.1:8020; this script probes it over plaintext.

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

# --- tool checks ----------------------------------------------------------

mq::require_cmd curl
mq::require_cmd python3
mq::require_cmd openssl
mq::require_cmd sed
mq::require_cmd podman
mq::require_rootless_podman

# --- prepare state + verify prerequisites ---------------------------------

mq::state_init
mq::state_require_env

# Refuse a duplicate start. The cleanup script is idempotent and the
# README documents the canonical stop-then-restart sequence.
if [[ -f "$(mq::state_path state/executor.pid)" ]]; then
  if mq::pid_is_alive "$(cat "$(mq::state_path state/executor.pid)")"; then
    mq::fail "02-start-executor: an Executor is already running (pid=$(cat "$(mq::state_path state/executor.pid)")); run 99-cleanup.sh first"
  fi
fi

# State Registry must already be running.
REG_PID_FILE="$(mq::state_path state/registry.pid)"
[[ -f "$REG_PID_FILE" ]] || mq::fail "02-start-executor: $REG_PID_FILE missing; run 01-prepare.sh first"
REG_PID="$(cat "$REG_PID_FILE")"
mq::pid_is_alive "$REG_PID" || mq::fail "state-registry pid=$REG_PID is not alive; run 01-prepare.sh again"

# --- LLM configuration check ---------------------------------------------

# The production binary's Config.Validate requires exactly one of:
#   - agent_profile_id (server-side profile)
#   - inline model + api_key + usage_id
# We do NOT persist the API key anywhere on disk.
HAS_INLINE=0
if [[ -n "${OPENHANDS_LLM_MODEL:-}" && -n "${OPENHANDS_LLM_API_KEY:-}" && -n "${OPENHANDS_LLM_USAGE_ID:-}" ]]; then
  HAS_INLINE=1
fi
HAS_PROFILE=0
if [[ -n "${OPENHANDS_AGENT_PROFILE_ID:-}" ]]; then
  HAS_PROFILE=1
fi

if [[ "$HAS_INLINE" -eq 0 && "$HAS_PROFILE" -eq 0 ]]; then
  mq::fail "02-start-executor: set OPENHANDS_LLM_MODEL + OPENHANDS_LLM_API_KEY + OPENHANDS_LLM_USAGE_ID, or set OPENHANDS_AGENT_PROFILE_ID. The LLM API key is NEVER persisted to disk or the runtime state directory."
fi
if [[ "$HAS_INLINE" -eq 1 && "$HAS_PROFILE" -eq 1 ]]; then
  mq::fail "02-start-executor: both inline LLM (OPENHANDS_LLM_*) and OPENHANDS_AGENT_PROFILE_ID are set; pick exactly one"
fi

# --- Podman socket --------------------------------------------------------

if [[ -z "${DOCKER_SOCKET_PATH:-}" ]]; then
  DOCKER_SOCKET_PATH="/run/user/$(id -u)/podman/podman.sock"
fi
mq::trace "executor" "DOCKER_SOCKET_PATH=$DOCKER_SOCKET_PATH"

# --- launch ---------------------------------------------------------------

EXEC_BIN="$(mq::state_path bin/executor_docker_openhands)"
EXEC_YAML="$(mq::state_path state/executor.yaml)"
EXEC_LOG="$(mq::state_path logs/executor.log)"
: >"$EXEC_LOG"
chmod 0600 "$EXEC_LOG"

# Inline-LLM values are forwarded as env vars only (no shell-active
# expansion in the runtime state file). The %q quoting in
# mq::state_write_env protects anything we do persist, but the LLM API
# key is intentionally never written to state/env.sh.
EXEC_ENV=(
  "DOCKER_SOCKET_PATH=$DOCKER_SOCKET_PATH"
  "EXECUTOR_API_BIND=127.0.0.1:8020"
  "EXECUTOR_POLL_INTERVAL=1s"
  "EXECUTOR_STATE_REGISTRY_URL=http://localhost:18443"
  "EXECUTOR_SCOPE=team"
  "EXECUTOR_TEAM_ID=${FLOWAI_TEAM_ID}"
  "EXECUTOR_AUTHORIZED_TAG=openhands"
  "EXECUTOR_MAX_CONTAINERS=1"
  "OPENHANDS_HOST_PORT_START=${OPENHANDS_HOST_PORT_START:-18000}"
  "OPENHANDS_HOST_PORT_END=${OPENHANDS_HOST_PORT_END:-18010}"
  "OPENHANDS_WORKSPACE=${OPENHANDS_WORKSPACE:-/workspace/project}"
  "OPENHANDS_INITIAL_RUN=true"
  "FLOWAI_CLEANUP_ID_DIR=$(mq::state_path state)"
)

# Append the LLM env only when set inline. We keep these in the local
# env-block so the bash array passes them as discrete KEY=VALUE pairs;
# they never appear in any file the scripts persist.
if [[ "$HAS_INLINE" -eq 1 ]]; then
  EXEC_ENV+=(
    "OPENHANDS_LLM_MODEL=$OPENHANDS_LLM_MODEL"
    "OPENHANDS_LLM_API_KEY=$OPENHANDS_LLM_API_KEY"
    "OPENHANDS_LLM_USAGE_ID=$OPENHANDS_LLM_USAGE_ID"
  )
  if [[ -n "${OPENHANDS_LLM_BASE_URL:-}" ]]; then
    EXEC_ENV+=("OPENHANDS_LLM_BASE_URL=$OPENHANDS_LLM_BASE_URL")
  fi
fi
if [[ "$HAS_PROFILE" -eq 1 ]]; then
  EXEC_ENV+=("OPENHANDS_AGENT_PROFILE_ID=$OPENHANDS_AGENT_PROFILE_ID")
fi

mq::trace "executor" "starting executor_docker_openhands (pid will be captured from background)"
nohup env -i \
  PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
  HOME="$HOME" \
  "${EXEC_ENV[@]}" \
  "$EXEC_BIN" -config "$EXEC_YAML" \
  >>"$EXEC_LOG" 2>&1 &
EXEC_PID=$!
printf '%s\n' "$EXEC_PID" > "$(mq::state_path state/executor.pid)"
chmod 0600 "$(mq::state_path state/executor.pid)"

# Wait for the Executor to publish at least one startup log line.
EXEC_DEADLINE=$(( $(date +%s) + 30 ))
EXEC_UP=0
while [[ $(date +%s) -lt $EXEC_DEADLINE ]]; do
  if grep -q "executor_docker_openhands starting" "$EXEC_LOG" 2>/dev/null; then
    EXEC_UP=1
    break
  fi
  if ! mq::pid_is_alive "$EXEC_PID"; then
    mq::fail "executor exited during startup (log: $(tail -c 4096 "$EXEC_LOG"))"
  fi
  sleep 0.3
done
[[ "$EXEC_UP" -eq 1 ]] || mq::fail "executor did not emit a startup log line within 30s (log: $(tail -c 4096 "$EXEC_LOG"))"

# Probe the executor's plaintext platform health surface. The executor
# binds 127.0.0.1:8020 without TLS. /v1/livez returns 200 as soon as
# the HTTP server is up.
mq::wait_http_plain "http://127.0.0.1:8020/v1/livez" 15 \
  || mq::fail "executor /v1/livez not reachable on plaintext http://127.0.0.1:8020 (log: $(tail -c 4096 "$EXEC_LOG"))"

# Confirm the State Registry registered the Executor by polling
# /v1/readyz and parsing the JSON `state_registry_registered` field
# to the literal true. We DO NOT block on OpenHands reachability
# because the V1 agent-server only materialises once a task claims
# a container, and the manual QA flow has not submitted a task yet;
# the readiness endpoint returns HTTP 503 while the executor is
# still negotiating the OpenHands container, which is expected and
# tolerated here as long as state_registry_registered is true.
REG_WAIT_DEADLINE=$(( $(date +%s) + 30 ))
REGISTERED=0
LAST_BODY=""
LAST_HTTP=""
while [[ $(date +%s) -lt $REG_WAIT_DEADLINE ]]; do
  # Use curl WITHOUT --fail-with-body here so 200 + 503 both return
  # bodies we can parse. A non-2xx response without a body is still
  # tolerated (the probe may not be listening for the first few ms
  # after startup).
  set +o errexit
  RESP="$(curl --silent --show-error --max-time 3 \
    -w '\n%{http_code}' \
    "http://127.0.0.1:8020/v1/readyz" 2>&1)"
  CURL_RC=$?
  set -o errexit
  if [[ $CURL_RC -eq 0 ]]; then
    LAST_HTTP="${RESP##*$'\n'}"
    LAST_BODY="${RESP%$'\n'*}"
    if [[ "$LAST_HTTP" == "200" || "$LAST_HTTP" == "503" ]]; then
      if printf '%s' "$LAST_BODY" | python3 -c '
import json, sys
try:
    body = json.load(sys.stdin)
except Exception:
    sys.exit(2)
val = body.get("state_registry_registered", None)
sys.exit(0 if val is True else 1)
' >/dev/null 2>&1; then
        REGISTERED=1
        break
      fi
    fi
  fi
  if ! mq::pid_is_alive "$EXEC_PID"; then
    mq::fail "executor exited while waiting for registration (last body=$LAST_BODY http=$LAST_HTTP log: $(tail -c 4096 "$EXEC_LOG"))"
  fi
  sleep 0.5
done
if [[ "$REGISTERED" -ne 1 ]]; then
  mq::fail "executor did not report state_registry_registered=true within 30s (last http=$LAST_HTTP body=$LAST_BODY log: $(tail -c 4096 "$EXEC_LOG"))"
fi

mq::trace "executor" "executor pid=$EXEC_PID is up on http://127.0.0.1:8020 (state_registry_registered=true)"
mq::trace "executor" "log: $EXEC_LOG"
mq::trace "executor" "next step: 03-submit-task.sh 'your prompt here'"
