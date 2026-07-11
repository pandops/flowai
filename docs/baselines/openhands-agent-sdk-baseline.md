# OpenHands Agent SDK — Baseline Specification

> **Scope of this document.** A precise characterization of the OpenHands Software Agent SDK for the purposes of comparing it against alternative "agent runtime" candidates inside the FlowAI Docker executor. This is a **baseline reference**, not a recommendation. No comparison or judgement of alternatives is made here.
>
> **Source of truth.** Cloned from `github.com/OpenHands/software-agent-sdk` at commit `57374ec1bbdcb3db5cb806219c3cb4d8b6684f74` on 2026-07-09; cross-referenced against the official docs (`docs.openhands.dev/sdk`), the OpenAPI/REST surface generated under `openhands-agent-server/`, and the GHCR container builds (`ghcr.io/openhands/agent-server`).

---

## 0. Versioning context (V0 vs V1)

OpenHands split into two generations during the 2025/2026 transition:

| Generation | Where it lives | License | Status |
|---|---|---|---|
| **V0 (legacy)** | `github.com/All-Hands-AI/OpenHands` (transferred to `OpenHands/OpenHands` on 2025-10-23) | no top-level `LICENSE` file | Marked `# IMPORTANT: LEGACY V0 CODE - Deprecated since version 1.0.0, scheduled for removal April 1, 2026`. Last release 0.59.0. |
| **V1 (current)** | `github.com/OpenHands/software-agent-sdk` (also reachable as the redirect from `All-Hands-AI/agent-sdk` since the org transfer) | **MIT** | Active. This is the SDK you integrate with. |

**Permalinks:**
- Org transfer notice: https://github.com/OpenHands/OpenHands/issues/11376
- Legacy V0 deprecation banner inside the source: https://github.com/All-Hands-AI/OpenHands/blob/3ec999e8/openhands/core/main.py
- V1 SDK root: https://github.com/OpenHands/software-agent-sdk (at commit `57374ec1…`)

**The V1 SDK is the "agent runtime" you would embed**, not the V0 monorepo. All references below are to V1 unless otherwise noted.

---

## 1. Repository location and current state

### 1.1 Canonical repository

| Field | Value | Source / Permalink |
|---|---|---|
| Org / repo | `OpenHands / software-agent-sdk` | https://github.com/OpenHands/software-agent-sdk |
| Default branch | `main` | `gh api repos/OpenHands/software-agent-sdk` |
| License | **MIT** | root `LICENSE`, repo metadata |
| Primary language | Python (98.6%) + thin JS / HTML / Shell wrappers (0.5% / 0.4% / 0.1%) | `gh repo view` languages |
| Head commit at time of report | `57374ec1bbdcb3db5cb806219c3cb4d8b6684f74` (`ci(version-bump-prs): make PR-creation steps independent (#4051)`) | `git log -1` on the cloned tree |
| Created | 2025-08-23 | `gh repo view` `createdAt` |
| Stargazers / Forks | 859 / 324 | `gh repo view` |
| Open issues (as of 2026-07-09) | 126 | `gh api search/issues?q=repo:OpenHands/software-agent-sdk+is:issue+is:open` |

### 1.2 Latest releases and cadence

PyPI shows an unbroken cadence since 2025-10-14 with **60+ tagged releases** to date. Latest at report time: **`openhands-sdk 1.34.0`** (2026-07-09). PyPI release history (selected):

```
1.34.0  Jul  9, 2026
1.33.0  Jul  8, 2026
1.32.0  Jul  7, 2026
1.31.2  Jul  7, 2026
1.31.1  Jul  5, 2026
1.31.0  Jul  2, 2026
1.30.0  Jul  1, 2026
1.29.3  Jun 26, 2026
…
1.0.0   Nov  6, 2025    ← first stable v1
```

**Source**: https://pypi.org/project/openhands-sdk/#history

The PyPI sdist at 1.34.0 is **611.8 kB** source / **733.9 kB** wheel (`py3-none-any`). Upload via `uv/0.11.28 publish` on Ubuntu 24.04 CI runner.

GitHub releases include both SDK tags (`v1.34.0`) and **image tags** for the agent-server container (`cloud: 1.44.0` from 2026-07-09; `1.10.0` from 2026-07-08; etc.). These `cloud:`-prefixed tags are pushed to GHCR for the container image and are *distinct* from the SDK Python release.

### 1.3 Maintenance signals

| Signal | Observation | Source |
|---|---|---|
| Releases in last 30 days | ~12 (between v1.30.0 and v1.34.0) | PyPI history |
| PRs merged on **today** (2026-07-09) | ≥ 3 (PRs #4051, #3987, #4008) | `gh search prs` |
| New issues on **today** (2026-07-09) | ≥ 3 (issues #4054, #4053, #4050) | `gh search issues` |
| Cumulative contributors | 100 (top: `xingyaoww`, `enyst`, `simonrosenberg`, `neubig`, `VascoSch92`) | `gh repo view` |
| Dependabot | enabled (PRs from `dependabot[bot]` visible in contributor list) | contributor list |
| Workspace dependency guards | CVEs pinned in top-level `pyproject.toml`: `starlette>=0.49.1` (CVE-2025-62727), `aiohttp>=3.13.3` (CVE-2025-69223 + 7), `urllib3>=2.6.3` (CVE-2026-21441/66471/66418), `protobuf>=6.33.5` (CVE-2026-0994), `pillow>=12.1.1` (CVE-2026-25990), `orjson>=3.11.7`, `rich>=14.3.3`, `lupa>=2.8` (CVE-2026-34444) | https://github.com/OpenHands/software-agent-sdk/blob/57374ec1…/pyproject.toml#L20-L37 |

**Verdict**: very actively maintained. Expect a release every 1–7 days.

### 1.4 CI workflows

`gh api repos/.../contents/.github/workflows` lists **27 workflow files** (verbatim names):

`README-RELEASE.md`, `agent-server-rest-api-breakage.yml`, `api-breakage.yml`, `assign-reviews.yml`, `auto-label-issues.yml`, `cancel-eval.yml`, `check-docstrings.yml`, `check-documented-examples.yml`, `check-duplicate-examples.yml`, `create-release.yml`, `deploy-docs.yml`, `deprecation-check.yml`, `integration-runner.yml`, `issue-duplicate-checker.yml`, `oh-update-documentation.yml.back`, `persisted-settings-compat.yml`, `pr-artifacts.yml`, `pr-description-check.yml`, `precommit.yml`, `prepare-release.yml`, `pypi-release.yml`, `qa-changes-by-openhands.yml`, `qa-changes-evaluation.yml`, `release-binaries.yml`, `remove-duplicate-candidate-label.yml`, `review-thread-gate.yml`, `run-eval.yml`, `run-examples.yml`, `server.yml`, `stale.yml`.

The container is built by `server.yml` at `.github/workflows/server.yml`.

---

## 2. Language / runtime requirements

### 2.1 Inside the agent container

| Layer | Requirement | Evidence |
|---|---|---|
| Python (SDK & server) | **`>= 3.12`** per `openhands-sdk/pyproject.toml`; the Dockerfile currently pins **3.13** in its runtime base (`nikolaik/python-nodejs:python3.13-nodejs22-slim`) | `pyproject.toml: requires-python = ">=3.12"`; https://github.com/OpenHands/software-agent-sdk/blob/57374ec1…/openhands-agent-server/openhands/agent_server/docker/Dockerfile#L6 |
| Python runtime management | `uv` 0.11.6 baked into builder & runtime (`ghcr.io/astral-sh/uv:0.11.6 /uv /uvx /bin/`) — pulls python-build-standalone into `/agent-server/uv-managed-python` for reproducibility | Dockerfile lines 32–41 |
| Node.js (ACP agents only) | **Node 22.14.0** installed to dedicated `/opt/acp-node/`; binary wrappers in `/usr/local/bin/` for `claude-agent-acp`, `codex-acp`, `gemini`. Skipped gracefully on incompatible glibc/musl bases | Dockerfile lines 153–207 |
| OpenVSCode Server | `gitpod-io/openvscode-server-v1.98.2` (deb-arch tarball) | Dockerfile line 240 |
| Linux server user | `openhands` UID/GID **10001/10001**, passwordless sudo via `/etc/sudoers` | Dockerfile lines 7–9, 142–149 |
| Python deps (locked) | `lmnr>=0.7.47,<0.7.53`, `litellm>=1.84.1`, `fastmcp>=3.0.0`, `pydantic>=2.12.5`, `python-json-logger>=3.3.0`, `agent-client-protocol>=0.10.1`, `httpx[socks]>=0.27.0`, `websockets>=12`, `tenacity>=9.1.2`, `pillow>=12.1.1`, `tree-sitter>=0.25`, `tree-sitter-bash>=0.25`, `joserfc>=1.0.0`, `filelock>=3.20.1`, `python-frontmatter>=1.1.0`, `fakeredis[lua]>=2.32.1`, `deprecation>=2.1.0` | `openhands-sdk/pyproject.toml` |
| Optional extras | `boto3` (built into runtime image), `vertex` (requires `--build-arg ENABLE_VERTEX=1`) | `pyproject.toml` + Dockerfile line 15 |
| OS shell tools | `bash ca-certificates curl wget sudo git jq tmux tar build-essential coreutils util-linux procps findutils grep sed tini …`. Multi-distro support: `apt-get` / `apk` / `microdnf` / `dnf` / `yum` / `zypper` paths. | Dockerfile lines 96–141 |
| Locale | `LC_ALL=C.UTF-8`, `LANG=C.UTF-8` (required for libtmux + PyInstaller) | Dockerfile lines 221–222 |
| Workspace dir | `/workspace/` (created, chowned to `openhands`) | Dockerfile line 150 |
| Runtime defaults | `LOG_JSON=true`, `OH_ENABLE_VNC=false`, `PORT=8000` baked into the image | Dockerfile lines 223–225, 334–337 |

### 2.2 SDK on the host (alternative deployment mode)

The Python packages (`openhands-sdk`, `openhands-tools`, `openhands-workspace`, `openhands-agent-server`) install into any CPython ≥ 3.12 venv via `uv pip install`. Containerisation is *optional*, delegated to `DockerWorkspace` / `DockerDevWorkspace`. **There is no GPU requirement at any layer.**

---

## 3. Container / distribution story

### 3.1 The "agent runtime" image

OpenHands ships a single multi-purpose container that **is** the agent runtime:

> **`ghcr.io/openhands/agent-server`**

This image runs the OpenHands SDK as a FastAPI/WebSocket server (`python -m openhands.agent_server`) with the SDK and a full development workspace (Node 22, Python venv, **Docker-in-Docker**, Chromium, VSCode Web, VNC, GitHub CLI, ACP agents).

### 3.2 Dockerfile architecture (verified source)

The Dockerfile at `openhands-agent-server/openhands/agent_server/docker/Dockerfile` is a **multi-stage build with 4 final targets**:

```dockerfile
ARG BASE_IMAGE=nikolaik/python-nodejs:python3.13-nodejs22-slim          # line 6

# Stage 1: builder       (python:3.13-bookworm + uv 0.11.6 -> venv under /agent-server)
# Stage 2: binary-builder (PyInstaller; emits /agent-server/dist/openhands-agent-server)
# Stage 3: base-image-minimal (FROM ${BASE_IMAGE}; base OS + uv runtime + Node 22 for ACP)
# Stage 4: base-image       (full image; adds VSCode Web, Docker, gh CLI, VNC, XFCE, Chromium)

FROM base-image          AS source         # entrypoint: tini -- /agent-server/.venv/bin/python -m openhands.agent_server
FROM base-image          AS binary         # entrypoint: tini -- /usr/local/bin/openhands-agent-server
FROM base-image-minimal  AS source-minimal
FROM base-image-minimal  AS binary-minimal
```

Permalink: https://github.com/OpenHands/software-agent-sdk/blob/57374ec1…/openhands-agent-server/openhands/agent_server/docker/Dockerfile

### 3.3 Three runtime variants × two architectures

Per `.github/workflows/server.yml` (`jobs.build-and-push-image.strategy.matrix`), the project ships **6 per-architecture builds** plus multi-arch manifests:

| Variant | Base image | Architectures | Default tag suffix |
|---|---|---|---|
| `python` | `nikolaik/python-nodejs:python3.13-nodejs22-slim` | linux/amd64 + linux/arm64 | `python` |
| `java` | `eclipse-temurin:17-jdk` | amd64 + arm64 | `java` |
| `golang` | `golang:1.21-bookworm` | amd64 + arm64 | `golang` |

Reference: workflow matrix lines 274–313 of `server.yml`.

The same matrix is encoded in the tag generator at `docker/build.py` (`BuildOptions.image = "ghcr.io/openhands/agent-server"`, `BuildOptions.custom_tags` driven by the matrix).

- https://github.com/OpenHands/software-agent-sdk/blob/57374ec1…/.github/workflows/server.yml
- https://github.com/OpenHands/software-agent-sdk/blob/57374ec1…/openhands-agent-server/openhands/agent_server/docker/build.py

### 3.4 Image tag scheme (parsed from `BuildOptions.all_tags`)

For a commit `57374ec1bbdcb3db5cb806219c3cb4d8b6684f74`:

```
ghcr.io/openhands/agent-server:57374ec-python        ← multi-arch manifest (recommended)
ghcr.io/openhands/agent-server:57374ec1bbdc-python   ← multi-arch full SHA
ghcr.io/openhands/agent-server:57374ec-python-amd64  ← arch-specific
ghcr.io/openhands/agent-server:57374ec-python-arm64  ← arch-specific
ghcr.io/openhands/agent-server:main-python          ← on main
ghcr.io/openhands/agent-server:latest-python        ← on main (alias)
ghcr.io/openhands/agent-server:1.34.0-python        ← semver-tagged release
ghcr.io/openhands/agent-server:1.34-python          ← semver alias
ghcr.io/openhands/agent-server:1-python             ← major alias
```

Semver aliases come from `BuildOptions._release_tag_aliases` at `build.py#L230`.

### 3.5 "Agent runtime" vs full OpenHands app

There is **no separately distributed "agent runtime" image** that excludes the web/CLI/Cloud surface. The `*-minimal` Dockerfile targets strip VSCode Web / Docker / VNC / Chromium, but the agent server entrypoint and full SDK remain. The "full OpenHands app" (web UI, Cloud, Agent Canvas) **consumes** this SDK as a library; the SDK image is the only required runtime artefact. (`OpenHands/OpenHands` README confirms `OpenHands/agent-canvas` and `OpenHands/OpenHands` consume this SDK.)

### 3.6 What the image installs (full variant)

From the Dockerfile (commit `57374ec1`):

| Tool | Where | Why |
|---|---|---|
| `tini` PID-1 reaper | `ENTRYPOINT ["tini", …]` | Clean SIGTERM forwarding |
| `python-build-standalone` (3.13) | `/agent-server/uv-managed-python` | Reproducible interpreter |
| `.venv` with `openhands-sdk/-tools/-workspace/-agent-server` | `/agent-server/.venv` | Self-contained `/agent-server` directory contract (copy-onto-any-base works) |
| Claude Code, Codex, Gemini CLI (ACP servers) | `/opt/acp-node/` + npm globals | Used by `ACPAgent` (delegate to external agents) |
| Claude Code `managed-settings.json` | `/etc/claude-code/managed-settings.json` | Allow `[Edit, Read, Bash]` with no human loop |
| OpenVSCode Server `1.98.2` | `/openhands/.openvscode-server`; served on **port `host_port+1`** | In-browser IDE for the workspace |
| Docker Engine + buildx + compose | DinD on port 2375 (no TLS); `daemon.json` with `mtu: 1450` | Lets the agent spawn its own containers |
| GitHub CLI (`gh`) | `PATH` | Workflows from inside the agent |
| VNC (tigervnc-standalone-server) + XFCE4 + noVNC + websockify | served on **port `8002`** (`$NOVNC_PORT`) | Visual browser session for `browser-use` |
| Chromium (`/usr/bin/chromium`) | `$CHROME_BIN`, `$PUPPETEER_EXECUTABLE_PATH` | `browser-use` automation driver |
| Custom XFCE wallpaper | `/usr/share/backgrounds/xfce/xfce-shapes.svg` | Cosmetic |
| Chromium flags | `--no-sandbox --disable-dev-shm-usage --disable-gpu` | Run as non-root |

### 3.7 Caveats on size / architecture

- **Multi-arch**: yes — image manifests combine `linux/amd64` and `linux/arm64`; CI uses `docker buildx imagetools create` (server.yml lines 521–527) to merge per-arch images into a manifest list.
- **Compressed size**: not retrieved (no `read:packages` scope on the GHCR endpoint in this environment). The image **is large** because the full variant bundles Chromium, XFCE4, VSCode Server, Node 22, DinD, GitHub CLI, Python, and the PyInstaller-compiled `openhands-agent-server` binary.
- **Architecture quirks**: PyInstaller build uses Python **3.13** (note in `build.py` lines 381–388 acknowledges a PyInstaller+libtmux issue on 3.12/3.13; see SDK issue #1886). The Dockerfile comments out a `python-build-standalone >= 20260408` requirement for the fix, pinned via `uv 0.11.6` (Dockerfile lines 22–32 and #2761 in the SDK repo). It's fragile and worth noting for ops.
- **`OPENHANDS_BUILD_GIT_SHA` / `OPENHANDS_BUILD_GIT_REF`**: build-args stamped into the runtime image as env vars; exposed via `/server_info`.

### 3.8 Default ports & auth when used as FlowAI agent runtime

| Endpoint | Auth | Source |
|---|---|---|
| `GET /health`, `GET /ready` | none | `sdk/arch/agent-server.md` "Useful Endpoints" |
| `GET /server_info`, `GET /docs` (OpenAPI UI) | none | same |
| `/api/*` REST | `X-Session-API-Key: $OH_SESSION_API_KEYS_<n>` | `auth_router.py`, `sockets.py:104–212` |
| WebSocket | same header (sent before upgrade); `code=4001` rejection on failure | `<…/agent_server/sockets.py>` |
| OpenAI-compat `/v1/chat/completions` | same key, OR `Authorization: Bearer <key>` | `openai/router.py` `check_openai_api_key` |
| `webhooks` (push-out from server) | bearer token in `headers` of each configured webhook | `agent-server.md` + README "Webhook Configuration" |

Encrypted secrets stored in conversation state are encrypted via `$OH_SECRET_KEY` (Fernet/AES). **Must remain stable across restarts** — otherwise values are unrecoverable.

### 3.9 Default binding (CARE!)

The agent server **binds to `0.0.0.0:8000` by default** — must be overridden with `--host 127.0.0.1` (or kept behind a TLS reverse proxy) for production. This is documented in `openhands-agent-server/README.md` and called out in `sdk/arch/agent-server.md` ("Expose It Safely"). Do not ship with `0.0.0.0` and no API key.

### 3.10 Configuration model

- **Environment variables** (legacy): `SESSION_API_KEY`, `OH_SECRET_KEY`, `OH_ALLOW_CORS_ORIGIN_REGEX`, …
- **Environment variables (indexed)**: `OH_SESSION_API_KEYS_0`, `OH_SESSION_API_KEYS_1`, … (supports key rotation)
- **JSON config file** at `$OPENHANDS_AGENT_SERVER_CONFIG_PATH` (default `workspace/openhands_agent_server_config.json`). Supports:
  ```json
  {
    "session_api_key": "...",
    "allow_cors_origins": ["https://..."],
    "allow_cors_origin_regex": null,
    "conversations_path": "workspace/conversations",
    "webhooks": [
      {
        "webhook_url": "https://your-webhook-endpoint.com/events",
        "method": "POST",
        "event_buffer_size": 10,
        "num_retries": 3,
        "retry_delay": 5,
        "headers": { "Authorization": "Bearer …" }
      }
    ]
  }
  ```

### 3.11 Webhook capability (often missed)

`webhooks[]` is a first-class config knob: the server can push event notifications to an external HTTP endpoint, with **buffered batching** (`event_buffer_size`, default 10) and **retries** (`num_retries`, default 3; `retry_delay`, default 5 s). This is "the thing the SDK docs almost never lead with" — useful for FlowAI's executor → router event handoff if FlowAI doesn't want to scrape `/api/conversations/{id}/events` itself.

---

## 4. Observability surface (the comparison axis)

This is the deepest section because it's the comparison axis for FlowAI.

The observability surface has **three layers**, and one **absent fourth**:

1. **Structured logging** — JSON to stdout (or Rich for humans).
2. **Distributed tracing** — OpenTelemetry OTLP, gated on env vars, with Laminar as the reference instrumentation layer.
3. **Per-call metrics** — Promoted to a first-class Pydantic model (`Metrics`), accessible by API on the conversation.
4. **Absent**: no Prometheus `/metrics`, no statsd, no OpenTelemetry *metrics* (only traces). Tokens / cost / latency are **per-conversation** JSON, not time-series.

### 4.1 Tracing (OpenTelemetry via Laminar)

**Standard**: OpenTelemetry OTLP (HTTP/protobuf or gRPC). The SDK uses **`lmnr` (Laminar)** as its instrumentation layer — `lmnr>=0.7.47,<0.7.53` is a hard dependency of `openhands-sdk`. Evidence:
```python
# openhands-sdk/openhands/sdk/observability/laminar.py:91
from lmnr import Instruments, Laminar
```
https://github.com/OpenHands/software-agent-sdk/blob/57374ec1…/openhands-sdk/openhands/sdk/observability/laminar.py#L91

**Activation**: zero-code — the SDK checks these env vars on import:

```
LMNR_PROJECT_API_KEY                ← Laminar (enables browser session replay)
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT
OTEL_EXPORTER_OTLP_ENDPOINT
OTEL_ENDPOINT
OTEL_EXPORTER_OTLP_TRACES_HEADERS
OTEL_EXPORTER_OTLP_HEADERS
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL  # "http/protobuf" (recommended) | "grpc"
OTEL_EXPORTER                       # "otlp_http" | "otlp_grpc"
LMNR_BASE_URL, LMNR_HTTP_PORT, LMNR_GRPC_PORT, LMNR_FORCE_HTTP
```

Reference: `laminar.py#L27-L82`. With none of these set, observability is **lazy-disabled**: `maybe_init_laminar()` short-circuits and the `@observe` decorator is a no-op (no `lmnr` even gets imported):

```python
# laminar.py:118-198
def observe(name=None, session_id=None, user_id=None,
            span_type: Literal["DEFAULT","LLM","TOOL"] = "DEFAULT", …):
    """Lazy-resolving observe decorator. When observability is not enabled,
    decorated functions run as pass-throughs with no `lmnr` import."""
```

The implementation explicitly swaps `start_active_span` for `start_span + use_span` to survive asyncio / thread context propagation — they observed orphan traces on `start_active_span` (~60% of conversations lost `session_id`; see `RootSpan` docstring at `laminar.py#L234-L254`).

**Backends supported** (verified by docs):
- **Laminar / Laminar self-hosted** (`https://api.lmnr.ai` default, or `LMNR_BASE_URL` for self-host)
- **MLflow** (with `x-mlflow-experiment-id` header)
- **Honeycomb** (`https://api.honeycomb.io:443/v1/traces` + `x-honeycomb-team=…`)
- **Jaeger** (gRPC, port 4317)
- **Any OTLP Collector**

Reference docs: https://docs.openhands.dev/sdk/guides/observability (verified verbatim).

**Span hierarchy**:

```
conversation
└─ conversation.run
   └─ agent.step
      ├─ llm.completion
      └─ tool.execute
   └─ agent.step
      └─ llm.completion
```

`@observe(name=…, session_id=…, span_type="LLM" | "TOOL")` decorators are wired into:
- `MCPToolExecutor.call_tool` — `span_type="TOOL"` (`mcp/tool.py:67`)
- `RemoteConversation.send_message` — `name="conversation.send_message"` (`remote_conversation.py:1108`)
- All hooks (`hooks/executor.py:204`)
- `LLM.completion()`, internal `tool.execute`, …

**Span attributes auto-emitted**: `conversation_id` (UUID), `session_id` (= conversation UUID, groups all traces from one conversation), `tool_name`, `action.kind`.

**Browser session replays**: only enabled when the **Laminar** backend is used (not generic OTLP). The `_is_otel_backend_laminar()` heuristic at `laminar.py#L221-L228` checks for `LMNR_PROJECT_API_KEY`. For non-Laminar backends, browser instruments are **explicitly disabled**:

```python
# laminar.py:107-115
Laminar.initialize(
    disabled_instruments=[
        Instruments.BROWSER_USE_SESSION,
        Instruments.PATCHRIGHT,
        Instruments.PLAYWRIGHT,
    ],
    force_http=force_http,
)
```

Replays use **rrweb**. Doc: `/sdk/guides/browser-session-recording`.

**Foreign-trace context**: `init_laminar_for_external()` at `laminar.py#L410-L445` is provided for webhook integrations (GitHub, Slack) to attach external trace context as parent.

**Sampling**: no SDK-side sampler. Sampling is delegated to the OTLP collector (the doc's only recommendation: "configure sampling at the collector level").

**Tracing for non-LLM work**: there is no metric on tool-call latency, only on LLM `ResponseLatency`. Tool-call durations appear only as the wall-clock span on each `tool.execute`. Errors set the OpenTelemetry span status automatically by virtue of `@observe`.

### 4.2 Logging

**Architecture**: dual-mode logger, switched by `LOG_JSON` env var (defaults to `false` in dev, **`true` in the published agent-server image**).

Source: `openhands-sdk/openhands/sdk/logger/logger.py`

- **RichHandler** (stderr) when `LOG_JSON=false` and `CI` not set — human-readable, with optional colored tracebacks (`LOG_RICH_TRACEBACKS`, default `true`).
- **JSONFormatter (python-json-logger)** when `LOG_JSON=true` or `CI=true`:
  ```python
  fmt = "%(asctime)s %(levelname)s %(name)s %(filename)s %(lineno)d %(message)s"
  ```
  Single-line JSON per record. Default fields: time, level, logger name, source `file:line`, message. **No built-in `trace_id`/`span_id` field** — cross-referencing with traces requires joining on application `session_id` / `conversation_id` that the caller must propagate. (See "Known gaps" §9.)

**Settings (read at module import)**:
```python
LOG_LEVEL = INFO                  # DEBUG=true forces DEBUG
LOG_JSON = false                  # image default is true
LOG_TO_FILE = false               # also writes TimedRotatingFileHandler when true
LOG_DIR = logs                    # when LOG_TO_FILE
LOG_ROTATE_WHEN = midnight
LOG_BACKUP_COUNT = 7
LOG_RICH_TRACEBACKS = true
LOG_AUTO_CONFIG = true            # auto-call setup_logging on import
DEBUG_LLM = false                 # interactive y/N confirmation required; warns about API-key leakage
```
(`logger.py#L29-L53`)

**Third-party noise suppressed at import**:
- `litellm`, `LiteLLM`, `openai` → silenced to `ERROR`
- `httpcore`, `httpx`, `libtmux` → silenced to `WARNING`

**Log shipping**: the agent-server image ships **no log-shipper sidecar**. Logs come out of stdout as JSON (`LOG_JSON=true`). Vector / Fluent Bit / Promtail scrape them as-is.

**Audit/event log**: there is a **separate** per-task event log exposed via the `event_router` (`/api/conversations/{id}/events`) which is the canonical agent-history stream (§7). Conceptually distinct from the logger.

### 4.3 Metrics (Pydantic model, not Prometheus)

**There is no Prometheus-style `/metrics` endpoint and no OTel metrics exporter.** The SDK exposes metrics as a **Pydantic model attached to each `LLM` and `Conversation`**. Consumers must scrape them via the SDK's Python API or via the Agent Server's REST/WebSocket.

**The `Metrics` model** (verbatim from `openhands-sdk/openhands/sdk/llm/utils/metrics.py`):

```python
class Cost(BaseModel):
    model: str
    cost: float                    # >= 0
    timestamp: float = Field(default_factory=time.time)

class ResponseLatency(BaseModel):
    model: str
    latency: float                 # seconds, >= 0
    response_id: str

class TokenUsage(BaseModel):
    model: str = ""
    prompt_tokens: int = 0
    completion_tokens: int = 0
    cache_read_tokens: int = 0      # cache hits (model-dependent)
    cache_write_tokens: int = 0
    reasoning_tokens: int = 0       # extended-thinking (Anthropic / OpenAI reasoning models)
    context_window: int = 0
    per_turn_token: int = 0
    response_id: str = ""

class MetricsSnapshot(BaseModel):    # summary view
    model_name: str = "default"
    accumulated_cost: float = 0.0
    max_budget_per_task: float | None = None
    accumulated_token_usage: TokenUsage | None = None
    @property
    def cache_hit_rate(self) -> float | None:   # 0.0..1.0, handles OpenAI/ACP-style denominators

class Metrics(MetricsSnapshot):     # full record
    costs: list[Cost]
    response_latencies: list[ResponseLatency]
    token_usages: list[TokenUsage]   # one per API call; entire history kept in memory
```

Source: https://github.com/OpenHands/software-agent-sdk/blob/57374ec1…/openhands-sdk/openhands/sdk/llm/utils/metrics.py#L1-L330

**Tracking layers** (multi-LLM aggregation):

1. **Per-LLM**: `llm.metrics.accumulated_cost`, `llm.metrics.accumulated_token_usage.{prompt, completion, cache_read, cache_write, reasoning}_tokens`. Lifecycle: append-only lists `costs`, `token_usages`, `response_latencies` (no rotation, lives for the lifetime of the Python process).
2. **Per-conversation**: `conversation.conversation_stats` (a `ConversationStats`). Maps `usage_id → Metrics`. `get_combined_metrics()` aggregates totals across all LLMs in this conversation.
3. **Token budget**: `llm.metrics.max_budget_per_task` — gating capability. If a per-task budget is set, the agent refuses to continue once exceeded.
4. **Per-usage-id registration**: `LLMRegistry` is the SDK-wide handle. Each `LLM` carries a `usage_id` (e.g. `"agent"`, `"condenser"`) and is registered so condenser-LLM cost is separate from agent-LLM cost.

**What is *not* exported as a metric:**
- Number of tool calls (count available only via event log; SDK does not aggregate).
- Error / failure rate.
- Wall-clock per agent step (only per-LLM-call latency).
- Token rate / tokens-per-second.
- Browser session count.
- Active conversation count over time.
- Histograms of latency distribution (only per-call values retained).

**Cost calculation**: built-in `litellm.model_prices_and_context_window.json` covers major providers. Custom models accept `input_cost_per_token` / `output_cost_per_token` (`openhands-sdk/openhands/sdk/llm/llm.py`).

**Cache-hit rate**: `MetricsSnapshot.cache_hit_rate` at `metrics.py#L93-L109` is a derived 0–1 value handling OpenAI / ACP-style "cache within prompt" vs "cache outside prompt" denominators.

Reference doc: https://docs.openhands.dev/sdk/guides/metrics

### 4.4 Token / cost accounting summary

| Question | Answer | Source |
|---|---|---|
| Per-request token split (prompt / completion / cache / reasoning)? | **Yes** (model-dependent fields filled in when provider returns them) | `TokenUsage` |
| Per-conversation aggregate USD cost? | **Yes** (sums per-call `Cost.cost`) | `Metrics.accumulated_cost` |
| Per-user / per-usage-id breakdown? | **Yes** via `conversation.conversation_stats.usage_to_metrics` | `conversation_stats.py` |
| Budget cap (USD)? | **Yes** via `llm.metrics.max_budget_per_task` | `Metrics` |
| Real-time streaming as LLM streams? | **No**; cost finalised after each LiteLLM call completes | `LLM.completion()` pipeline |
| Currency support? | **USD only** (no FX conversion) | `Cost` |
| Long-term retention? | **No**; in-process memory only. Persist via `model_dump_json` of `conversation_state`. | `conversation_state.py` |
| Export formats? | `dict` / Pydantic JSON (`model_dump()`). No CSV / Prometheus text. | `Metrics.get()` |

### 4.5 Summary for FlowAI

- **Tracing**: out-of-the-box OTLP, hooks are first-class via `@observe`. Session grouping is by conversation UUID. `trace_id`/`span_id` are **not** injected into JSON logs by default — joins must use `session_id`/`conversation_id`.
- **Logging**: JSON structured. No `trace_id` field built in.
- **Metrics**: no Prometheus. You scrape conversation stats over the Agent Server REST API or read `llm.metrics` programmatically. **No time series; no latency histograms; only per-call values.**

---

## 5. Agent capabilities

### 5.1 Tool / function calling

Action/Observation/Executor triplet pattern, all Pydantic-typed. Bundled in the **`openhands-tools`** package (`openhands-tools/openhands/tools/`):

```
apply_patch/              # GPT-5 preset tool (ApplyPatchTool)
browser_use/              # Browser automation via browser-use/Patchright
delegate/                 # Sub-agent delegation (Task Tool Set)
file_editor/              # Default FileEditorTool (str_replace_editor style)
gemini/                   # Browser bridge (Gemini CLI flavor)
glob/                     # File pattern matching
grep/                     # ripgrep-style search
planning_file_editor/     # Editor variant for planning mode
preset/                   # Agent preset bundles (default, cli, planner, ...)
task/                     # Sub-agent / delegate tool
task_tracker/             # PLAN/TODO tracking
terminal/                 # BashTool (PTY-backed via libtmux)
tom_consult/              # Theory of Mind consult sub-agent
utils/
workflow/                 # Multi-step workflow tool
```

All tools follow the same `Action -> Observation -> Executor` pattern with built-in validation, error handling, and security classification (see §6).

### 5.2 Multi-agent coordination

- **`delegate` tool** / **`TaskToolSet`**: parent agent can delegate work to a sub-agent (TOM consult, critic, file-based agent) — `/sdk/guides/task-tool-set`.
- **Goal Completion Loop** (`convo-goal`): conversation runs autonomously toward a verifiable objective with a judge-driven refinement loop.
- **Iterative Refinement** (`iterative-refinement`): explicit critic-and-refine workflow.
- **Critic** (experimental): a separate LLM scores each step and triggers re-do loops.
- **Sub-agents are first-class**: spawned via `sub_agents_router` (`<.../agent_server/sub_agents_router.py>`).
- **Fork a conversation** (`convo-fork`): branch off an existing conversation for follow-up without contaminating the original.
- **Deferred init / warm-pool** (`agent-server/deferred-init`): pre-warm agent-server pods, then `POST /api/init` to activate — supports queueing workloads.
- **File-based agents** (`agent-file-based`): define specialized sub-agents as plain Markdown with YAML frontmatter, no Python required.
- **Plugins** (`plugins`): bundle skills + hooks + MCP servers + agents + commands into reusable packages.

### 5.3 MCP support

First-class via `fastmcp>=3.0.0`:

- **In-process MCP**: pass `mcp_config={"mcpServers": {...}}` to `Agent(...)`. Example used verbatim in docs: `{"mcpServers": {"fetch": {"command": "uvx", "args": ["mcp-server-fetch"]}}}`.
- **MCP via Agent Server**: `mcp_router.py` (29.6 kB) + `mcp_oauth_store.py` (11.5 kB) — full MCP management over HTTP, with OAuth store.
- **Tracing**: every MCP call is traced (`@observe(name="MCPToolExecutor.call_tool", span_type="TOOL")`).

Reference: https://docs.openhands.dev/sdk/arch/mcp; https://docs.openhands.dev/sdk/guides/mcp

### 5.4 Browser use

`browser_use` tool launched against **Patchright** (anti-detect Playwright fork). Headless Chromium is pre-installed and configured in the full container image. VNC + noVNC exposed on port `host_port+2` for visual replay; OpenVSCode Web on `host_port+1`. **rrweb** session replay is captured and forwarded to Laminar (only if OTEL backend is Laminar). Chromium flags `--no-sandbox --disable-dev-shm-usage --disable-gpu` to run as non-root.

Reference: https://docs.openhands.dev/sdk/guides/agent-browser-use; https://docs.openhands.dev/sdk/guides/browser-session-recording

### 5.5 Code execution sandboxing

The `terminal` (`BashTool`) runs commands in a **libtmux-wrapped PTY** inside whatever workspace the agent is configured with. Modes:

| Mode | Where code runs | How |
|---|---|---|
| **Local** | Same machine as the SDK | `LocalWorkspace` (not isolated) |
| **Sandboxed (Docker)** | Container spawned via `DockerWorkspace` using `ghcr.io/openhands/agent-server` | pre-built image, fast start |
| **Sandboxed (DockerDev)** | Container built on the fly via `DockerDevWorkspace` (uses `nikolaik/python-nodejs:python3.13-nodejs22-slim`) | image bake adds startup time |
| **Sandboxed (Apptainer)** | Rootless Apptainer container (HPC) | documented at `/sdk/guides/agent-server/apptainer-sandbox` |
| **API-managed** | Hosted runtime (`POST /api/init`) | `/sdk/guides/agent-server/api-sandbox`, `/sdk/guides/agent-server/cloud-workspace` |

`DockerDevWorkspace` can wrap a **custom base image** if pre-installed dependencies are needed. The `binary` Dockerfile target is recommended for production (smaller, same surface area).

### 5.6 Security / action confirmation

`openhands-sdk/openhands/sdk/security/`:
```
analyzer.py              # SecurityAnalyzerBase
risk.py                  # SecurityRisk: LOW|MEDIUM|HIGH|UNKNOWN (UNKNOWN excluded from comparisons)
llm_analyzer.py          # LLMSecurityAnalyzer (extra LLM call classifies risk)
grayswan/analyzer.py     # GraySwan third-party API integration
defense_in_depth/
  policy_rails.py        # policy DSL
  pattern_analyzer.py    # PatternSecurityAnalyzer (regex signatures)
ensemble.py              # EnsembleSecurityAnalyzer (composes multiple analyzers)
```

`SecurityAnalyzerBase` hooks before each tool execution; on `HIGH` / `MEDIUM` risk, the conversation can require user confirmation (`SetConfirmationPolicyRequest` exposed in the API). Multiple analyzers can be composed via `EnsembleSecurityAnalyzer`.

Reference: https://docs.openhands.dev/sdk/guides/security; https://docs.openhands.dev/sdk/arch/security

---

## 6. Integration surface

### 6.1 LLM provider support

**Universe**: 100+ providers via **LiteLLM** >= 1.84.1. Verified references in code:

- OpenAI, Anthropic, Google Gemini / Vertex, Azure OpenAI, AWS Bedrock, Groq, OpenRouter, Moonshot, DeepSeek, Fireworks, Together, Perplexity, xAI
- Local: Ollama, SGLang, vLLM, LM Studio (CPU/GPU server)
- OpenHands-hosted (`openhands/claude-sonnet-4-5-...`, `openhands/gpt-...`)
- **ChatGPT Plus/Pro subscriptions** via OAuth (`LLM.subscription_login()`) — documented at `/sdk/guides/llm-subscriptions`

**Configuration**:
- **Programmatic**: `LLM(model="anthropic/claude-sonnet-4-5-...", api_key=SecretStr("..."), base_url=..., temperature=..., timeout=...)`.
- **Environment variable convention**: `LLM_MODEL`, `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_USAGE_ID`, `LLM_TIMEOUT`, `LLM_NUM_RETRIES`, ... (`LLM_FIELD` -> `field`).
- **JSON**: serialize/load with `llm.model_dump_json(exclude_none=True)` / `LLM.load_from_json(...)`.
- **Custom costs**: `input_cost_per_token` / `output_cost_per_token`.

**Dual API path**:
1. **Chat Completions** (`completion()`) — standard path.
2. **Responses API** (`responses()`) — used for `gpt-5*` family (`gpt-5`, `gpt-5-mini`, `gpt-5-codex`). Pattern matching lives in `model_features.py`.

### 6.2 Protocol support

| Protocol | Support | Evidence |
|---|---|---|
| **MCP** | First-class in-process + remote via Agent Server | `mcp_router.py` + `mcp_oauth_store.py` |
| **ACP** (Agent Client Protocol) | First-class; SDK can *delegate* to ACP servers (Claude Code, Codex, Gemini CLI); or be wrapped as an ACP server for external clients (VSCode, Zed, Toad, JetBrains) | `agent-client-protocol>=0.10.1` dep; `/sdk/guides/agent-acp` |
| **OpenAI Chat Completions** | Compatible gateway (`/v1/chat/completions` exposed by Agent Server) | `<.../agent_server/openai/router.py>` |
| Custom REST | only FastAPI REST in agent_server |
| Custom gRPC | not provided |
| GraphQL | not provided |

### 6.3 Triggering agent runs

Three call paths:

1. **Python library (in-process)** — synchronous:
   ```python
   from openhands.sdk import LLM, Conversation, Tool
   from openhands.tools.terminal import TerminalTool
   agent = Agent(llm=LLM(model="..."), tools=[Tool(name=TerminalTool.name)])
   conv = Conversation(agent=agent, workspace=".")
   conv.send_message("echo hello")
   conv.run()
   ```
2. **Python library (async)** — `run_async()`, async/await, parallel conversations.
3. **HTTP / WebSocket (remote)**:
   - `POST /api/conversations` — start
   - `POST /api/conversations/{id}/run` (or implicit on `send_message`)
   - `WS /api/conversations/{id}/events/socket` — stream events
   - `POST /api/conversations/{id}/events` — inject mid-run (push message)
   - `POST /api/conversations/{id}/cancel` — interrupt
   - `POST /api/conversations/{id}/fork` — branch
   - `POST /api/conversations/{id}/goal/start` — goal-driven autonomous loop
   - `GET /api/conversations/{id}/events?page=N` — paginated event replay
   - OpenAI-compat: `POST /v1/chat/completions`

### 6.4 Webhooks / external triggers

`init_laminar_for_external()` (in `laminar.py#L410-L445` and `/sdk/guides/observability`) is the documented pattern for webhook integrations to capture parent span context. The CLI / Cloud products use this for GitHub, Slack, and Jira integrations; the SDK does not provide a built-in webhook *receiver*.

**However**, the **Agent Server itself** has a first-class `webhooks[]` config (see §3.10/§3.11): the server *pushes* event notifications to configured external HTTP endpoints with batching and retries. This is the natural handoff point for FlowAI's executor <-> router event flow if FlowAI's Docker Executor does not want to scrape `/events`.

---

## 7. Operational concerns (Docker-Executor view)

### 7.1 How the agent runtime is started

| Pattern | Startup | Use case |
|---|---|---|
| **Embedded library** | `pip install openhands-sdk openhands-tools` and `from openhands.sdk import Conversation` | In-process mode; minimal |
| **In-process server** | `python -m openhands.agent_server --host 127.0.0.1 --port 8000` (after `pip install` of all 4 packages) | Same machine, REST client |
| **Sandboxed container** | `DockerWorkspace(server_image="ghcr.io/openhands/agent-server:latest-python", host_port=8010, platform="linux/amd64\|arm64") as ws:` | Cleanest isolation; FlowAI's Docker-Executor target |

Reference: https://docs.openhands.dev/sdk/guides/agent-server/docker-sandbox

### 7.2 Lifecycle / state

`Conversation` exposes the standard execution status machine:

```python
# openhands-sdk/openhands/sdk/conversation/state.py:49-121
class ConversationExecutionStatus(str, Enum):
    IDLE = "idle"               # ready to receive tasks (not terminal)
    RUNNING = "running"         # actively processing
    PAUSED = "paused"           # user-paused (mid-loop intervention)
    WAITING_FOR_CONFIRMATION   # security analyzer gate
    FINISHED = "finished"       # terminal
    ERROR = "error"             # terminal
    STUCK = "stuck"             # terminal; stuck-detection heuristic
```

`STUCK` is computed by scanning the last `MAX_EVENTS_TO_SCAN_FOR_STUCK_DETECTION=20` events (`stuck_detector.py`).

**Persistence**: conversation state is Pydantic-serializable, persisted under `workspace/conversations/{id}/{metadata.json, events.jsonl}` by default (`persistence/` subpackage). State survives container restart (if the workspace directory is mounted). Encrypted values require `$OH_SECRET_KEY` stable across restarts.

**Pause/Resume**: `/convo-pause-and-resume`; users can interrupt mid-run and resume by serializing state and rehydrating.

**Mid-run messaging**: `conversation.send_message_while_running` injects context mid-loop without restarting.

**WebSocket reconnection**: `RemoteConversation` reconnects after WebSocket drop (`fix(sdk): reconnect remote conversation websocket #3987`, merged 2026-07-09).

### 7.3 Long-running tasks

- No hard time limit; conversation runs until `FINISHED` / `ERROR` / `STUCK` / external cancel.
- `MAX_EVENTS_TO_SCAN_FOR_STUCK_DETECTION=20` defines the stuck heuristic window.
- `StuckDetector` is documented separately (`agent-stuck-detector`).
- Pause/resume works across asyncio contexts (the implementation swaps `start_active_span` for `start_span + use_span` precisely for this — orphan-trace rate dropped from ~60% to 0%; `laminar.py#L246-L254`).

### 7.4 Graceful shutdown

- Process entrypoint: `tini -- /usr/local/bin/openhands-agent-server` (binary mode) or `tini -- /agent-server/.venv/bin/python -m openhands.agent_server` (source mode). `tini` reaps zombies and forwards SIGTERM/SIGINT.
- `Conversation.close()` flushes state to persistence and triggers cleanup.
- `with DockerWorkspace(...) as ws:` is the context-manager idiom — `__exit__` stops the container.
- `Workspace.pause()` / `Workspace.resume()` for explicit pause without losing state.
- No native `cancel_after_timeout` for the server itself — wrap in a pod with `activeDeadlineSeconds` if running on K8s.

### 7.5 Resource footprint considerations

For FlowAI's Docker Executor, the agent-server image is a **heavy base** (Chromium + XFCE + DinD + VSCode + VNC + Node 22 + Python). Strategies:

- Use `DockerDevWorkspace` with a **leaner base image** (e.g., `python:3.13-slim`) for eval/CI workloads.
- Use `DockerWorkspace(server_image="...:binary-minimal")` for production headless loops (no VNC / Docker / VSCode).
- Network surface per container: port `8000` (agent API), plus optional `host_port+1` (VSCode Web), `+2` (noVNC), `+3` (desktop service).
- `mcp_oauth_store.py` keeps OAuth stateful tokens -> workspace volume must be persistent if you want to retain them across restarts.

---

## 8. Quick reference: file map

| Concern | Path (relative to repo root, commit `57374ec1`) |
|---|---|
| SDK source packages | `openhands-sdk/`, `openhands-tools/`, `openhands-workspace/`, `openhands-agent-server/` |
| LLM abstraction | `openhands-sdk/openhands/sdk/llm/llm.py` |
| Metrics (Pydantic) | `openhands-sdk/openhands/sdk/llm/utils/metrics.py` |
| Conversation state machine | `openhands-sdk/openhands/sdk/conversation/state.py` |
| Stuck detector | `openhands-sdk/openhands/sdk/conversation/stuck_detector.py` |
| Events | `openhands-sdk/openhands/sdk/event/` |
| Tools | `openhands-tools/openhands/tools/` |
| Security analyzers | `openhands-sdk/openhands/sdk/security/` |
| Observability (Laminar / OTEL) | `openhands-sdk/openhands/sdk/observability/laminar.py` |
| Logger (JSON / Rich) | `openhands-sdk/openhands/sdk/logger/logger.py` |
| MCP client | `openhands-sdk/openhands/sdk/mcp/tool.py` |
| Hooks / lifecycle | `openhands-sdk/openhands/sdk/hooks/executor.py` |
| Docker / Apptainer workspaces | `openhands-workspace/openhands/workspace/` |
| Agent Server (REST + WS) | `openhands-agent-server/openhands/agent_server/` |
| REST routers | `*_router.py` in `agent_server/` |
| OpenAI-compat gateway | `agent_server/openai/router.py` |
| Persistence | `agent_server/persistence/` |
| Dockerfile | `openhands-agent-server/openhands/agent_server/docker/Dockerfile` |
| Image build helper | `openhands-agent-server/openhands/agent_server/docker/build.py` |
| Image CI workflow | `.github/workflows/server.yml` |
| Docs site | `docs.openhands.dev/sdk` |
| PyPI | `pypi.org/project/openhands-sdk/` |

---

## 9. Known gaps / caveats (for honesty, so alternatives can score them)

1. **No Prometheus metrics endpoint.** Tokens / cost / latency live in process memory; persistence is opt-in via Pydantic dump. No statsd, no OTel metrics.
2. **No `trace_id`/`span_id` in JSON logs.** Cross-correlation between OTel traces and stdout JSON requires the consumer to extract `session_id`/`conversation_id` from message bodies, or to install a custom logging filter that reads OTel context. This is a deliberate gap — the SDK ships a JSON formatter and a separate OTel pipeline but does not stitch them.
3. **Browser session replays require Laminar.** With any other OTLP backend, `BROWSER_USE_SESSION/PATCHRIGHT/PLAYWRIGHT` instruments are explicitly disabled (`laminar.py#L107-L115`).
4. **Sampling is delegated entirely to the OTLP collector.** No SDK-side sampler.
5. **PyInstaller fragility.** Build requires Python 3.13 with a specific `python-build-standalone` (>= 20260408). Earlier interpreters trigger glibc/DinD-load issues (Dockerfile lines 22–32). Refer to SDK issue #2761.
6. **No GraphQL, no gRPC.** REST + WebSocket only.
7. **Workspace directory is in-container** by default (`/workspace`). Your Docker Executor must mount a writable volume to retain conversation state across container restarts.
8. **Encrypted-secret recovery requires `$OH_SECRET_KEY` to be persistent.** A key rotation permanently loses previously stored secrets. The Agent Server explicitly warns about this in its README and refuses to operate silently in that case.
9. **Default bind is `0.0.0.0:8000`.** Must be overridden to `127.0.0.1` or kept behind TLS in production. The README of `OpenHands/OpenHands` and `sdk/arch/agent-server.md` both call this out.
10. **Heavy full image.** The full variant bundles Chromium, XFCE, VNC, VSCode Server, DinD — useful for `browser-use` but oversized for headless agent workloads; use the `*-minimal` target.
11. **License policy**: the SDK is **MIT**, but LiteLLM's `model_prices_and_context_window.json` is third-party data baked into LiteLLM; the `vertex` optional extra pulls a Google SDK; bundled `apt` packages carry their own licenses.
12. **Image size on pull**: not measured in this report (no `read:packages` scope). Any alternative that wants to claim smaller size on disk has a low bar.
13. **V0 -> V1 migration**: the legacy `OpenHands/OpenHands` repo is deprecated; some blog posts / community answers still link to V0 code paths. Pin your dependency and your docs to the V1 SDK explicitly.

---

*Report generated 2026-07-09 against commit `57374ec1bbdcb3db5cb806219c3cb4d8b6684f74` of `OpenHands/software-agent-sdk`. All permalinks above resolve to that commit or to the live `main` branch as of the report date; downstream readers should re-pin their references to their actual production pin.*
