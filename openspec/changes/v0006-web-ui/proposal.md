# Change: v0006-web-ui

## Why

After backend services and executors exist, FlowAI needs an operator surface for reading task history, watching events, writing executor environment data, and recording control requests. This change intentionally omits authentication so the UI/proxy surface can be built before auth hardening.

## What Changes

- Add Web UI as the operator-facing frontend.
- Add API Gateway as the Web UI's only backend in no-auth mode.
- Proxy state reads, live-event streams, and control requests to State Registry.
- Proxy env/secret writes to Env Registry.

## Impact

- Operators use Web UI instead of direct service calls for normal workflows.
- Auth is explicitly deferred to `v0007-auth`.

## Non-Goals

- Validate bearer tokens or implement login.
- Make API Gateway talk to Router or Executors.
- Persist platform state in Web UI or API Gateway.
