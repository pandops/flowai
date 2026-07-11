# ADR-0003: v0001 Executor runs the OpenHands agent runtime

- **Status**: Accepted
- **Date**: 2026-07-09
- **Change**: v0001-executor-docker

## Context

The Executor must run an agent runtime inside its container. The agent runtime
provides: LLM integration, tool execution, browser automation, code sandboxing,
and event streams.

The Executor is image-agnostic (it pulls whatever OCI image is configured) but
the v0001 sample task fixture needs a concrete choice. The runtime chosen here
defines the integration surface (REST API, event format, env vars).

## Decision

v0001 ships the **OpenHands agent runtime**, using the official image
`ghcr.io/openhands/agent-server:latest-python`. The Executor integrates via
OpenHands' REST/WebSocket API on container port 8000.

## Rationale

- **Production-grade agent runtime**: OpenHands is a mature, actively-maintained
  open-source agent platform with a Dockerized server distribution.
- **First-class Docker integration**: OpenHands ships `DockerWorkspace` and the
  `ghcr.io/openhands/agent-server` image specifically for sandboxed agent
  execution — the exact use case we have.
- **Built-in observability**: OpenHands emits OpenTelemetry traces out of the
  box (Laminar by default; any OTLP-compatible backend via env vars). The
  Executor does not need to add observability on top.
- **REST/WebSocket server in each task container**: OpenHands runs as a
  long-lived HTTP server per accepted task container. The Executor submits
  tasks via REST and streams events back via WebSocket.
- **Familiar contract**: documented REST API, conversation/task model, and
  status endpoints. Less integration risk than a less-known runtime.
- **Interrupt and message injection surface is built-in**: OpenHands exposes a
  pause endpoint (`POST /api/conversations/{conversation_id}/pause`) and an
  event-creation endpoint (`POST /api/conversations/{conversation_id}/events`)
  that the Executor forwards to. Both endpoints are stable in OpenHands V1
  (`OpenHands/software-agent-sdk`, MIT, v1.31.0+).

## Consequences

Positive:

- The Executor has a real, production-grade agent inside, not a stub.
- Observability comes for free via OTel — no Executor-side instrumentation
  needed.
- Future changes can swap in real Router, State Registry, Env Registry without
  touching OpenHands integration.
- The OpenHands pause and event-creation endpoints are built-in and documented,
  making interrupt and message injection straightforward.

Negative:

- **Image size**: the `agent-server` image is several GB; cold start is
  non-trivial. Acceptable for MVP since the container stays warm.
- **OpenHands API coupling**: if OpenHands changes its REST API, the Executor
  must adapt. Mitigated by wrapping the OpenHands client behind an internal
  interface AND by making the endpoint paths configurable
  (`OPENHANDS_INTERRUPT_ENDPOINT`, `OPENHANDS_MESSAGE_ENDPOINT`) so other
  agent runtimes can be substituted.
- **OpenHands version drift**: `:latest-python` is floating; pinning to a digest
  is recommended for production but defer to a follow-up change.

## Integration surface used by the Executor

The Executor calls OpenHands via three documented endpoint families (all under
`/api/`):

| Purpose | Default endpoint | Config key |
|---|---|---|
| Health check | `GET /health` | (hardcoded) |
| Pause / interrupt a conversation | `POST /api/conversations/{conversation_id}/pause` | `OPENHANDS_INTERRUPT_ENDPOINT` |
| Inject a message into the agent's event queue | `POST /api/conversations/{conversation_id}/events` (event of type `message`, role `user`) | `OPENHANDS_MESSAGE_ENDPOINT` |

Authentication: `X-Session-API-Key` header on every call. Configured via
`OPENHANDS_API_KEY`. Optional in v0001 if the OpenHands server is started
without auth (development only).

These endpoints are pinned against the OpenHands V1 server
(`OpenHands/software-agent-sdk` ≥ v1.31.0). The endpoint paths are stable but
the Executor should be tested against the actual OpenHands server version
chosen at deployment time.

## Alternatives considered

- **Claude Agent SDK** (`@anthropic-ai/claude-agent-sdk`): Excellent OTel
  integration via Claude Code CLI. Requires a custom Docker image with Claude
  Code installed. Deferred to a separate Executor per the "one Executor = one
  runtime" decision.
- **LangChain / LangGraph**: Library you embed, not a server-in-a-container.
  Doesn't fit the Executor's container-orchestration model without a custom
  image.
- **Pydantic AI + Logfire**: Best OTel coverage, but Python-only library; needs
  custom container.
- **alpine:3.19 with `echo`**: Earlier ADR draft. Useful for testing Executor
  mechanics but doesn't exercise any real agent observability. Replaced by
  OpenHands for v0001.
- **Smolagents (HuggingFace)**: Lightweight but observability story is weak;
  not a fit if observability is on the must-have list.

## References

- v0001-executor-docker design.md "Components" and "Router Task List Contract".
- OpenHands docs: <https://docs.openhands.dev/sdk>
- OpenHands software-agent-sdk: <https://github.com/All-Hands-AI/OpenHands>