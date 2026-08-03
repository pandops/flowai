# Design: Env Registry

## Runtime Shape

- Env Registry stores executor environment definitions and encrypted secret values.
- Executors use open-env with signed scope tokens to get env-style `KEY=value` values.
- Env Registry persists metadata, ciphertext, and local audit entries in PostgreSQL.

## Boundaries

- Env Registry does not call State Registry or scheduler.
- Plaintext secret values are returned only to authorized Executor open-env calls.

## Proposed Diagrams

- `specs/diagrams/01-env-registry-topology.puml`
- `specs/diagrams/02-store-open-env-flow.puml`
