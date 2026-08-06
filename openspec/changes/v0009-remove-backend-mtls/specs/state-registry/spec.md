## ADDED Requirements

### Requirement: State Registry accepts backend HTTP without service authentication

State Registry SHALL expose its backend HTTP and WebSocket APIs without TLS,
client certificates, service credentials, or certificate-derived identity.
No State Registry endpoint SHALL require an authenticated listener, Executor,
Gateway, or administrator service identity. Where existing requirements use
the phrase authenticated service or authenticated Executor, this change SHALL
supersede that phrase with resolution of submitted identifiers against
canonical Registry records and the scope predicates defined for that endpoint.
An `executor_id`, `team_id`, `source_system_id`, Gateway context header, or
admin request field SHALL be treated as request data and SHALL NOT be described
or implemented as authentication proof.

#### Scenario: Backend caller uses HTTP without credentials

- **WHEN** an Executor, listener, or API Gateway calls its State Registry route over HTTP without a client certificate or authorization credential
- **THEN** State Registry processes the request using normal schema, existence, scope, ownership, assignment, lifecycle, and ordering validation and does not reject it for missing service authentication

#### Scenario: Backend caller presents a certificate

- **WHEN** a caller sends certificate-related metadata or legacy TLS configuration remains present
- **THEN** State Registry derives no identity or authority from it and does not load or validate client certificate material

### Requirement: State Registry trusts Gateway context without authenticating Gateway

State Registry SHALL accept Gateway-mediated operator and admin context as
forwarded request data without authenticating the Gateway connection. Gateway
SHALL remain responsible for user authentication. Network enforcement that
prevents callers from bypassing Gateway or spoofing Gateway/admin context SHALL
remain outside State Registry and outside this change.

#### Scenario: Gateway forwards authenticated operator context

- **WHEN** API Gateway authenticates an operator and forwards canonical `operator_id`, `team_id`, and `request_id` over backend HTTP
- **THEN** State Registry applies its existing team filters and audit rules without requiring a Gateway client certificate

#### Scenario: Direct caller copies Gateway context

- **WHEN** a network peer directly submits syntactically valid Gateway or admin context
- **THEN** State Registry performs no service-authentication check, and prevention of that route is delegated to deployment network controls outside this change

### Requirement: State Registry accepts configured listener ownership

State Registry SHALL accept listener task ingestion directly over HTTP without
service authentication. The request's configured `team_id` and
`source_system_id` SHALL be validated for existence, immutable relationship,
payload consistency, deduplication, and task-type rules, but SHALL NOT be
compared with an authenticated listener identity.

#### Scenario: Listener ingests with configured ownership

- **WHEN** a listener directly submits a valid task with an existing related `team_id` and `source_system_id`
- **THEN** State Registry persists or deduplicates it under the submitted canonical ownership without requiring listener credentials

## MODIFIED Requirements

### Requirement: Executors register one tag and either team-owned or system-owned scope

Each Executor SHALL register one ownership `scope` chosen from `{team,
system}` using the existing `PUT /v1/executors/{executor_id}` lifecycle. This
change SHALL NOT adopt the Registry-generated first-registration lifecycle
planned by `v0005-executor-k8s`. The request body SHALL carry neither
`executor_id` nor `identity`.
For `scope = team`, it SHALL carry configured non-null `team_id`; State
Registry SHALL verify the team exists and persist the immutable binding without
authenticating the caller. For `scope = system`, it SHALL omit `team_id`, and
State Registry SHALL persist NULL. Both scopes SHALL register exactly one tag,
Executor type, capacity observations, and metadata. State Registry SHALL
resolve the path ID to the canonical row on refresh, reject changes to
scope/team/tag, and SHALL NOT treat the ID as a credential. Registration SHALL
NOT create or update a team.

#### Scenario: Team-owned Executor registers configured team

- **WHEN** an unauthenticated Executor puts its existing Executor ID with `scope = team`, an existing configured `team_id`, one tag, type, capacity observations, and metadata
- **THEN** State Registry persists and returns the immutable team binding without deriving identity from transport

#### Scenario: System-owned Executor registers without team

- **WHEN** an unauthenticated Executor puts its existing Executor ID with `scope = system`, omits `team_id`, and supplies one tag, type, capacity observations, and metadata
- **THEN** State Registry persists NULL `team_id` and uses tag-based cross-team eligibility

#### Scenario: Registration shape is invalid

- **WHEN** team scope omits `team_id`, system scope includes `team_id` including null, the team is unknown, or the tag count is not one
- **THEN** State Registry rejects the request without partial persistence

### Requirement: State Registry uses Ingress-terminated transport

State Registry SHALL expose plain HTTP and WebSocket transport to backend
services. External HTTPS SHALL terminate at Ingress. State Registry SHALL NOT
configure a backend HTTP TLS listener, request client certificates, or load
backend HTTP certificate/key/CA files. Legacy backend HTTP TLS/mTLS
configuration fields SHALL be accepted and ignored. The State Registry
database client SHALL continue to use its configured secure PostgreSQL
connection and SHALL validate the database server identity and certificate
chain.

#### Scenario: HTTP backend and secure database coexist

- **WHEN** State Registry starts with an HTTP listener, valid secure PostgreSQL settings, and legacy HTTP mTLS fields
- **THEN** it ignores the legacy fields without reading their paths, serves backend HTTP without TLS, and connects to PostgreSQL with server certificate verification

#### Scenario: PostgreSQL certificate is invalid

- **WHEN** the backend HTTP listener is healthy but PostgreSQL presents an invalid server identity or chain
- **THEN** State Registry fails the database connection according to the existing secure-database policy

## REMOVED Requirements

### Requirement: State Registry enforces team-bound service identities with conditional Executor binding

**Reason**: Backend service authentication and certificate-bound authorization are removed; canonical records and submitted configuration provide scope data.

**Migration**: Remove service-identity middleware and certificate bindings; retain endpoint-level team, source, tag, assignment, FIFO, and lifecycle validation.
