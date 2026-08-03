# Delta for state-registry

This delta modifies the v0002 spec to swap the secret-at-rest
cryptography provider. v0008 preserves every public wire shape,
lifecycle invariant, FIFO ordering rule, HMAC scope-token
verification, team authorization rule (including `scope = system`
support), image-resolution rule, and tenancy invariant that v0002
already ships.

The OpenBao 2.6 Transit API at
<https://openbao.org/api-docs/secret/transit/> fixes several facts
that govern this delta:

- `POST /v1/<mount>/encrypt/<key_name>` and
  `POST /v1/<mount>/decrypt/<key_name>` accept `associated_data`
  and authenticate it like a nonce. v0008 uses these to bind
  the canonical `team_id`, `logical_secret_id`, `version` triple.
- `POST /v1/<mount>/rewrap/<key_name>` accepts only `ciphertext`,
  `context`, `key_version`, `nonce`, `reference`, `batch_input`.
  It does NOT accept `associated_data`. v0008 therefore does NOT
  use rewrap; it rotates only through
  `POST /v1/<mount>/keys/<key_name>/rotate`.
- `POST /v1/<mount>/keys/<key_name>/rotate` creates a new
  latest key version. The rotate call does NOT itself raise
  `min_encryption_version` or `min_decryption_version`.
- v0008 reads the canonical key's latest version through
  `GET /v1/<mount>/keys/<key_name>`. The documented default
  ciphertext shape is `vault:v<N>:...`; the official OpenBao
  2.6 public API does not expose a configurable
  `version_template`, so v0008 pins the implementation to that
  documented default and validates the parsed shape strictly.
- `min_decryption_version` and `min_encryption_version` are
  independent floors set out of band through
  `POST /v1/<mount>/keys/<key_name>/config`.

The delta has three sections. The `### Requirement:` headers
in `## MODIFIED Requirements` are preserved verbatim because
contract tools and tests use them as stable identifiers; only
the body text changes. Each body below is the authoritative
contract.

## MODIFIED Requirements

### Requirement: State Registry uses local AES-256-GCM authenticated encryption for secret values

The State Registry SHALL encrypt every stored secret value
through a pluggable in-process crypto provider whose active
implementation is the OpenBao Transit provider. The opaque
public `key_id` and `key_version` fields recorded on every
`secret_versions` row remain the contract surface and the
only fields v0002 already exposes; every value v0008 reads
from or writes to the active provider SHALL be transported as
the opaque `SecretVersion.key_id` (string of 1 to 64
characters matching `^[A-Za-z0-9][A-Za-z0-9._:-]*$`) and
`SecretVersion.key_version` (`uint >= 1`). The public `key_id`
SHALL be a provider-neutral opaque identifier chosen at
deployment time and resolved to a `(mount_path, key_name)`
pair through the secure runtime configuration; the public
`key_id` SHALL never expose `key_name`, the Transit mount
path, the Transit namespace, the Transit ciphertext form, the
active provider's nonce format, or its authentication tag
format. The Transit ciphertext form documented at
<https://openbao.org/api-docs/secret/transit/> is internal-only;
v0008 pins the implementation to the documented default
shape `vault:v<N>:...` (where `<N>` is the embedded key
version) and fails startup or operation if the provider
returns ciphertext that does not match that shape after a
strict parse. The active provider SHALL encrypt and decrypt
only with a server-controlled `associated_data` value that
canonical-binds team identifier, logical secret identifier,
and secret version; missing, mismatched, or reordered bindings
SHALL cause every provider decrypt operation to fail closed
before any plaintext leaves the Registry. v0008 SHALL bind
`associated_data` to exactly the canonical
`(team_id, secret_id, version)` triple; the State Registry
SHALL NOT add a `provider_marker` field to `associated_data`
because the routing decision for each row is taken from the
persisted `crypto_provider` column on `secret_versions`,
not from any authenticated binding. The Registry SHALL refuse
decryption when the active provider reports a config error,
missing key, missing mount, missing version, ACL denial,
tampered ciphertext, or wrong `associated_data`; startup
SHALL fail closed when the active provider cannot be reached
or cannot be authorized; thereafter, secret writes and
open-environment reads SHALL fail closed on every provider
error. Application logs, audit records, and error responses
SHALL NOT contain plaintext secret values, decrypted
environment values, `associated_data` values, ciphertext
bytes, nonce bytes, authentication tag bytes, key material,
derived key bytes, the Transit token, the Transit endpoint or
namespace, the Transit mount path, the Transit `key_name`,
or any raw provider error body; identifier-level metadata
(`team_id`, `secret_id`, `version`, opaque `key_id`, opaque
`key_version`, operation name, outcome status class,
`request_id`) MAY be recorded under the same audit rule v0002
already carries. The State Registry SHALL NOT depend on the
application code path for cryptographic correctness beyond
calling the active provider at the documented boundaries; the
provider abstraction is the only seam a future change may
swap without disturbing this contract. v0008 ships the OpenBao
Transit implementation as the active provider and ships a
local provider implementation whose sole role is the bounded
migration window documented under "State Registry migrates
local-at-rest ciphertext to the active provider through a
resumable per-(team_id, secret_id, version) reconciliation
owned durably by State Registry in PostgreSQL"; the local
provider SHALL never handle a write after the deployment
procedure accepted under "State Registry disables the local
provider only after every row is verified".

#### Scenario: Encryption binds team, secret, and version in associated data

- **WHEN** the State Registry encrypts a secret value through
  the active crypto provider
- **THEN** the `associated_data` SHALL bind the team
  identifier, the logical secret identifier, and the secret
  version with the canonical byte string; the routing
  decision SHALL be taken from the persisted
  `crypto_provider` column on `secret_versions`, not from
  any field in `associated_data`; the local provider SHALL
  use the v0002 legacy byte format and the active provider
  SHALL use the v0008 active byte format for the same
  canonical input triple; the resulting ciphertext SHALL
  be rejected on provider decrypt if any of the canonical
  bindings do not match the persisted records

#### Scenario: Decryption is denied before any plaintext leaves the Registry

- **WHEN** an authorized open-environment request is in
  progress
- **THEN** the State Registry SHALL decrypt using the active
  crypto provider under the same canonical authorization
  order v0002 prescribes, perform every canonical
  authorization check before returning plaintext, and never
  log plaintext, `associated_data` values, ciphertext bytes,
  nonce bytes, authentication tag bytes, key material, or
  derived key bytes; the public response body of a
  successful open contains only the authorized env-style
  values and the documented `OpenEnvironmentResponse` fields

#### Scenario: Logs and audit never contain secret material

- **WHEN** State Registry encrypts, decrypts, accepts,
  rejects, or retries any secret write or open-environment
  read
- **THEN** application logs, audit entries, and error
  responses SHALL contain no plaintext, no key material, no
  nonce bytes, no ciphertext bytes, no authentication tag
  bytes, no `associated_data` values, no individual claim
  values, no derived key bytes, no Transit token, no Transit
  endpoint, namespace, mount path, key name, or raw Transit
  error body; identifier-level metadata MAY be recorded
  under the same rule v0002 already carries

### Requirement: State Registry stores team-owned secrets with immutable versions

The State Registry SHALL store each logical secret in a
team-owned `secrets` row associated with an environment in the
same team. It SHALL encrypt every submitted secret value
through the active crypto provider and SHALL record the opaque
public `key_id` and `key_version` on the immutable
`secret_versions` row; plaintext is never persisted. Every
write records `secret_versions.crypto_provider = 'transit'`
in the same transaction. The logical secret identity, the
`secret_id`, the `version` integer, the team ownership
(inherited from the same-team `secrets` parent row), and the
plaintext meaning of the version SHALL be immutable for the
lifetime of the row; only the cryptographic envelope columns
(`ciphertext`, `key_id`, `key_version`, `crypto_provider`,
`migrated_at`) MAY be rewritten, and they SHALL be rewritten
only under the audited compare-and-swap envelope-rewrite
rules established by "State Registry migrates local-at-rest
ciphertext to the active provider through a resumable per-
(team_id, secret_id, version) reconciliation owned durably by
State Registry in PostgreSQL" and by "State Registry rotates
new writes to a new Transit key version while retaining
historical AAD-bound key versions". No public HTTP endpoint or
operator API SHALL mutate those envelope columns; the only
authorized writers are the in-process migration worker and
the in-process rotate procedure inside the existing
`state-registry` deployment boundary. Concurrent writers
SHALL see either the complete old envelope or the complete
new envelope of any `secret_versions` row, never a mixed
intermediate state, because each envelope rewrite runs in
exactly one transaction that takes a row-level lock on the
targeted row. Same-team operators manage secrets through
trusted API Gateway context; optional project/task scope
limits applicability within the team. A `secrets` row
inherits its scope from its parent environment: when the
parent environment is team-wide the secret is team-scoped,
and when the parent environment is task-owned the secret is
also task-owned and only the parent task in the same team may
open it. The canonical `associated_data` used by both
providers binds exactly `(team_id, secret_id, version)`; the
persisted `crypto_provider` column carries the routing
decision and is NOT part of `associated_data`. v0008 does
NOT introduce a direct `team_id` column on `secret_versions`;
team ownership of a `secret_versions` row is established
exclusively through the same-team FK to the canonical
`secrets` parent row.

#### Scenario: Migration envelope CAS preserves immutable identity

- **WHEN** v0008 installs its forward-only schema migration
- **THEN** it SHALL replace v0002's unconditional
  `secret_versions_append_only` UPDATE/DELETE trigger with an
  envelope-CAS UPDATE guard and an unconditional DELETE guard;
  the UPDATE guard SHALL permit changes only to `ciphertext`,
  `key_id`, `key_version`, `crypto_provider`, and `migrated_at`,
  and only after the current transaction has locked the matching
  per-row checkpoint in `state = 'in_progress'` and installed the
  matching transaction-local migration authorization marker;
  identity-column changes, updates without that claim, and every
  delete SHALL remain rejected

#### Scenario: Same-team operator stores a secret

- **WHEN** a valid secret write arrives through trusted API
  Gateway for a same-team team-wide environment
- **THEN** State Registry encrypts the value through the
  active crypto provider using the canonical
  `associated_data = "team_id=<team_id>\nsecret_id=<secret_id>\nversion=<version>\n"`,
  stores a logical `secrets` row and immutable referenced
  `secret_versions` row with non-sensitive opaque `key_id`
  and `key_version`, sets `crypto_provider = 'transit'` on
  the new row, appends a plaintext-free audit entry, and
  returns the secret identifier and version

#### Scenario: Same-team operator stores a secret under a task-owned environment

- **WHEN** a valid secret write arrives through trusted API
  Gateway for a same-team task-owned environment whose parent
  `task_id` belongs to the same team
- **THEN** State Registry stores the secret under that
  task-owned environment, encrypts the value through the
  active crypto provider with the canonical `associated_data`,
  sets `crypto_provider = 'transit'` on the new row, and
  only the parent task in the same team may open the
  resulting `secret_versions`

#### Scenario: Secret value changes

- **WHEN** a same-team operator replaces a secret value
- **THEN** State Registry appends a new encrypted
  `secret_versions` row referencing the same logical secret
  with the new opaque `key_id` / `key_version` and
  `crypto_provider = 'transit'` without modifying or exposing
  prior plaintext; the new row's `(secret_id, version)`
  triple is unique on the canonical `secrets` parent and the
  prior row's identity is unchanged

#### Scenario: Secret targets a foreign environment

- **WHEN** trusted Gateway context attempts to create or
  replace a secret under another team's environment
- **THEN** State Registry returns non-revealing `404`,
  performs no provider encrypt operation, and stores no
  secret or version

#### Scenario: Startup fails closed without an active provider

- **WHEN** the State Registry starts without a configured
  active crypto-provider endpoint, mount path, key name,
  valid Transit token, or verified mTLS trust anchors
- **THEN** startup fails closed and refuses all secret
  writes and open-environment reads

#### Scenario: Decryption fails closed when authenticated associated data does not match

- **WHEN** State Registry attempts to decrypt a stored
  ciphertext using the wrong team, logical secret, or
  version binding in the `associated_data`
- **THEN** State Registry returns the non-revealing `404
environment_unknown_or_unavailable` shape, performs zero
  provider decrypt operations when authorization is denied,
  and logs no key material, nonce, ciphertext, plaintext,
  `associated_data` value, Transit token, Transit endpoint,
  mount path, key name, or raw provider error body

### Requirement: State Registry opens environments only for assigned same-team Executors

State Registry SHALL expose `GET
/v1/environments/{environment_id}/open?task_id={task_id}` only
to the assigned Executor for the referenced task, regardless
of whether that Executor is team-owned or system-owned. The
scope-conditional team predicate SHALL be: when
`tasks.executor.scope = team`, the authenticated Executor
team SHALL equal the parent task's immutable `team_id`; when
`tasks.executor.scope = system`, the authenticated Executor's
own `team_id` SHALL be NULL and the parent task's immutable
`team_id` SHALL be the canonical team for the open. The
request SHALL carry the canonical `task_id` query parameter
and the compact three-part signed token in the
`X-FlowAI-Scope-Token` request header (the body SHALL NOT
carry any scope-token fields). State Registry SHALL reject the
request without any provider decrypt operation or value
disclosure when the `task_id` query parameter is absent,
malformed, or, after successful MAC verification and canonical
claim parsing, does not equal both the payload `task_id`
claim and the canonical assigned task, returning the same
non-revealing `404 environment_unknown_or_unavailable`
shape. The Executor SHALL present its authenticated identity
and a signed, unexpired scope token satisfying the "State
Registry signs open-environment scope tokens with an
allow-listed HMAC and a server-controlled rotating key"
requirement. Before the provider decrypt operation, State
Registry SHALL verify that the protected-header `kid` equals
the payload `key_id`, the token MAC under the declared
allow-listed HMAC algorithm (`HS256`/`HS384`/`HS512`) and the
documented active key window for `key_id`, the expected
literal `audience` (`state-registry.environment.open`), the
`issued_at <= server_now + 30 seconds` and `expiry >
issued_at` and `expiry - issued_at <= 5 minutes` window, every
token claim (including the project-scope rule for
`project_id`: required, nullable only when the canonical
environment has no project scope) against canonical records,
the scope-conditional team predicate above, task assignment,
the non-terminal task state, and project/task applicability.
The active provider SHALL bind
`associated_data = "team_id=<team_id>\nsecret_id=<secret_id>\nversion=<version>\n"`
on the decrypt call. The persisted `crypto_provider` column
SHALL route the decrypt to the local or active provider. On
success it SHALL return authorized env-style values and
append a plaintext-free team-scoped audit entry. Plaintext
SHALL remain in memory only between the provider decrypt step
and the response serialization. State Registry SHALL NOT
accept a token whose `key_id` is outside the documented active
window, SHALL NOT accept a token after terminal task state,
SHALL NOT accept a token presented by an Executor that is no
longer the recorded `tasks.executor_id`, and SHALL NOT expose
Transit credentials, the Transit ciphertext form, the Transit
error body, or any `associated_data` value through any
response, audit entry, or application log.

#### Scenario: Assigned team-owned Executor opens an environment

- **WHEN** the assigned team-owned Executor sends
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`
  with the compact three-part scope token in the
  `X-FlowAI-Scope-Token` request header, the protected header
  carrying `alg` (allow-listed `HS256`/`HS384`/`HS512`),
  `kid`, and `typ` (`scope-token+json`) with `kid == payload
key_id`, and the payload's `team_id`, `project_id`
  (required, null only when the canonical environment has no
  project scope), `task_id`, `environment_id`,
  `executor_id`, `audience` (literal
  `state-registry.environment.open`), `key_id`,
  `issued_at`, and `expiry` (`expiry > issued_at`,
  `expiry - issued_at <= 5 minutes`, `issued_at <=
server_now + 30 seconds`) all matching canonical records,
  the MAC verifying under constant-time comparison, the
  assigned Executor team equals the parent task's team
- **THEN** State Registry returns authorized values
  through the active crypto provider for the row's persisted
  `crypto_provider` marker and records `team_id`, actor,
  action, resource, request, and outcome without plaintext
  or any secret material

#### Scenario: Assigned system-owned Executor opens a cross-team environment

- **WHEN** the assigned system-owned Executor sends
  `GET /v1/environments/{environment_id}/open?task_id={task_id}`
  with a valid scope token whose `team_id` claim equals the
  parent task's `team_id` (the Executor's own `team_id` is
  NULL) and every other canonical claim matches the parent
  task and environment
- **THEN** State Registry returns authorized values
  through the active crypto provider, the scope-conditional
  team predicate resolves to the parent task's `team_id`,
  and the audit entry records the same identifier-level
  metadata plus `executor_scope = 'system'`; no plaintext
  disclosure and no provider detail leak

#### Scenario: Scope token has a foreign or mismatched claim

- **WHEN** any token claim, signature, audience, `key_id`,
  expiry, assignment, team, project, task, environment, or
  Executor binding is invalid
- **THEN** State Registry rejects the request without
  performing any provider decrypt operation, returns the
  non-revealing `404 environment_unknown_or_unavailable`
  shape, and provides no provider-side detail in any log
  or audit

## ADDED Requirements

### Requirement: State Registry routes every secret write and read through a pluggable crypto provider

The State Registry SHALL isolate every secret encrypt and
decrypt operation behind a single `SecretCryptoProvider`
interface whose active implementation is configured at
startup through deployment configuration. The interface
SHALL accept a canonical `associated_data` value at both
encrypt and decrypt time that binds `team_id`,
`logical_secret_id`, and `version`; the interface SHALL
return an opaque public `key_id` (string of 1 to 64
characters matching `^[A-Za-z0-9][A-Za-z0-9._:-]*$`) and an
opaque public `key_version` (integer `>= 1`). The opaque
public `key_id` SHALL be provider-neutral and SHALL NOT expose
`key_name`, the Transit mount path, the Transit namespace,
the Transit ciphertext form, the active provider's nonce
format, or its authentication tag format. The
`key_id` -> `(mount_path, key_name)` mapping SHALL be a
deployment-time runtime configuration loaded from the secure
runtime configuration source at startup; it is NOT persisted
in PostgreSQL, it is NOT mutated by the rotate command (rotate
changes `key_version`, never `key_id`), and it is never
exposed in a public response, audit entry, or application
log. The interface SHALL be implemented as at least two
concrete providers: (a) the OpenBao Transit provider that
calls `POST /v1/<mount>/encrypt/<key_name>`, `POST
/v1/<mount>/decrypt/<key_name>`, `POST
/v1/<mount>/keys/<key_name>/rotate`, and `GET
/v1/<mount>/keys/<key_name>` under the documented
`associated_data` contract, and (b) a local provider whose
encrypted records are written under `crypto_provider =
'local'` and that is disabled by the deployment cutover
procedure under "State Registry disables the local provider
only after every row is verified". The OpenBao Transit
provider SHALL pin the implementation to the documented
default ciphertext shape `vault:v<N>:...` where `<N>` is the
embedded key version; it SHALL fail startup or operation if
the provider returns ciphertext that does not match that
shape after a strict parse; and it SHALL NOT expose the
shape through any public field. The interface SHALL return
one of the documented sentinel errors
(`provider_unavailable`, `provider_forbidden`,
`provider_invalid_aad_or_ciphertext`, `provider_key_unknown`,
`provider_mount_unknown`) on every documented Transit failure
class, and the State Registry SHALL translate every sentinel
to the non-revealing `404 environment_unknown_or_unavailable`
shape for the open-environment endpoint or to the documented
HTTP error shape for the secret write endpoints. The State
Registry SHALL NOT call
`POST /v1/<mount>/rewrap/<key_name>`; the documented Transit
rewrap endpoint accepts only `ciphertext`, `context`,
`key_version`, `nonce`, `reference`, `batch_input` and has
no `associated_data` parameter, so rewrap cannot authenticate
v0008 ciphertext. Rewrap is recorded as an explicit non-goal
at <https://openbao.org/api-docs/secret/transit/#rewrap-data>.
No additional secret-at-rest implementation SHALL exist
outside this interface; every secret write goes through the
active provider; no application code path SHALL compose its
own encrypt / decrypt primitive.

#### Scenario: Active provider is OpenBao Transit after startup

- **WHEN** the State Registry starts with the documented
  Transit configuration
- **THEN** every secret write and read is served by the
  OpenBao Transit provider, every persisted row carries
  `crypto_provider = 'transit'`, every `SecretVersion.key_id`
  and `SecretVersion.key_version` matches the public opaque
  envelope, and the Transit ciphertext form is never present
  in any response, audit, or log

#### Scenario: Sentinel errors never expose Transit detail

- **WHEN** the active provider returns a documented Transit
  failure (sealed, unavailable, ACL denied, mount absent,
  key absent, version absent, tampered ciphertext, or wrong
  `associated_data`)
- **THEN** the State Registry translates it to one of the
  documented sentinel errors and the public response body,
  audit entry, and application log carry the documented
  outcome status class plus identifier-level metadata only;
  no Transit error body, no Transit endpoint or namespace,
  no Transit mount path, no Transit `key_name`, no Transit
  token, and no raw HTTP detail ever leave the Registry

### Requirement: State Registry binds associated_data to exactly (team_id, secret_id, version)

The State Registry SHALL bind `associated_data` to exactly the
canonical triple `team_id`, `logical_secret_id`, and `version`.
For `crypto_provider = 'local'`, the local provider SHALL use
the v0002 legacy UTF-8 bytes
`{team_id}\x00{secret_id}\x00{decimal_version}` used when the
row was sealed. For `crypto_provider = 'transit'`, the active
provider SHALL use the UTF-8 bytes
`"team_id=<team_id>\nsecret_id=<secret_id>\nversion=<uint64>\n"`.
Each provider's decrypt input SHALL be byte-equal to that
provider's original encrypt input for the row. The migration
worker SHALL decrypt with the legacy format and encrypt with the
active format; this re-authenticates the same identity triple
rather than performing byte-equal re-encryption. Neither format
contains a provider marker, scope marker, nonce, key reference,
or token. The State Registry SHALL return the non-revealing
`404 environment_unknown_or_unavailable` shape and audit
the failure with the `provider_invalid_aad_or_ciphertext`
outcome status class whenever the provider reports an AAD
mismatch. The routing decision for each row (which provider
serves the read) SHALL be taken exclusively from the
persisted `crypto_provider` column on `secret_versions`,
NOT from any value inside `associated_data`.

#### Scenario: Provider route is taken from the persisted column, not AAD

- **WHEN** the State Registry serves an open for a row
  whose persisted `crypto_provider = 'transit'`
- **THEN** the State Registry SHALL call the active
  provider's `Decrypt` with the canonical `associated_data`
  byte string, regardless of any prior `crypto_provider`
  state or any field within `associated_data`

### Requirement: State Registry migrates local-at-rest ciphertext to the active provider through a resumable per-(team_id, secret_id, version) reconciliation owned durably by State Registry in PostgreSQL

The State Registry SHALL backfill every existing
`secret_versions` row written by v0002 with
`crypto_provider = 'local'` and SHALL provide a resumable,
idempotent migration worker owned by State Registry that
converts local ciphertext to active-provider ciphertext one
immutable secret version at a time. The worker SHALL be
an in-process worker inside the existing `state-registry`
deployment boundary; it SHALL NOT be a separate service and
it SHALL NOT keep any durable state outside PostgreSQL. The
worker SHALL own a per-row durable
`crypto_migration_checkpoints(migration_name, team_id,
secret_id, version, state, attempt_count, verified_at,
error_class, updated_at, PRIMARY KEY(migration_name, team_id,
secret_id, version))` table inside the same PostgreSQL
database the State Registry already uses; every immutable
secret version in scope for the migration is represented by
exactly one row, keyed by
`(migration_name, team_id, secret_id, version)` so that
`secret_id` is NOT assumed to be globally unique across
teams. `state` is constrained to `pending | in_progress |
verified | failed`. The table is the worker's single source
of truth; a worker restart reads the table under row-level
lock to resume from the next `(team_id, secret_id, version)`
tuple whose `state IN ('pending','failed')` and whose
canonical identity still resolves. The worker SHALL work
over batches keyed by `(team_id, crypto_provider,
secret_id, version)`; per row, the worker SHALL verify that
the canonical `secrets` parent row still exists with the
same `team_id` and that the row's persisted `(secret_id,
version)` identity still resolves on that parent; v0008
SHALL NOT introduce a direct `team_id` column on
`secret_versions`, so the canonical team is derived from
the canonical `secrets` parent row. The worker SHALL NOT
require `version` to equal a `current_version` field on
the parent because `secrets` does NOT own per-version
identity. The verification SHALL NOT consult `audit_entries`
for any authorization or source-of-truth purpose
(audit entries are append-only evidence only). After
verification, the worker SHALL claim the per-row checkpoint
under row-level lock by transitioning `state = 'pending'`
(or `'failed'`) to `'in_progress'`, decrypt through the
local provider with the v0002 legacy `associated_data` byte
string, encrypt through the active provider with the v0008
active byte string, and in one transaction that holds a
row-level lock on both the `crypto_migration_checkpoints`
row (keyed by `(migration_name, team_id, secret_id,
version)`) and the targeted `secret_versions` row, rewrite
ONLY the envelope columns (`ciphertext`, `key_id`,
`key_version`, `crypto_provider = 'transit'`,
`migrated_at`) of the targeted row, mark the checkpoint
row `state = 'verified', verified_at = NOW()`, and append a
`secret_version_migrated` audit entry with identifier-level
metadata only. The canonical team_id used to write the
checkpoint row and the audit entry SHALL be derived from
the canonical `secrets` parent row. The targeted row's
`(secret_id, version)` and logical secret identity SHALL be
immutable and SHALL NOT be touched by the worker. Plaintext
SHALL exist only in State Registry process memory between
the local decrypt step and the active encrypt step and
SHALL travel only to the verified mTLS-protected in-flight
request accepted by Transit; plaintext SHALL NOT be
persisted, SHALL NOT be logged, SHALL NOT be audited,
SHALL NOT be returned to any operator, and SHALL NOT be
transmitted to any other destination. The CAS commit
replaces any provider-once claim: if the worker crashes
between the Transit call returning ciphertext and
PostgreSQL committing, a retry MAY invoke the provider
again; the CAS predicate `WHERE crypto_provider = 'local'`
ensures that no already-committed `'transit'` row is
encrypted again; exactly one migrated envelope is committed
and visible per immutable version; discarded provider
responses are NEVER persisted and NEVER logged. The worker
SHALL fail closed on any provider error and SHALL record
the failure in `crypto_migration_checkpoints` so the
operator can retry after acknowledgement. The worker SHALL
NOT expose any new public HTTP endpoint to query migration
progress. Transit-native rows that were written directly
through the active provider and therefore carry
`migrated_at IS NULL` do NOT require a checkpoint row;
their successful write transaction and the documented
envelope constraints are the only evidence that they are
intact, and cutover trusts them through that evidence.

#### Scenario: Migration happy path verifies canonical identity and rewrites envelope atomically

- **WHEN** the migration worker selects a `crypto_provider
= 'local'` row whose canonical `secrets` parent row
  exists with the row's same `team_id` and whose
  `(secret_id, version)` identity still resolves on the
  parent
- **THEN** the worker claims the per-row checkpoint under
  row-level lock keyed by `(migration_name, team_id,
secret_id, version)`, decrypts locally in memory with the
  v0002 legacy `associated_data` byte string whose `team_id`
  is the canonical team's identifier, encrypts through the
  active provider with the v0008 active byte string,
  and in one transaction that holds a row-level lock on
  both rows, rewrites ONLY the envelope columns
  (`ciphertext`, `key_id`, `key_version`,
  `crypto_provider = 'transit'`, `migrated_at`) of the
  targeted row, marks the checkpoint `state = 'verified',
verified_at = NOW()`, and appends a
  `secret_version_migrated` audit entry with identifier-
  level metadata only; the targeted row's `(secret_id,
version)` identity is unchanged; the row is never
  visible to a reader in an intermediate state; plaintext
  never persists, is never logged, is never audited, is
  never returned to any operator, and travels only inside
  the verified mTLS-protected in-flight request to Transit

#### Scenario: Migration restart is idempotent

- **WHEN** the migration worker is restarted between two
  immutable versions
- **THEN** the worker resumes from the next `(team_id,
secret_id, version)` row in
  `crypto_migration_checkpoints` whose `state IN
('pending','failed')` after the restart, never re-encrypts
  any row whose marker is already `'transit'`, never
  duplicates any canonical `audit_entries` row, and
  produces the same final state as a single uninterrupted
  run; the worker reads the per-row checkpoint table under
  row-level lock on every boot

#### Scenario: Mixed local and Transit rows read through the persisted marker

- **WHEN** some `secret_versions` rows carry
  `crypto_provider = 'local'` and others carry
  `crypto_provider = 'transit'` during the migration window
- **THEN** every open-environment read and every secret
  replacement routes by the row's persisted marker to the
  corresponding provider with that provider's documented
  `associated_data` byte format, the non-revealing `404
environment_unknown_or_unavailable` shape is returned
  unchanged on every denial, and no plaintext is exchanged
  between the two providers

#### Scenario: Crash between Transit call and PostgreSQL commit is safe

- **WHEN** the worker crashes between the active provider
  returning ciphertext and the CAS commit of the envelope
  rewrite
- **THEN** on the next worker run the per-row checkpoint
  keyed by `(migration_name, team_id, secret_id, version)`
  still says `state = 'pending'` (or `'failed'` after a
  recorded failure), the worker re-attempts the row, the
  CAS predicate `WHERE crypto_provider = 'local'` ensures
  no already-committed `'transit'` row is encrypted again,
  at most one envelope is committed per immutable version,
  and the discarded earlier provider response is never
  persisted or logged

### Requirement: State Registry disables the local provider only after every row is verified

The State Registry SHALL provide an audited
deployment/operator cutover procedure that runs inside the
existing `state-registry` deployment boundary under the
existing deployment identity. The procedure SHALL be exposed
as a `state-registry` internal command and SHALL NOT be
exposed as a new public HTTP endpoint under the OpenAPI
surface. The cutover procedure SHALL run one immediate
transactional consistency scan over the canonical tables; the
scan is the entire cutover gate and there is no separate
report table, no freshness timestamp, and no separate
worker-state row. The scan SHALL confirm, all in one
transaction:

- `SELECT COUNT(*) FROM secret_versions WHERE
crypto_provider = 'local'` equals `0`.
- For every `secret_versions` row where `migrated_at IS NOT
NULL` and `crypto_provider = 'transit'`, the matching row
  in `crypto_migration_checkpoints` keyed by
  `(migration_name, team_id, secret_id, version) =
('local_to_transit', canonical_team_id, secret_id, version)`
  (where the canonical team_id is derived from the
  `secrets` parent of the version row) has `state =
'verified'` and `verified_at >= migrated_at`.
- `SELECT COUNT(*) FROM crypto_migration_checkpoints
WHERE state IN ('pending','in_progress','failed')
AND migration_name = 'local_to_transit'` equals `0`;
  the absence of `in_progress` rows is the worker-idle
  proof — there is no other worker-state row.
- The active provider's `GET /v1/<mount>/keys/<key_name>`
  read-key call succeeds.

Transit-native rows that were written directly through
the active provider carry `migrated_at IS NULL` and are
trusted through their successful provider write
transaction; they do NOT require a checkpoint row and
cutover does NOT scan them for a checkpoint. The procedure
SHALL NOT decrypt arbitrary Transit rows again and SHALL
NOT use `POST /v1/<mount>/rewrap/<key_name>` as proof (it
would be unable to authenticate AAD-bound ciphertext
anyway). The public open-environment authorization path is
unchanged by this procedure.

#### Scenario: Cutover accepted only after per-row consistency scan

- **WHEN** an operator invokes the deployment cutover
  procedure signed by the deployment identity and the
  transactional consistency scan above returns the
  documented acceptance conditions
- **THEN** State Registry accepts the procedure, records a
  plaintext-free audit entry with action `crypto.cutover`
  and outcome `succeeded`, sets `crypto.local.enabled =
false`, and continues to serve every Transit row under the
  active provider

#### Scenario: Cutover rejected when any row is still local

- **WHEN** an operator invokes the deployment cutover
  procedure and the canonical `secret_versions` table still
  contains any row with `crypto_provider = 'local'` or any
  per-row checkpoint in `state IN
('pending','in_progress','failed')`
- **THEN** State Registry rejects the procedure, records the
  rejection in `crypto_migration_checkpoints`, audits the
  rejection with identifier-level metadata only, and
  continues to serve both provider classes

### Requirement: State Registry rotates new writes to a new Transit key version while retaining historical AAD-bound key versions

The State Registry SHALL provide an internal rotate procedure
that runs inside the existing `state-registry` deployment
boundary under the existing deployment identity. The
procedure SHALL be exposed as a `state-registry` internal
command and SHALL NOT be exposed as a new public HTTP
endpoint under the OpenAPI surface. The procedure SHALL call
`POST /v1/<mount>/keys/<key_name>/rotate` on the canonical
Transit key named in the deployment configuration; the
rotate endpoint takes no body parameters and returns no
documented success body. After the rotate call succeeds,
new encrypt calls through the active provider that do not
specify `key_version` SHALL use the new latest version. The
State Registry MAY observe the new latest version through a
subsequent `GET /v1/<mount>/keys/<key_name>` call when it
needs to read the key configuration, but the rotate call
itself does NOT return a version and SHALL NOT be polled for
one. The State Registry SHALL record the audit entry
`crypto.rotate` with identifier-level metadata only (the
canonical `key_id`, the previous and new latest version, the
`request_id`, the outcome status class). Historical
AAD-bound ciphertext rows SHALL remain bound to their
embedded older key version and SHALL decrypt under that
older version through the active provider; ordinary
rotation SHALL NOT produce or persist plaintext and SHALL
NOT decrypt existing rows. The State Registry SHALL NOT call
`POST /v1/<mount>/rewrap/<key_name>` to rotate AAD-bound
ciphertext because rewrap has no `associated_data` parameter
and cannot authenticate v0008 ciphertext; the documented
Transit rewrap endpoint is recorded as intentionally not
used. `min_encryption_version` and `min_decryption_version`
are independent key-configuration floors set out of band
through `POST /v1/<mount>/keys/<key_name>/config`; v0008 SHALL
NOT advance `min_decryption_version` past any key version
still referenced by an AAD-bound ciphertext row. Retiring
historical key versions requires an explicit future
migration change and is not part of v0008. The opaque
public `key_id` SHALL NOT change as a result of a rotate; it
is a deployment-time stable identifier that maps to a
`(mount_path, key_name)` pair through the secure runtime
configuration.

#### Scenario: Rotate creates a new latest key version and ordinary new writes use it

- **WHEN** the operator invokes the deployment rotate
  procedure signed by the deployment identity on the
  canonical Transit key
- **THEN** `POST /v1/<mount>/keys/<key_name>/rotate` succeeds
  without a documented response body, the canonical Transit
  key gains a new latest key version, every subsequent
  encrypt call that does not specify `key_version` uses the
  new latest version, every existing Transit ciphertext
  whose embedded version is no less than
  `min_decryption_version` still decrypts through the active
  provider, the opaque `key_id` does not change, and the
  action records a plaintext-free `audit_entries` row with
  action `crypto.rotate` and identifier-level metadata only

#### Scenario: Ordinary rotation never rewrites historical ciphertext

- **WHEN** the rotate procedure succeeds and historical
  AAD-bound ciphertext rows still exist
- **THEN** those rows SHALL remain unchanged in
  `secret_versions`; the State Registry SHALL NOT call
  `POST /v1/<mount>/rewrap/<key_name>` for them; no provider
  decrypt call SHALL be made on them as part of ordinary
  rotation; no plaintext SHALL ever be produced or persisted

#### Scenario: min_decryption_version is never raised past referenced versions by v0008

- **WHEN** historical Transit key versions are still
  referenced by existing AAD-bound ciphertext rows
- **THEN** the State Registry SHALL NOT issue a request
  to `POST /v1/<mount>/keys/<key_name>/config` that raises
  `min_decryption_version` past any of those versions; the
  deployment procedure that retires historical versions is
  out of scope for v0008

### Requirement: State Registry pins the non-revealing 404 shape to every provider failure class

The State Registry SHALL return the same non-revealing `404
environment_unknown_or_unavailable` shape for every
documented failure mode that touches the active crypto
provider after canonical authorization passes. The
documented failure modes SHALL include at least Transit
sealed, Transit unavailable, ACL denied, mount absent, key
absent, version absent, tampered ciphertext, and wrong
`associated_data`; the State Registry SHALL translate every
documented failure mode to one of the documented sentinel
errors and SHALL return the non-revealing `404
environment_unknown_or_unavailable` response in every
case. Provider outage detail SHALL be routed only to
plaintext-free operator telemetry and audit entries; the
public response body, audit entry, and application log
SHALL carry identifier-level metadata plus the outcome
status class only. The State Registry SHALL perform zero
provider decrypt operations when authorization is denied;
the provider decrypt operation SHALL occur only after every
canonical authorization check passes.

#### Scenario: Transit sealed state returns the same non-revealing 404

- **WHEN** every canonical authorization check passes and
  the active Transit server reports a sealed state
- **THEN** State Registry returns the non-revealing `404
environment_unknown_or_unavailable` shape with zero
  plaintext disclosure and audits the rejection with the
  `provider_unavailable` outcome status class and
  identifier-level metadata only

#### Scenario: Transit ACL denied returns the same non-revealing 404

- **WHEN** every canonical authorization check passes and
  the active Transit server returns an ACL denial for the
  encrypt, decrypt, or rotate call
- **THEN** State Registry returns the non-revealing `404
environment_unknown_or_unavailable` shape with zero
  plaintext disclosure and audits the rejection with the
  `provider_forbidden` outcome status class and
  identifier-level metadata only

#### Scenario: Transit wrong `associated_data` returns the same non-revealing 404

- **WHEN** every canonical authorization check passes and
  the active Transit server reports an `associated_data`
  mismatch for the provider decrypt call
- **THEN** State Registry returns the non-revealing `404
environment_unknown_or_unavailable` shape with zero
  plaintext disclosure, zero Transit error-body
  disclosure, and audits the rejection with the
  `provider_invalid_aad_or_ciphertext` outcome status class
  and identifier-level metadata only

### Requirement: State Registry records plaintext-free team audit entries

The State Registry SHALL record, for each auditable
authorized operator action, control, environment or secret
mutation, and open-environment access, an immutable audit
entry containing `team_id`, actor identity and type, action,
resource type and identifier, `request_id`, outcome, and
timestamp. Audit entries, application logs, event payloads,
and error responses SHALL NOT contain secret plaintext,
decrypted environment values, key material, nonce bytes,
ciphertext bytes, authentication tag bytes,
`associated_data` values, or individual claim values beyond
identifier-level metadata. Audit reads SHALL be filtered by
trusted `team_id` before pagination or aggregation. During
v0008, the existing audit-friendly invariant is preserved
exactly; the new audit actions are `crypto.rotate`,
`crypto.cutover`, and `crypto.migrate` (for
`secret_version_migrated`).

#### Scenario: v0008 audit actions carry only identifier-level metadata

- **WHEN** the State Registry persists a `crypto.rotate`
  audit entry, a `crypto.cutover` audit entry, or a
  `crypto.migrate` (`secret_version_migrated`) audit entry
- **THEN** the audit entry SHALL contain `team_id`,
  `secret_id` (when applicable), opaque `key_id`, opaque
  `key_version`, the operation name, the outcome status
  class, and `request_id`; it SHALL NOT contain any
  plaintext, `associated_data` bytes, ciphertext bytes,
  nonce bytes, authentication tag bytes, key material, or
  derived key bytes

## REMOVED Requirements

### Requirement: Startup fails closed when AES-GCM key is missing

> Removed as a dedicated requirement header. The same
> invariant is restated as "Startup fails closed without an
> active provider" under the modified "State Registry stores
> team-owned secrets with immutable versions"; the boundary
> moves from the local AES-GCM key to the active provider
> descriptor. The behavioural invariant (startup refuses
> all secret writes and open-environment reads when the
> cryptographic prerequisite is missing) is unchanged.

### Requirement: Decryption fails closed when authenticated associated data does not match

> Removed as a dedicated requirement header. The same
> invariant is restated as "Decryption fails closed when
> authenticated associated data does not match" under the
> modified "State Registry stores team-owned secrets with
> immutable versions", and the provider-level failure
> classes it covers are enumerated under the new "State
> Registry pins the non-revealing 404 shape to every
> provider failure class" requirement.
