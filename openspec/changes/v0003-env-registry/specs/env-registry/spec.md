## ADDED Requirements

### Requirement: Env Registry stores executor environment definitions through the write surface

The Env Registry SHALL accept environment-definition writes, validate input, store non-secret environment variables, encrypt secret values at rest with a KMS-backed data key, persist ciphertext and metadata for secrets, write an audit-log entry, and return environment or secret identifiers.

#### Scenario: Executor environment data is created

- **WHEN** a valid executor environment write reaches the Env Registry
- **THEN** the Env Registry stores non-secret values as environment metadata, stores secret values as ciphertext, and returns identifiers for the stored entries

### Requirement: Env Registry opens environment values only for executors

The Env Registry SHALL expose scoped env-style `KEY=value` values only to Executors presenting valid HMAC-signed, project-bound, unexpired scope tokens, and the response SHALL include the task's authorized non-secret environment variables plus decrypted secret values.

#### Scenario: Executor starts a task

- **WHEN** an Executor presents a valid scope token at task start
- **THEN** the Env Registry returns env-style values for that task scope and records a local audit entry for the open-env access

#### Scenario: Scope token is checked

- **WHEN** an Executor requests open-env values
- **THEN** the Env Registry validates the scope signature, expiry, and project binding before returning any value

### Requirement: Env Registry remains independent and non-routing

The Env Registry SHALL NOT participate in scheduling, SHALL NOT call a scheduler or State Registry, SHALL NOT participate in business logic, and SHALL NOT return plaintext secret values to any caller other than an authorized Executor open-env request.

#### Scenario: A caller stores executor environment data

- **WHEN** executor environment or secret data is submitted
- **THEN** the Env Registry handles storage without calling the State Registry or scheduler

### Requirement: Env Registry protects stored and logged secret material

The Env Registry SHALL NOT modify stored secrets in place, SHALL NOT log plaintext secret values, and SHALL record only secret identifiers and audit metadata in logs.

#### Scenario: Secret value changes

- **WHEN** a stored secret value needs to change
- **THEN** the Env Registry represents the change as a new secret version rather than mutating plaintext in place
