# Change: 007 Auth

## Why

After the Web UI and API Gateway operator surface exists in no-auth mode, FlowAI needs authentication and bearer-token enforcement for operator traffic.

## What Changes

- Add login/token issuance and token refresh behavior.
- Require bearer-token validation on Web UI REST requests and WebSocket upgrades.
- Update Web UI to authenticate and attach bearer tokens.
- Keep API Gateway business-logic-free and still disconnected from Router and Executors.

## Impact

- Operator flows from `0006-web-ui` become authenticated.
- Gateway rejects unauthenticated operator requests before proxying.

## Non-Goals

- Change Router task submission topology.
- Add persistent platform state to API Gateway.
- Make Web UI call services other than API Gateway.
