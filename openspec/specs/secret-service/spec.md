## Purpose

Define the Secret Service as FlowAI's credential store with exactly two surfaces: `store` for writes proxied by the API Gateway and `open env` for Executor reads at task start.

## Requirements

### Requirement: Secret Service stores encrypted secrets through the write surface
The Secret Service SHALL accept `POST /secrets` writes proxied by the API Gateway, validate input, encrypt values at rest with a KMS-backed data key, persist ciphertext and metadata, write an audit-log entry, and return a secret identifier.

#### Scenario: Operator creates a secret
- **WHEN** the API Gateway proxies a valid secret write from the Web UI
- **THEN** the Secret Service stores ciphertext and returns a secret identifier

### Requirement: Secret Service opens environment values only for executors
The Secret Service SHALL expose scoped env-style `KEY=value` secret values only to Executors presenting valid HMAC-signed, project-bound, unexpired scope tokens.

#### Scenario: Executor starts a task
- **WHEN** an Executor presents a valid scope token at task start
- **THEN** the Secret Service returns env-style values for that task scope

#### Scenario: Scope token is checked
- **WHEN** an Executor requests open-env values
- **THEN** the Secret Service validates the scope signature, expiry, and project binding before returning any value

### Requirement: Secret Service remains independent and non-routing
The Secret Service SHALL NOT be called directly by the Web UI or Automation, SHALL NOT call the Event Router or State Store, SHALL NOT participate in business logic, and SHALL NOT return plaintext secret values to the API Gateway.

#### Scenario: Web UI needs to store a secret
- **WHEN** the Web UI submits secret data
- **THEN** the Web UI reaches the Secret Service only through the API Gateway proxy path

### Requirement: Secret Service protects stored and logged secret material
The Secret Service SHALL NOT modify stored secrets in place, SHALL NOT log plaintext secret values, and SHALL record only secret identifiers and audit metadata in logs.

#### Scenario: Secret value changes
- **WHEN** a stored secret value needs to change
- **THEN** the Secret Service represents the change as a new secret version rather than mutating plaintext in place

#### Scenario: Secret access is logged
- **WHEN** the Secret Service records an audit event
- **THEN** the log contains identifiers and metadata, not plaintext secret values

### Requirement: Secret Service does not own caller rate policy
The Secret Service SHALL NOT enforce rate limiting on its own; any rate policy belongs to the caller or an upstream layer.

#### Scenario: Caller rate policy exists
- **WHEN** a caller needs rate limiting for secret operations
- **THEN** that policy is enforced outside the Secret Service
