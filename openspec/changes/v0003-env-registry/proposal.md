# Change: v0003-env-registry

## Why

FlowAI needs a dedicated executor environment store after historical state exists. Env Registry owns non-secret env vars and encrypted secrets independently from State Registry and scheduling.

## What Changes

- Add Env Registry store surface for executor environment definitions.
- Add open-env surface for authorized Executor reads.
- Encrypt secret values at rest and record local audit entries.

## Impact

- Executors can receive scoped task environment values without Web UI/API Gateway involvement.
- Secret material stays out of State Registry and Router.

## Non-Goals

- Implement Web UI forms or API Gateway proxying.
- Store task history.
- Queue or dispatch tasks.
