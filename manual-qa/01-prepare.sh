#!/usr/bin/env bash
# 01-prepare.sh — prepare the manual QA runtime.
#
# Responsibilities:
#   * verify the rootless Podman socket is present and usable
#   * require every external command used by the manual-qa flow
#   * build the State Registry and Executor binaries (untagged production)
#   * resolve ghcr.io/openhands/agent-server:latest-python to an immutable
#     repository@sha256:... digest; this becomes the team's default_image
#     so a successful claim returns a real resolved_image the Executor
#     can pull verbatim
#   * issue an ephemeral CA + State Registry / Postgres serverAuth certs
#     + admin clientAuth cert
#   * start a TLS-enabled postgres:16 container bound to 127.0.0.1
#     (SELinux-safe :Z bind mounts; entrypoint invoked via /bin/bash)
#   * start a normal untagged State Registry with verify-full DB TLS
#     (against the CA bundle, NOT the Postgres leaf cert) and mTLS
#     listener (production mode; NO STATE_REGISTRY_TEST_MODE)
#   * verify /v1/livez AND /v1/readyz on the State Registry
#   * create team / source-system / task-type via admin mTLS API
#   * issue dynamically bound listener / team Executor / gateway certs
#     with the actual team_id and source_system_id values
#   * write a stable Executor YAML with executor_id: exec-local-openhands
#
# Idempotency:
#   * running on a fresh runtime directory is the normal case
#   * running on a populated directory that still has live processes
#     fails fast; the operator must run 99-cleanup.sh first
#
# Environment inputs (all optional; documented in README.md):
#   FLOWAI_MANUAL_QA_RUN_DIR    runtime state root (default /tmp/flowai-manual-qa)
#   FLOWAI_REPO_ROOT            repository root (default: auto-detect)

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
: "${FLOWAI_MANUAL_QA_RUN_DIR:=}"
: "${FLOWAI_REPO_ROOT:=}"
: "${FLOWAI_OPENHANDS_REPOSITORY:=}"
: "${FLOWAI_OPENHANDS_DIGEST:=}"
: "${FLOWAI_PG_PASSWORD:=}"
: "${FLOWAI_PG_DSN:=}"
: "${FLOWAI_STATE_REGISTRY_AES_KEY_HEX:=}"
: "${FLOWAI_STATE_REGISTRY_CURSOR_KEY_ID:=}"
: "${FLOWAI_STATE_REGISTRY_CURSOR_KEY_HEX:=}"
: "${FLOWAI_STATE_REGISTRY_SCOPE_TOKEN_KEY_ID:=}"
: "${FLOWAI_STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX:=}"
: "${FLOWAI_STATE_REGISTRY_URL:=}"
: "${FLOWAI_TEAM_ID:=}"
: "${FLOWAI_SOURCE_SYSTEM_ID:=}"
: "${FLOWAI_TASK_TYPE_ID:=}"
: "${FLOWAI_EXECUTION_TAG:=}"

# --- required tools (every external command used anywhere in the flow) ----

mq::require_cmd go
mq::require_cmd openssl
mq::require_cmd curl
mq::require_cmd realpath
mq::require_cmd timeout
mq::require_cmd podman
mq::require_cmd sed
mq::require_cmd python3
mq::require_rootless_podman

# --- state + repo resolution ---------------------------------------------

mq::state_init

if [[ -z "${FLOWAI_REPO_ROOT:-}" ]]; then
  FLOWAI_REPO_ROOT="$(cd "$MQ_SCRIPT_DIR/.." && pwd)"
fi
export FLOWAI_REPO_ROOT

mq::trace "prepare" "FLOWAI_MANUAL_QA_RUN_DIR=$FLOWAI_MANUAL_QA_RUN_DIR"
mq::trace "prepare" "FLOWAI_REPO_ROOT=$FLOWAI_REPO_ROOT"

if [[ -f "$(mq::state_path state/registry.pid)" ]]; then
  mq::fail "01-prepare: $(mq::state_path state/registry.pid) already exists; run 99-cleanup.sh first"
fi

RT="$(mq::detect_runtime)"
mq::trace "prepare" "container runtime: $RT"

# --- build binaries (untagged production) --------------------------------

BIN_DIR="$(mq::state_path bin)"
mq::trace "prepare" "building state-registry (production, untagged)"
( cd "$FLOWAI_REPO_ROOT" && go build -o "$BIN_DIR/state-registry" ./state-registry/cmd/state-registry )
chmod 0755 "$BIN_DIR/state-registry"

mq::trace "prepare" "building executor_docker_opehands (production, untagged)"
( cd "$FLOWAI_REPO_ROOT" && go build -o "$BIN_DIR/executor_docker_opehands" ./executor_docker_opehands/cmd/executor_docker_opehands )
chmod 0755 "$BIN_DIR/executor_docker_opehands"

# --- resolve OpenHands image to an immutable digest ---------------------

OH_IMAGE="ghcr.io/openhands/agent-server:latest-python"
mq::trace "prepare" "pulling $OH_IMAGE"
"$RT" pull --quiet "$OH_IMAGE" >/dev/null 2>&1 || mq::fail "podman pull $OH_IMAGE"

# RepoDigests[0] returns "repo@sha256:..." after the first pull; the
# image-inspect .Digest fallback returns only the digest when RepoDigests
# is empty (older podman versions or freshly built images).
DIGEST_LINE="$("$RT" inspect --format '{{index .RepoDigests 0}}' "$OH_IMAGE" 2>/dev/null || true)"
if [[ -z "$DIGEST_LINE" ]]; then
  DIGEST_LINE="$("$RT" image inspect --format '{{.Digest}}' "$OH_IMAGE" 2>/dev/null || true)"
fi
[[ -n "$DIGEST_LINE" ]] || mq::fail "could not resolve an immutable digest for $OH_IMAGE"

# Strip any :tag from the source image ref to produce the bare repo
# when only the digest was returned. The sed pipeline drops a trailing
# @digest first, then any trailing :tag where the tag does not contain
# '/' (so port numbers in registry hostnames are preserved).
normalize_repo_no_tag() {
  printf '%s' "$1" | sed -e 's/@.*$//' -e 's/:[^/:]*$//'
}

case "$DIGEST_LINE" in
  *@sha256:*)
    OH_REPO="${DIGEST_LINE%@*}"
    OH_DIGEST="${DIGEST_LINE##*@}"
    ;;
  sha256:*)
    OH_REPO="$(normalize_repo_no_tag "$OH_IMAGE")"
    OH_DIGEST="$DIGEST_LINE"
    ;;
  *)
    mq::fail "unexpected digest format from $RT inspect: $DIGEST_LINE"
    ;;
esac

# Validate the derived values; refuse to proceed if either is empty
# (a partial digest would corrupt the four-level image precedence).
[[ -n "$OH_REPO" ]]  || mq::fail "could not derive OpenHands repository from $OH_IMAGE"
[[ -n "$OH_DIGEST" ]] || mq::fail "could not derive OpenHands digest from $DIGEST_LINE"

mq::trace "prepare" "OpenHands image resolved to $OH_REPO@$OH_DIGEST"
mq::state_write_env "FLOWAI_OPENHANDS_REPOSITORY" "$OH_REPO"
mq::state_write_env "FLOWAI_OPENHANDS_DIGEST" "$OH_DIGEST"

# --- certificate issuance ------------------------------------------------

CA_DIR="$(mq::state_path state/certs)"
mq::ca_init "$CA_DIR"

# serverAuth certs for the State Registry and Postgres.
mq::issue_server_cert "$CA_DIR" "$CA_DIR" "registry"
mq::issue_server_cert "$CA_DIR" "$CA_DIR" "pg-server"

# Admin client cert. NO team; NO serialNumber — peerauth rejects extras.
# Listener, team-executor, and gateway certs are issued AFTER admin
# onboarding so the O=/serialNumber= fields carry the real identifiers.
mq::issue_client_cert "$CA_DIR" "$CA_DIR" "admin" \
  "CN=system-admin" "OU=admin"

# --- Postgres (TLS) ------------------------------------------------------

PG_PASSWORD="$(mq::random_password)"
mq::state_write_env "FLOWAI_PG_PASSWORD" "$PG_PASSWORD"

PG_NAME="flowai-manual-qa-pg-$$"
PG_INFO="$(mq::runtime_run_postgres "$RT" "$PG_NAME" "$PG_PASSWORD" "flowai")" \
  || mq::fail "start postgres"
PG_CID="${PG_INFO%%$'\n'*}"
PG_HOST_PORT="${PG_INFO##*$'\n'}"
mq::trace "prepare" "postgres container=$PG_CID host_port=$PG_HOST_PORT"

# Wait for postgres to accept SQL connections before starting the Registry.
PG_READY=0
PG_DEADLINE=$(( $(date +%s) + 30 ))
while [[ $(date +%s) -lt $PG_DEADLINE ]]; do
  set +o errexit
  "$RT" exec "$PG_CID" pg_isready --username postgres --dbname flowai >/dev/null 2>&1
  PG_RC=$?
  set -o errexit
  if [[ $PG_RC -eq 0 ]]; then
    PG_READY=1
    break
  fi
  sleep 0.5
done
[[ "$PG_READY" -eq 1 ]] || mq::fail "postgres: pg_isready did not return 0 within 30s (logs: $("$RT" logs --tail 40 "$PG_CID" 2>/dev/null))"
mq::trace "prepare" "postgres is SQL-ready on host 127.0.0.1:$PG_HOST_PORT"

# DSN with sslmode=verify-full so the State Registry's Go client
# verifies the server chain against STATE_REGISTRY_POSTGRES_TLS_CA (the
# CA bundle, NOT the Postgres leaf cert).
PG_DSN="postgres://postgres:${PG_PASSWORD}@127.0.0.1:${PG_HOST_PORT}/flowai?sslmode=verify-full"
mq::state_write_env "FLOWAI_PG_DSN" "$PG_DSN"

# --- State Registry keys + state registry ---------------------------------

AES_KEY="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
CURSOR_KEY_ID="cursor-$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
CURSOR_KEY="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
SCOPE_KEY_ID="scope-$(head -c 4 /dev/urandom | od -An -tx1 | tr -d ' \n')"
SCOPE_KEY="$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
mq::state_write_env "FLOWAI_STATE_REGISTRY_AES_KEY_HEX" "$AES_KEY"
mq::state_write_env "FLOWAI_STATE_REGISTRY_CURSOR_KEY_ID" "$CURSOR_KEY_ID"
mq::state_write_env "FLOWAI_STATE_REGISTRY_CURSOR_KEY_HEX" "$CURSOR_KEY"
mq::state_write_env "FLOWAI_STATE_REGISTRY_SCOPE_TOKEN_KEY_ID" "$SCOPE_KEY_ID"
mq::state_write_env "FLOWAI_STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX" "$SCOPE_KEY"

# --- launch State Registry (production mTLS, untagged) ------------------

REG_BIN="$BIN_DIR/state-registry"
REG_LOG="$(mq::state_path logs/state-registry.log)"
: >"$REG_LOG"
chmod 0600 "$REG_LOG"

REG_ENV=(
  "STATE_REGISTRY_BIND_HOST=127.0.0.1"
  "STATE_REGISTRY_BIND_PORT=18443"
  "STATE_REGISTRY_POSTGRES_URL=$PG_DSN"
  "STATE_REGISTRY_AES_KEY_HEX=$AES_KEY"
  "STATE_REGISTRY_CURSOR_KEY_ID=$CURSOR_KEY_ID"
  "STATE_REGISTRY_CURSOR_KEY_HEX=$CURSOR_KEY"
  "STATE_REGISTRY_SCOPE_TOKEN_KEY_ID=$SCOPE_KEY_ID"
  "STATE_REGISTRY_SCOPE_TOKEN_KEY_HEX=$SCOPE_KEY"
  "STATE_REGISTRY_TLS_SERVER_CERT=$CA_DIR/registry.crt"
  "STATE_REGISTRY_TLS_SERVER_KEY=$CA_DIR/registry.key"
  "STATE_REGISTRY_TLS_CLIENT_CA=$CA_DIR/ca.crt"
  "STATE_REGISTRY_TLS_REQUIRE_CLIENT_CERT=true"
  # pgx's sslrootcert expects the CA that signed the server cert.
  # Use the CA bundle, NOT the Postgres leaf cert.
  "STATE_REGISTRY_POSTGRES_TLS_CA=$CA_DIR/ca.crt"
  "STATE_REGISTRY_POSTGRES_TLS_MODE=verify-full"
)

# Build the env-prefixed command line. We do not export these globally
# because that would leak the AES key into unrelated subprocesses.
mq::trace "prepare" "starting state-registry on 127.0.0.1:18443 (mTLS, verify-full)"
nohup env -i \
  PATH="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin" \
  HOME="$HOME" \
  "${REG_ENV[@]}" \
  "$REG_BIN" \
  >>"$REG_LOG" 2>&1 &
REG_PID=$!
printf '%s\n' "$REG_PID" > "$(mq::state_path state/registry.pid)"
chmod 0600 "$(mq::state_path state/registry.pid)"

# Wait for "state-registry listening bind=127.0.0.1:18443" to appear.
REG_DEADLINE=$(( $(date +%s) + 30 ))
REG_UP=0
while [[ $(date +%s) -lt $REG_DEADLINE ]]; do
  if grep -q '"state-registry listening".*"bind":"127.0.0.1:18443"' "$REG_LOG" 2>/dev/null; then
    REG_UP=1
    break
  fi
  if ! mq::pid_is_alive "$REG_PID"; then
    mq::fail "state-registry exited before becoming ready (log: $(tail -c 4096 "$REG_LOG"))"
  fi
  sleep 0.3
done
[[ "$REG_UP" -eq 1 ]] || mq::fail "state-registry never logged bind within 30s (log: $(tail -c 4096 "$REG_LOG"))"
mq::trace "prepare" "state-registry pid=$REG_PID is listening on https://127.0.0.1:18443"

# Verify it is mTLS-reachable by hitting /v1/livez AND /v1/readyz with
# the admin cert. readyz=200 confirms the Postgres connection + AES key
# are wired up before we onboard anything.
mq::wait_http_https "https://localhost:18443/v1/livez" \
  "$CA_DIR/ca.crt" "$CA_DIR/admin.crt" "$CA_DIR/admin.key" 15 \
  || mq::fail "state-registry /v1/livez not reachable over mTLS"
mq::wait_http_https "https://localhost:18443/v1/readyz" \
  "$CA_DIR/ca.crt" "$CA_DIR/admin.crt" "$CA_DIR/admin.key" 15 \
  || mq::fail "state-registry /v1/readyz did not return 200; the Registry is not fully ready"

mq::state_write_env "FLOWAI_STATE_REGISTRY_URL" "https://localhost:18443"

# --- admin onboarding (team, source-system, task-type) -------------------

TMP_BODY="$(mktemp "$(mq::state_path state)/body.XXXXXX")"
chmod 0600 "$TMP_BODY"

# 1. team — REQUIRED default_image is the resolved OpenHands digest.
mq::json_encode \
  "team_name=qa-team-local" \
  "default_image={\"repository\":\"$OH_REPO\",\"digest\":\"$OH_DIGEST\"}" \
  >"$TMP_BODY"

REG_URL="https://localhost:18443"
RESP="$(mq::curl_admin_post "$REG_URL/admin/teams" "$TMP_BODY")" \
  || mq::fail "POST /admin/teams: $RESP"
STATUS="${RESP%%$'\n'*}"
BODY="${RESP#*$'\n'}"
[[ "$STATUS" == "201" ]] || mq::fail "POST /admin/teams returned $STATUS body=$BODY"
TEAM_ID="$(printf '%s' "$BODY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["team_id"])')" \
  || mq::fail "extract team_id from POST /admin/teams response: $BODY"
mq::trace "prepare" "team_id=$TEAM_ID"

# 2. source-system — bound to the team above.
mq::json_encode \
  "team_id=$TEAM_ID" \
  "listener_identity=listener-local" \
  >"$TMP_BODY"
RESP="$(mq::curl_admin_post "$REG_URL/admin/source-systems" "$TMP_BODY")" \
  || mq::fail "POST /admin/source-systems: $RESP"
STATUS="${RESP%%$'\n'*}"
BODY="${RESP#*$'\n'}"
[[ "$STATUS" == "201" ]] || mq::fail "POST /admin/source-systems returned $STATUS body=$BODY"
SOURCE_SYSTEM_ID="$(printf '%s' "$BODY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["source_system_id"])')" \
  || mq::fail "extract source_system_id: $BODY"
mq::trace "prepare" "source_system_id=$SOURCE_SYSTEM_ID"

# 3. task-type — execution_tag = openhands.
mq::json_encode \
  "team_id=$TEAM_ID" \
  "execution_tag=openhands" \
  >"$TMP_BODY"
RESP="$(mq::curl_admin_post "$REG_URL/admin/task-types" "$TMP_BODY")" \
  || mq::fail "POST /admin/task-types: $RESP"
STATUS="${RESP%%$'\n'*}"
BODY="${RESP#*$'\n'}"
[[ "$STATUS" == "201" ]] || mq::fail "POST /admin/task-types returned $STATUS body=$BODY"
TASK_TYPE_ID="$(printf '%s' "$BODY" | python3 -c 'import json,sys; print(json.load(sys.stdin)["task_type_id"])')" \
  || mq::fail "extract task_type_id: $BODY"
mq::trace "prepare" "task_type_id=$TASK_TYPE_ID"

rm -f "$TMP_BODY"

# Persist the canonical identifiers for downstream scripts.
mq::state_write_env "FLOWAI_TEAM_ID" "$TEAM_ID"
mq::state_write_env "FLOWAI_SOURCE_SYSTEM_ID" "$SOURCE_SYSTEM_ID"
mq::state_write_env "FLOWAI_TASK_TYPE_ID" "$TASK_TYPE_ID"
mq::state_write_env "FLOWAI_EXECUTION_TAG" "openhands"

# --- re-issue certs with the real team / source-system binding -----------

# Defensive cleanup: a previous run might have left bootstrap files
# around. The current code never issues them but a stale file under
# that name could only ever impersonate the team.
rm -f "$CA_DIR/listener-bootstrap."* "$CA_DIR/team-executor-bootstrap."* "$CA_DIR/gateway-bootstrap."* 2>/dev/null || true

mq::issue_client_cert "$CA_DIR" "$CA_DIR" "listener" \
  "CN=listener-local" "OU=listener" \
  "O=$TEAM_ID" "serialNumber=$SOURCE_SYSTEM_ID"

# The team Executor's CN is the documented executor_id. The Operator
# wrote that value to the YAML in 02-start-executor.sh; the cert CN
# here must match so the peerauth parser accepts the registration.
mq::issue_client_cert "$CA_DIR" "$CA_DIR" "team-executor" \
  "CN=exec-local-openhands" "OU=team-executor" \
  "O=$TEAM_ID"

mq::issue_client_cert "$CA_DIR" "$CA_DIR" "gateway" \
  "CN=operator-local" "OU=gateway" \
  "O=$TEAM_ID"

# --- write the stable Executor YAML (private copy) ----------------------
#
# Minimal valid YAML: only the executor_id is pinned here (it has no
# environment override in the production binary, so the YAML is the
# only knob). Every other field defaults from the executor's own
# defaults or is overridden explicitly in 02-start-executor.sh.

EXEC_YAML="$(mq::state_path state/executor.yaml)"
install -m 0600 /dev/null "$EXEC_YAML"
printf 'executor_id: exec-local-openhands\n' >"$EXEC_YAML"
chmod 0600 "$EXEC_YAML"

# Snapshot the binding summary for grep-ability.
{
  printf 'team_id=%s\n' "$TEAM_ID"
  printf 'source_system_id=%s\n' "$SOURCE_SYSTEM_ID"
  printf 'task_type_id=%s\n' "$TASK_TYPE_ID"
  printf 'openhands_image=%s@%s\n' "$OH_REPO" "$OH_DIGEST"
} > "$(mq::state_path state/binding.txt)"
chmod 0600 "$(mq::state_path state/binding.txt)"

mq::trace "prepare" "preparation complete"
mq::trace "prepare" "next step: 02-start-executor.sh (requires OPENHANDS_LLM_* or OPENHANDS_AGENT_PROFILE_ID)"