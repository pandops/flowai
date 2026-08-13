## ADDED Requirements

### Requirement: Executors use unauthenticated backend HTTP

Every concrete Executor SHALL call State Registry over plain HTTP without a
client certificate, private key, CA bundle, authorization credential, or
certificate-derived service identity. It SHALL preserve the existing
registration lifecycle and take `scope`, optional `team_id`, and
`authorized_tag` from its configuration. This change SHALL NOT alter how an
Executor ID is allocated or persisted; that planned registration redesign
belongs to `v0005-executor-k8s`. It SHALL NOT treat `executor_id` as a
credential.
Legacy State Registry TLS/mTLS configuration fields SHALL be accepted and
ignored without reading the referenced files.

#### Scenario: Team-owned Executor starts without certificate files

- **WHEN** a Docker or K8s Executor starts with State Registry `http://` URL, `scope = team`, configured `team_id`, one tag, and no certificate files
- **THEN** it registers with the existing Executor ID lifecycle and proceeds using persisted scope rules

#### Scenario: System-owned Executor starts without team

- **WHEN** a Docker or K8s Executor starts with `scope = system`, no `team_id`, one tag, and no certificate files
- **THEN** it registers over HTTP and may discover eligible matching-tag tasks across teams

#### Scenario: Legacy mTLS fields remain configured

- **WHEN** legacy certificate, key, CA, or TLS fields contain missing or invalid paths
- **THEN** the Executor ignores them, performs no filesystem read for those paths, and readiness depends on HTTP connectivity rather than certificate validity

### Requirement: Executors preserve configured ownership without transport identity

For team scope, every concrete Executor SHALL keep its configured immutable
`team_id` in registration, cached assignments, labels, events, controls, and
environment-open requests. For system scope, it SHALL omit registration
`team_id` and use each claimed task's canonical `team_id` for task-scoped
operations. State Registry rejection of inconsistent canonical data SHALL stop
the affected operation. No transport identity SHALL be used as authorization
evidence.

#### Scenario: Team-owned task data contradicts configured team

- **WHEN** a team-owned Executor receives task, event, control, token, or environment data whose `team_id` differs from its configured binding
- **THEN** it rejects the data locally and starts no new runtime

#### Scenario: Executor reconnects after restart

- **WHEN** an Executor restarts with the same persistent cache and configured scope
- **THEN** it uses its persisted existing `executor_id`, refreshes over HTTP, and reconciles its cached assignments without certificate identity

## REMOVED Requirements

### Requirement: Docker Executor uses its registered scope as its authorization identity

**Reason**: Scope remains required, but it is configuration and persisted Registry data rather than an authenticated transport identity.

**Migration**: Apply the `Executors preserve configured ownership without transport identity` requirement and remove certificate-based language and client setup.
