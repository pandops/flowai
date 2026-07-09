# Design: State Registry

## Runtime Shape

- State Registry owns canonical task records, event logs, audit trail, and control-request records.
- It persists records in PostgreSQL.
- It serves direct reads/writes before API Gateway exists.

## Boundaries

- State Registry does not call Env Registry or any scheduler.
- State Registry does not store env values or secret values.

## Proposed Diagrams

- `specs/diagrams/01-state-registry-topology.puml`
- `specs/diagrams/02-events-controls-flow.puml`
