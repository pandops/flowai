# Manual QA — production-mTLS end-to-end runbook

This directory contains a self-contained, runnable manual QA flow that
exercises the production `state-registry/` + `executor_docker_opehands/`
pair end to end: PostgreSQL 16 with TLS, mutual-TLS State Registry in
normal untagged production mode, the OpenHands V1 agent-server pulled
to an immutable digest, and a single in-line task that walks the full
`pending -> created -> running -> finished | failed` lifecycle.

The flow is intentionally **one command per phase** so a human operator
can pause, inspect logs, and resume between steps. `run-all.sh` is the
only wrapper and it is deliberately thin; it never hides the required
LLM input.

> **The removed mocked task server is not part of this flow.** The current
> executor expects the real State Registry; the mocked task server is
> the v0001 wire surface and is not wired to the v0002 client.
> Likewise, do not set `STATE_REGISTRY_TEST_MODE` and do not pass
> `curl -k`. The State Registry in production mode rejects both.

Инструкция по запуску ручного тестирования mTLS end-to-end
(State Registry ↔ Executor ↔ OpenHands ↔ PostgreSQL).

---

## 1. Prerequisites

- **Linux host with rootless Podman.** A working rootless Podman socket
  at `$XDG_RUNTIME_DIR/podman/podman.sock` (this manual QA flow does
  **not** support Docker; the Postgres launch uses SELinux-safe `:Z`
  bind mounts that are podman-specific). Verify with `podman info`.
- **Go toolchain.** `go` (1.22+) must be on `PATH`; `01-prepare.sh`
  builds both production binaries from this repository.
- **OpenSSL CLI.** `openssl` (3.x) for ephemeral CA + serverAuth +
  clientAuth certificate issuance.
- **GNU coreutils with `timeout`.** `mq::openssl_run` requires the
  GNU `timeout` command to bound every OpenSSL subprocess; most Linux
  distributions ship this by default.
- **`curl`** with TLS + mTLS support. Stock curl with libssl is fine.
- **`python3`** is the canonical JSON encoder (and validation
  fallback). `mq::json_encode` also accepts `jq` when present and
  delegates nested-object encoding to it via `--argjson`, but `jq`
  is **not** a hard prerequisite.
- **LLM credentials.** Either set
  `OPENHANDS_LLM_MODEL` + `OPENHANDS_LLM_API_KEY` + `OPENHANDS_LLM_USAGE_ID`,
  or set `OPENHANDS_AGENT_PROFILE_ID` — exactly one. The API key is
  never persisted to disk by these scripts; it lives only in the
  Executor process environment for the lifetime of the run.
- **Outbound network.** `01-prepare.sh` pulls
  `ghcr.io/openhands/agent-server:latest-python` and resolves it to an
  immutable digest; the Executor then pulls that same digest at claim
  time. No outbound is required for the State Registry or Postgres.

## 2. Architecture flow

```
        ┌──────────┐  mTLS (verified cert subject) ┌──────────┐
admin ──┤  POST    ├────────────────────────────────▶│  State   │
        │  /admin  │                                  │ Registry │  verify-full TLS
        └──────────┘                                  │ (prod)   ├──────────┐
                                                       └─────┬────┘          │
                                                         TLS │               │
                                                              ▼               ▼
                                                       ┌──────────┐    ┌─────────┐
                                                       │ postgres │    │  Exec   │
                                                       │  16 (TLS)│    │  (1 slot│
                                                       └──────────┘    │   )     │
        ┌──────────┐  mTLS                              ▲             │  rootless│
listener ┤  POST    ├──────────────────────────────────┘             │  podman  │
        │  /v1/tasks│  (team_id, source_system_id,                   │  + openhands
        └──────────┘   source_id, task_type_id)                       │  image@sha256
                                                                       │   )
                                                                       ▼
        ┌──────────┐  mTLS (verified cert subject)
gateway ┤  GET     ├────────────────────────────────▶   State Registry
        │  /v1/... │                                        /v1/tasks/{id}
        └──────────┘                                        /v1/tasks/{id}/events

                                                                Container with
                                                                flowai.executor_id
                                                                + flowai.task_id
                                                                  labels only
```

- **Listener identity** is the verified peer certificate subject
  `CN=listener-local,OU=listener,O=<team_id>,serialNumber=<source_system_id>`.
- **Team Executor identity** is `CN=exec-local-openhands,OU=team-executor,O=<team_id>`.
- **Gateway identity** is `CN=operator-local,OU=gateway,O=<team_id>`.
- **System administrator identity** is `CN=system-admin,OU=admin`.
- The State Registry and PostgreSQL server certs carry SAN
  `DNS:localhost,IP:127.0.0.1` and EKU `serverAuth`. Client certs
  carry EKU `clientAuth`.
- The Executor's own platform health surface (`/v1/livez`,
  `/v1/readyz`) is **plaintext HTTP** on `127.0.0.1:8020`; mTLS is
  reserved for the State Registry.

## 3. Usage order

The manual QA scripts read their inputs from a local dotenv file at
`manual-qa/.env` so no manual `export` is required. The committed
template is `manual-qa/.env.example` (safe to read, no real secrets).
`manual-qa/.env` is matched by the root `.gitignore` and is never
committed; the scripts refuse to read it unless it is owned by you
and is mode 0600.

To get started once:

```bash
cd <repo-root>/manual-qa

# Copy the safe template; the scripts refuse anything that is not
# mode 0600 or that is owned by another user.
cp manual-qa/.env.example manual-qa/.env
chmod 0600 manual-qa/.env
# Edit manual-qa/.env and set OPENHANDS_LLM_API_KEY to your real key.
# (Leave OPENHANDS_LLM_BASE_URL and OPENHANDS_AGENT_PROFILE_ID blank
# unless you are using an alternative LLM endpoint or a server-side
# agent profile respectively. Exactly one LLM mode must be active.)
```

Precedence: **process environment > `.env` > script defaults.** A
shell export (`export OPENHANDS_LLM_MODEL=…` in the parent shell,
or an inline `KEY=value ./02-start-executor.sh`) always wins over the
same key in `manual-qa/.env`, including explicitly empty exported
values. Inside the same script, placeholder defaults only fill in
keys that are still unset after `.env` has been read.

Then run the phases:

```bash
cd <repo-root>/manual-qa

# 1) Build binaries, issue certs, start Postgres + State Registry,
#    register team / source-system / task-type.
./01-prepare.sh

# 2) Start the executor against the State Registry over mTLS.
#    The LLM input is read from manual-qa/.env (or the shell
#    environment, which wins). It is NEVER persisted to disk or to
#    the runtime state directory.
./02-start-executor.sh

# 3) Submit a task. The prompt can come from argv[1] or from the
#    PROMPT key in manual-qa/.env.
./03-submit-task.sh "List the contents of /etc/hostname and report what you find."

# 4) Watch the task to terminal state (default 600 s timeout).
./04-watch-task.sh
# or with a custom timeout:
./04-watch-task.sh "" 300

# 5) Tear everything down (idempotent).
./99-cleanup.sh
```

`run-all.sh` chains 01..04 and traps `EXIT/INT/TERM` so a failure still
runs `99-cleanup.sh`. It does not hide the LLM input — the dotenv
loader picks up `OPENHANDS_LLM_*` / `OPENHANDS_AGENT_PROFILE_ID` from
`manual-qa/.env` (or from the shell environment, which wins) before
the executor phase is reached.

## 4. Environment inputs

| Variable                     | Required for           | Default                                 | Notes                                                                                      |
| ---------------------------- | ---------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------ |
| `FLOWAI_MANUAL_QA_RUN_DIR`   | all scripts            | `/tmp/flowai-manual-qa`                 | Runtime state root. Mode 0700. State files mode 0600. Refuses symlinks and `..` traversal. |
| `FLOWAI_REPO_ROOT`           | `01-prepare.sh`        | `<this-dir>/..`                         | Repository root used for `go build`.                                                       |
| `OPENHANDS_LLM_MODEL`        | `02-start-executor.sh` | —                                       | Required _or_ `OPENHANDS_AGENT_PROFILE_ID`.                                                |
| `OPENHANDS_LLM_API_KEY`      | `02-start-executor.sh` | —                                       | Required _or_ `OPENHANDS_AGENT_PROFILE_ID`. Never persisted.                               |
| `OPENHANDS_LLM_USAGE_ID`     | `02-start-executor.sh` | —                                       | Required _or_ `OPENHANDS_AGENT_PROFILE_ID`.                                                |
| `OPENHANDS_LLM_BASE_URL`     | `02-start-executor.sh` | unset                                   | Optional. Forwarded to OpenHands V1.                                                       |
| `OPENHANDS_AGENT_PROFILE_ID` | `02-start-executor.sh` | —                                       | Required _or_ all three `OPENHANDS_LLM_*`. Exactly one of the two groups must be set.      |
| `DOCKER_SOCKET_PATH`         | `02-start-executor.sh` | `/run/user/$(id -u)/podman/podman.sock` | Override only when running against a non-default socket.                                   |
| `OPENHANDS_HOST_PORT_START`  | `02-start-executor.sh` | `18000`                                 | YAML default; env wins.                                                                    |
| `OPENHANDS_HOST_PORT_END`    | `02-start-executor.sh` | `18010`                                 | YAML default; env wins.                                                                    |
| `OPENHANDS_WORKSPACE`        | `02-start-executor.sh` | `/workspace/project`                    | In-container path; the Executor does NOT bind-mount the host checkout.                     |
| `RUN_ALL_NO_CLEANUP`         | `run-all.sh`           | unset                                   | Set to `1` to skip the trap-time cleanup.                                                  |

## 5. What reaches the Executor (env propagation)

`02-start-executor.sh` reads the allow-listed values from
`manual-qa/.env` (and from the shell environment, which wins), then
launches `executor_docker_opehands` via `nohup env -i … "$EXEC_BIN"
-config "$EXEC_YAML"`. The `env -i` invocation clears the inherited
environment and rebuilds it from a single `EXEC_ENV` Bash array, so
the Executor subprocess sees ONLY the keys the script explicitly
passes — nothing leaks from the operator's interactive shell.

Keys that DO reach the Executor subprocess:

| Source             | Key                                                                                                                                                                  | How it is assembled                                                                                           |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `.env` / shell env | `OPENHANDS_LLM_MODEL`                                                                                                                                                | inline-LLM mode only; never written to a state file                                                           |
| `.env` / shell env | `OPENHANDS_LLM_API_KEY`                                                                                                                                              | inline-LLM mode only; never written to a state file                                                           |
| `.env` / shell env | `OPENHANDS_LLM_USAGE_ID`                                                                                                                                             | inline-LLM mode only                                                                                          |
| `.env` / shell env | `OPENHANDS_LLM_BASE_URL`                                                                                                                                             | inline-LLM mode only; appended only when set                                                                  |
| `.env` / shell env | `OPENHANDS_AGENT_PROFILE_ID`                                                                                                                                         | profile mode only; appended only when set                                                                     |
| runtime state      | `DOCKER_SOCKET_PATH`                                                                                                                                                 | `.env` / shell env, with the documented Podman default fallback                                               |
| runtime state      | `OPENHANDS_HOST_PORT_START`, `OPENHANDS_HOST_PORT_END`                                                                                                               | `.env` / shell env, with the YAML defaults as fallback                                                        |
| runtime state      | `OPENHANDS_WORKSPACE`                                                                                                                                                | `.env` / shell env, with `/workspace/project` as fallback                                                     |
| script-assembled   | `EXECUTOR_API_BIND`, `EXECUTOR_POLL_INTERVAL`                                                                                                                        | hard-coded in `02-start-executor.sh`                                                                          |
| script-assembled   | `EXECUTOR_STATE_REGISTRY_URL`, `EXECUTOR_STATE_REGISTRY_TLS_CLIENT_CERT`, `EXECUTOR_STATE_REGISTRY_TLS_CLIENT_KEY`, `EXECUTOR_STATE_REGISTRY_TLS_SERVER_CA`          | mTLS material path bundle from `state/certs/`                                                                 |
| script-assembled   | `EXECUTOR_SCOPE=team`, `EXECUTOR_TEAM_ID`, `EXECUTOR_AUTHORIZED_TAG=openhands`, `EXECUTOR_MAX_CONTAINERS=1`, `OPENHANDS_INITIAL_RUN=true`, `FLOWAI_CLEANUP_ID_DIR=…` | derived from the State Registry admin onboarding performed by `01-prepare.sh`                                 |
| shell env          | `PATH`, `HOME`                                                                                                                                                       | the only inherited keys `env -i` re-adds, so the Executor can locate shared libraries and its own `~/.config` |

Keys that DO **NOT** reach the Executor subprocess:

- The State Registry AES key, the cursor-token HMAC key, and the
  scope-token HMAC key (these are secrets of the Registry, not of the
  Executor).
- The Postgres password and DSN (the Executor does not talk to
  Postgres; only the State Registry does).
- The Listener / Operator / Admin client certificates and keys
  (these are identities of OTHER services, not of the Executor).
- The runtime state directory's `state/env.sh`, `state/certs/*`, and
  `logs/*` (none of these are mounted into the Executor process).
- The `manual-qa/.env` file itself. The dotenv loader parses it inside
  the operator shell and exports the values into `EXEC_ENV`; the
  parser never sources the file and the file is not mounted into the
  OpenHands child container.

Keys that reach the OpenHands V1 child container:

- The Executor forwards the LLM configuration (model, API key, usage
  id, optional base URL) inside the V1 conversation request that the
  Executor builds when it claims a task. The V1 agent-server is what
  actually contacts the upstream LLM provider; the keys reach it via
  the Executor's V1 conversation body, NOT via environment variables
  on the child container and NOT via a bind-mount of `manual-qa/.env`.
- `OPENHANDS_WORKSPACE` is the in-container path the V1 child treats
  as its workspace root. The Executor does NOT bind-mount the host
  repository into the child; the value is forwarded verbatim.
- The Executor does NOT receive any State Registry admin material or
  any of the host's Postgres credentials, so the child cannot see them
  either.

In short: the dotenv file is the operator's interface to the
Executor's runtime inputs. Everything inside the four-level image
precedence (`teams.default_image`, `source_systems.default_image`,
`task_types.default_image`, `tasks.image`) and every State-Registry /
Postgres secret stays on the Registry host and never reaches the
Executor or the OpenHands child.

## 6. Security notes

- The runtime directory is created with mode 0700 and every state file
  is written with mode 0600 by these scripts. Private keys are always
  mode 0600. The runtime root is validated to refuse symlinks and
  `..` traversal segments.
- `mq::state_require_env` refuses to source the state env file
  unless it is owned by the current user and is mode 0600.
- `mq::load_dotenv` never sources the file and never uses `eval` or
  `set -a`; it parses `KEY=value` lines into a fixed allow-list, refuses
  symlinks, refuses non-current-owner files, refuses any mode that
  grants group / world access (only 0600 / 0400 are accepted), rejects
  NUL bytes and carriage returns, and rejects `$` / `` ` `` / `\`
  characters inside unquoted values. Shell exports — including
  explicitly empty exports — always win over values from the dotenv
  file.
- All admin / listener / gateway / Executor requests to the State
  Registry go over mTLS with `--fail-with-body --cacert --cert --key`.
  There is no `curl -k`, no `InsecureSkipVerify`, no plaintext
  fallback for the State Registry surfaces.
- `STATE_REGISTRY_TEST_MODE` is never set; the State Registry runs in
  the normal untagged production mode and the verified peer certificate
  is the sole source of identity headers.
- The Postgres connection string carries `sslmode=verify-full` so the
  State Registry's Go client (pgx) verifies the server chain against
  the **`ca.crt` bundle**, not the Postgres leaf cert. The custom
  Postgres entrypoint copies the staged cert + key into
  `/var/lib/postgresql/` with `postgres:postgres` ownership and modes
  `0600` (key) / `0644` (cert) before execing the image's original
  entrypoint. The whole tls-dir is mounted as a single Podman private
  SELinux volume (`--volume ...:/etc/flowai/pg-tls:ro,Z`), and the
  container is started with
  `--entrypoint /bin/bash /etc/flowai/pg-tls/custom-entrypoint.sh postgres -c ssl=on ...`
  so the bash shebang is not required to point at the in-image
  interpreter path.
- The Listener certificate carries the team's `O=` and the
  source-system's `serialNumber=`. The Team Executor certificate
  carries `O=<team_id>` and `CN=exec-local-openhands` (matches the
  pinned `executor_id` in the Executor YAML). The Gateway certificate
  carries `O=<team_id>` and `CN=operator-local`. The Admin certificate
  carries `CN=system-admin,OU=admin` with no `O=`, no `serialNumber=`.
  `03-submit-task.sh` validates the listener cert's `O=` and
  `serialNumber=` equal the persisted `FLOWAI_TEAM_ID` and
  `FLOWAI_SOURCE_SYSTEM_ID` before issuing the POST.
- LLM API keys are forwarded to the Executor subprocess via `env -i`,
  never written to a state file, and never logged.
- All `openssl` subprocesses are bounded by GNU `timeout` and an
  output cap; a stuck invocation cannot hang the script.
- Every process termination is gated on `/proc/PID/cmdline` matching
  the expected binary path so a stale PID file from a previous run
  cannot accidentally kill an unrelated process the kernel has since
  reused the PID for.
- Every Bash variable expansion that could touch a value from outside
  the script is either quoted or passed as a discrete array element.
  The state-env file is emitted with `printf %q` so values never
  become shell-active.

## 7. Expected task lifecycle

After `03-submit-task.sh` returns a `task_id`:

| State                    | Source         | When                                                                                                                                                                                                  |
| ------------------------ | -------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `pending`                | State Registry | Ingestion writes the durable row with no lifecycle event. The Executor is not yet aware.                                                                                                              |
| `created`                | State Registry | First event on successful FIFO claim. Appended in the same transaction.                                                                                                                               |
| `running`                | Executor       | Appended after the claim and after the environment open + image pull succeed.                                                                                                                         |
| `finished` _or_ `failed` | Executor       | Exactly one terminal event. `finished` means OpenHands reached the `FINISHED` task status; `failed` covers `failed`, `error`, `stuck`, `paused` interrupts, health-check failure, and submit failure. |

The first event on a claimed task is `created` (not `running`), and
the `dispatched` state from v0001 is removed; no event of that name is
appended at any point.

The dedupe key is `(team_id, source_system_id, source_id)`. We derive
`source_id` from a SHA-256 of the prompt, so a retry of
`03-submit-task.sh` with the **same prompt** (in this runtime
directory) returns the existing canonical `task_id` with status 200
instead of 201. The dedupe is per-run: the same prompt against the
same listener / source-system returns the same task_id as long as
the listener / source-system / team identity are stable.

## 8. Troubleshooting

- **"rootless Podman socket not found"** — enable the user systemd
  socket: `systemctl --user enable --now podman.socket`. Verify with
  `ls -l $XDG_RUNTIME_DIR/podman/podman.sock`.
- **"state-registry exited before becoming ready".** Look at
  `$FLOWAI_MANUAL_QA_RUN_DIR/logs/state-registry.log`. The most
  common cause is a Postgres DSN mismatch (verify-full needs the
  Postgres server cert to be signed by the CA at `state/certs/ca.crt`).
- **"executor did not report state_registry_registered=true within
  30s"** — `02-start-executor.sh` polls `/v1/readyz` and waits for
  the JSON field `state_registry_registered: true`. Inspect
  `$FLOWAI_MANUAL_QA_RUN_DIR/logs/executor.log` for the registration
  attempt. HTTP 503 is tolerated as long as the field is true
  (OpenHands reachability is not required before the first task).
- **"task entered unknown state"** — current production code only
  ever emits `pending`, `created`, `running`, `finished`, `failed`.
  Anything else is a wire bug.
- **"OpenHands container never comes up"** — verify the LLM
  credentials are valid; the V1 agent-server exits on startup if the
  chat-completions endpoint rejects the key. Look at the executor log
  and `podman logs <container>`.
- **"task_already_claimed"** — a previous run still owns the task via
  its `command_id`. Run `99-cleanup.sh` to terminate the stale
  Executor (the cleanup wipes the slot / port reservation), or wait
  for the terminal event.
- **A leftover container with `flowai.executor_id=exec-local-openhands`
  is still alive after `99-cleanup.sh`** — `99-cleanup.sh` removes
  every container matching that label via
  `podman ps -aq --filter label=...`. If one persists, run
  `podman rm -f $(podman ps -aq --filter label=flowai.executor_id=exec-local-openhands)`.
- **Executor probe fails on `/v1/livez`** — the executor binds
  **plaintext HTTP** on `127.0.0.1:8020`. Probe with
  `curl http://127.0.0.1:8020/v1/livez`, never with `--cacert`. mTLS
  is only used against the State Registry on
  `https://localhost:18443`.

## 9. Cleanup

`./99-cleanup.sh` is idempotent:

1. Verify the executor's `/proc/PID/cmdline` matches
   `executor_docker_opehands`, then `SIGTERM` (then `SIGKILL` after
   a deadline) the executor process.
2. Verify the State Registry's `/proc/PID/cmdline` matches
   `state-registry`, then `SIGTERM` (then `SIGKILL`) the State
   Registry process. PID-reuse is detected and refused before any
   signal is sent.
3. `podman rm -f` every container with
   `flowai.executor_id=exec-local-openhands`.
4. `podman rm -f` the Postgres container.
5. `rm -rf` the protected `$FLOWAI_MANUAL_QA_RUN_DIR` (unless
   `--keep-runtime-state` is passed; then only pids + pg artifacts
   are removed so the operator can inspect certs, logs, and the
   binding summary on disk after the run).

The Postgres data volume lives on tmpfs inside the ephemeral
container, so a stale Postgres data directory cannot leak across runs.
The flow is fully self-contained: nothing in the repository changes;
nothing world-readable stays behind on the host.

`--keep-runtime-state` is for **post-mortem inspection only**. It does
NOT make the next preparation a no-op reuse path: any subsequent
`./01-prepare.sh` requires either a fresh
`FLOWAI_MANUAL_QA_RUN_DIR` or a plain `./99-cleanup.sh` to remove the
prior runtime directory before re-preparation.
