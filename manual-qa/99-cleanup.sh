#!/usr/bin/env bash
# 99-cleanup.sh — stop the Executor + State Registry safely, remove
# Executor-owned OpenHands containers and the Postgres container, and
# delete the protected runtime artifacts.
#
# Idempotency: every step tolerates a missing pid / log / container.
# The script can be run repeatedly; the second invocation is a no-op.
#
# PID-reuse safety: every terminate_pid call is gated on the process's
# /proc/PID/cmdline matching the expected binary path, so a stale PID
# file from a previous run that has since been reassigned to a
# different process is NEVER killed.
#
# Usage:
#   ./99-cleanup.sh [--keep-runtime-state]
#
# Flags:
#   --keep-runtime-state   leave $FLOWAI_MANUAL_QA_RUN_DIR on disk
#                          so the operator can inspect certs, logs, and
#                          binding files after the run. Pids + pg
#                          artifacts are removed. A new preparation
#                          requires a fresh runtime directory (or a
#                          successful removal via plain ./99-cleanup.sh);
#                          --keep-runtime-state does NOT make the
#                          next preparation a no-op reuse path.

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

KEEP_RUNTIME=0
for arg in "$@"; do
  case "$arg" in
    --keep-runtime-state) KEEP_RUNTIME=1 ;;
    *) mq::fail "99-cleanup: unknown argument '$arg'" ;;
  esac
done

mq::state_init

RT=""
if command -v podman >/dev/null 2>&1; then
  RT="podman"
fi
[[ -n "$RT" ]] || mq::trace "cleanup" "no container runtime on PATH; skipping container cleanup"

# --- terminate Executor ---------------------------------------------------

EXEC_PID_FILE="$(mq::state_path state/executor.pid)"
if [[ -f "$EXEC_PID_FILE" ]]; then
  EXEC_PID="$(cat "$EXEC_PID_FILE")"
  if mq::pid_is_alive "$EXEC_PID"; then
    if mq::pid_identity_check "$EXEC_PID" "executor_docker_opehands"; then
      mq::trace "cleanup" "sending SIGTERM to executor pid=$EXEC_PID"
      mq::terminate_pid "$EXEC_PID" 15 "executor_docker_opehands" || mq::trace "cleanup" "executor pid=$EXEC_PID did not exit cleanly"
    else
      mq::trace "cleanup" "executor pid=$EXEC_PID is alive but cmdline does not match executor_docker_opehands; refusing to kill (PID reuse)"
    fi
  fi
  rm -f "$EXEC_PID_FILE"
fi

# --- terminate State Registry --------------------------------------------

REG_PID_FILE="$(mq::state_path state/registry.pid)"
if [[ -f "$REG_PID_FILE" ]]; then
  REG_PID="$(cat "$REG_PID_FILE")"
  if mq::pid_is_alive "$REG_PID"; then
    if mq::pid_identity_check "$REG_PID" "state-registry"; then
      mq::trace "cleanup" "sending SIGTERM to state-registry pid=$REG_PID"
      mq::terminate_pid "$REG_PID" 10 "state-registry" || mq::trace "cleanup" "state-registry pid=$REG_PID did not exit cleanly"
    else
      mq::trace "cleanup" "state-registry pid=$REG_PID is alive but cmdline does not match state-registry; refusing to kill (PID reuse)"
    fi
  fi
  rm -f "$REG_PID_FILE"
fi

# --- remove Executor-owned OpenHands containers --------------------------

if [[ -n "$RT" ]]; then
  CIDS="$("$RT" ps -aq --filter "label=flowai.executor_id=exec-local-openhands" 2>/dev/null || true)"
  if [[ -n "$CIDS" ]]; then
    mq::trace "cleanup" "removing Executor-owned OpenHands containers: $CIDS"
    while read -r cid; do
      [[ -n "$cid" ]] || continue
      mq::runtime_remove "$RT" "$cid"
    done <<<"$CIDS"
  fi

  PG_CID_FILE="$(mq::state_path state/pg.cid)"
  if [[ -f "$PG_CID_FILE" ]]; then
    PG_CID="$(cat "$PG_CID_FILE")"
    if [[ -n "$PG_CID" ]]; then
      mq::trace "cleanup" "removing Postgres container $PG_CID"
      mq::runtime_remove "$RT" "$PG_CID"
    fi
    rm -f "$PG_CID_FILE"
  fi
fi

# --- tear down the runtime state directory -------------------------------

if [[ "$KEEP_RUNTIME" -eq 0 ]]; then
  mq::trace "cleanup" "removing runtime directory $FLOWAI_MANUAL_QA_RUN_DIR"
  # We use rm -rf -- because FLOWAI_MANUAL_QA_RUN_DIR is owned by the
  # caller and contains no world-writable paths; if a foreign user has
  # slipped a file in via a writable subdirectory, the worst case is
  # that the rm refuses on the foreign file.
  rm -rf -- "${FLOWAI_MANUAL_QA_RUN_DIR:?}"
  mq::trace "cleanup" "runtime directory removed"
else
  # --keep-runtime-state keeps the directory on disk so the operator can
  # inspect certs, logs, and binding files after the run. We drop only
  # the per-run process + container identifiers (every pid + pg artifact)
  # so a subsequent ./99-cleanup.sh without --keep-runtime-state
  # cleanly removes the rest, and a subsequent ./01-prepare.sh in the
  # SAME directory would still rebuild all binaries + reissue certs.
  mq::trace "cleanup" "--keep-runtime-state set; keeping $FLOWAI_MANUAL_QA_RUN_DIR on disk"
  rm -f "$(mq::state_path state)"/*.pid \
    "$(mq::state_path state)/task.id" \
    "$(mq::state_path state)/task.ingest_status" \
    "$(mq::state_path state)/pg.cid" \
    "$(mq::state_path state)/pg.host_port" \
    "$(mq::state_path state)/pg.name" \
    "$(mq::state_path state)/binding.txt"
  rm -rf -- "$(mq::state_path state/pg-tls)"
fi

mq::trace "cleanup" "done"