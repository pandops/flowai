# Design: State Registry — Use OpenBao Transit as the active provider

## Provider abstraction

The State Registry introduces an in-process interface that
every at-rest secret encryption call routes through. The
interface is the boundary documented in
`openspec/changes/v0008-use-openbao-transit/specs/state-registry/spec.md`;
its concrete implementations are described here so the
contract is unambiguous and the v0002 public surface
(`key_id`, `key_version`, paths, error shapes) remains
untouched.

```text
type SecretCryptoProvider interface {
    Encrypt(ctx, plaintext []byte, aad CipherAAD) (EncryptedRecord, error)
    Decrypt(ctx, record EncryptedRecord, aad CipherAAD) ([]byte, error)
    Active() ProviderDescriptor
    Health(ctx) error
}

type EncryptedRecord struct {
    Ciphertext []byte
    KeyID      string // opaque public identifier; never exposes key_name
    KeyVersion uint32 // opaque provider-neutral integer version
}

type CipherAAD struct {
    TeamID        string
    LogicalSecret string
    Version       uint64
}

type ProviderDescriptor struct {
    Name      string // "local", "transit"
    MountPath string
    KeyName   string
}
```

The `CipherAAD` value type binds ONLY the canonical
`team_id`, `logical_secret_id`, `version` triple. v0008 does
NOT add a `provider_marker` field to AAD; the routing
decision for each row is taken from the persisted
`crypto_provider` column on `secret_versions`, not from any
authenticated binding. The local provider and the active
Transit provider therefore use the SAME canonical AAD bytes
for a given canonical input triple, which is why a local row
can decrypt under the same `associated_data` it was written
under after migration re-encrypts it through Transit.

The opaque envelope recorded on every `secret_versions` row
is exactly the `EncryptedRecord`: opaque `key_id` string
(deployment-time runtime configuration maps it to a
`(mount_path, key_name)` pair) and `key_version` integer.
Public fields stay the same in the OpenAPI schema; the
public `key_id` is provider-neutral and never exposes
`key_name`, the Transit mount path, or the Transit
ciphertext form.

### `key_id` as a deployment-time runtime configuration

`key_id` is an opaque public identifier chosen at deployment
time. The State Registry loads the deployment-time mapping
from `key_id` to the `(mount_path, key_name)` pair from the
secure runtime configuration source at startup. The mapping
is NOT a PostgreSQL lookup table, NOT a runtime-discovered
node, NOT a schema, and NOT mutated by `rotate` (rotate
changes `key_version`; the public `key_id` is stable for the
lifetime of the deployment). The mapping is never exposed
through any public response, audit entry, or application
log. Reads consult the deployment-time mapping to resolve
the `(mount_path, key_name)` pair before constructing a
Transit request.

## OpenBao Transit implementation

The active implementation calls OpenBao Transit's documented
REST endpoints over mutually authenticated TLS. The HTTP
body and headers are constructed to match the public API
documentation; every error returned by Transit is translated
to one of the State Registry's internal sentinel errors so
the public `404 environment_unknown_or_unavailable` shape
is preserved. The endpoint choice follows the official 2.6
API:

| Concern | Implementation choice |
|---|---|
| Endpoint naming | Configurable `<mount>` (default `/transit`). Configurable `key_name`. |
| Encryption | `POST /v1/<mount>/encrypt/<key_name>` with JSON body `{"plaintext": "<b64>", "associated_data": "<b64>"}`; no client nonce (Transit generates its nonce; documented fields are `plaintext`, `associated_data`, `context` (unused), `key_version`, `nonce` (unused), `batch_input` (unused)). |
| Decryption | `POST /v1/<mount>/decrypt/<key_name>` with JSON body `{"ciphertext": "<text>", "associated_data": "<b64>"}`. The opaque `associated_data` MUST equal the value used at `Encrypt` time byte-for-byte; mismatch is the canonical "wrong AAD" failure and routes to the non-revealing `404 environment_unknown_or_unavailable`. |
| Rotation (rotate) | `POST /v1/<mount>/keys/<key_name>/rotate` creates a new (latest) key version. The documented request takes no body parameters and the documented success response carries no body. Subsequent encrypt calls that do not specify `key_version` use the new latest version the deployment permits. Existing AAD-bound ciphertext from prior versions is left untouched and continues to decrypt under the older key version that matches its embedded version, as long as `min_decryption_version` permits. The rotate call itself does NOT raise `min_encryption_version` or `min_decryption_version`. |
| Read-key | `GET /v1/<mount>/keys/<key_name>` returns the documented JSON `{"data": {"type": ..., "deletion_allowed": ..., "derived": ..., "exportable": ..., "allow_plaintext_backup": ..., "keys": {"<version>": <timestamp>, ...}, "min_decryption_version": ..., "min_encryption_version": ..., "name": ..., "supports_encryption": ..., "supports_decryption": ...}}`. The State Registry uses this call only to observe the canonical key's current state during cutover verification; nothing else. |
| Rewrap | Intentionally NOT used. The documented rewrap endpoint at `POST /v1/<mount>/rewrap/<key_name>` accepts only `ciphertext`, `context`, `key_version`, `nonce`, `reference`, `batch_input` — it has no `associated_data` parameter. Since every v0008 ciphertext binds `associated_data` at encrypt time, rewrap cannot authenticate a v0008 ciphertext and SHALL NOT be called by the State Registry. This is documented at <https://openbao.org/api-docs/secret/transit/#rewrap-data>. |
| Key configuration | Out of band: `POST /v1/<mount>/keys/<key_name>` with the documented `aes256-gcm96`, `deletion_allowed=false`, `exportable=false`, `allow_plaintext_backup=false`; explicit `min_decryption_version` and `min_encryption_version` set through `POST /v1/<mount>/keys/<key_name>/config`. v0008 SHALL NOT advance `min_decryption_version` past any still-referenced Transit ciphertext; retiring historical versions requires an explicit future migration change. |
| Ciphertext format | v0008 pins the implementation to the documented default ciphertext shape `vault:v<N>:...` where `<N>` is the embedded key version. The official OpenBao 2.6 public API does not expose a configurable `version_template`, so v0008 does not configure one; the implementation parses the documented shape strictly and fails startup or operation if the provider returns ciphertext that does not match. |
| Authentication | HTTP `X-Vault-Token: <token>` (or Kubernetes / Vault Agent wrapped equivalent). The token is loaded from a secure configuration source at startup; rotated independently of any State Registry restart. The token MUST NOT be sent to logs, audit, or error responses. |
| Network identity | mTLS with verified peer certificates; self-signed Transit deployments pin the CA bundle through State Registry configuration. |
| Disable upsert | `POST /v1/<mount>/config/keys` with `disable_upsert=true` set out of band so a typo never creates a key through `/encrypt`. |
| Failure mapping | `5xx` and connect failure -> internal sentinel `provider_unavailable`. `403 permission denied` -> `provider_forbidden`. `400 invalid ciphertext` or `400 associated_data mismatch` -> `provider_invalid_aad_or_ciphertext`. `404 key not found` -> `provider_key_unknown`. `404 mount not found` -> `provider_mount_unknown`. All sentinels return the same `404 environment_unknown_or_unavailable` shape to the caller; provider-side detail is logged to plaintext-free audit. |

The `AssociatedData` value is exactly the canonical binding
the Registry uses for the row, encoded as a UTF-8 byte string:

```
team_id=<team_id>\nsecret_id=<secret_id>\nversion=<uint64>\n
```

This matches v0002's authenticated binding of team identifier
/ logical secret identifier / version. Both providers
produce the same bytes for the same canonical inputs, so a
local row's `associated_data` is byte-identical to the Transit
row's `associated_data` for the same logical secret version.
The persisted `crypto_provider` column records the routing
decision, not the binding.

### Why "no client nonce"

OpenBao Transit's documented encrypt contract generates the
nonce internally and embeds it in the returned ciphertext
(the documented default form `vault:v<N>:...`). Passing a
client nonce is not part of the documented ordinary `POST
/v1/<mount>/encrypt/<key_name>` payload for at-rest
authenticated encryption. The active provider therefore
never sends one; this preserves wire-format compatibility
and avoids a documented unsupported field.

### Why only `associated_data`, never `context`

OpenBao Transit accepts an optional `context` field distinct
from `associated_data`. The State Registry binds `team_id`,
`logical_secret_id`, and `version` exclusively through
`associated_data` (which is authenticated like a nonce on
every encrypt and decrypt). The State Registry does NOT use
`context` for any project or task scope narrowing. A future
derived-key design MAY adopt `context`, but it does so as an
explicit follow-up change and never as an implicit override
of `associated_data`.

## Migration contract

OpenBao Transit operates only on Transit ciphertext; v0002
local AES-256-GCM ciphertext cannot be rewrap'd or
re-encrypted under any documented Transit API path. The
migration is a deliberate reconciliation, not a rotation,
and the worker mutates ONLY the cryptographic envelope
columns of each immutable secret version under a row-level
lock.

| Step | Atomic boundary | Outcome |
|---|---|---|
| 1. Discover | Select local rows joined to canonical secrets parents: `SELECT SV.team_id, SV.secret_id, SV.version FROM secret_versions SV JOIN secrets S ON S.environment_id IN (env_set) AND S.secret_id = SV.secret_id WHERE SV.crypto_provider = 'local' ORDER BY SV.team_id, SV.secret_id, SV.version LIMIT $1`. The canonical `team_id` is the parent's `team_id` (v0008 does NOT introduce a direct `team_id` column on `secret_versions`). | A bounded batch of legacy rows whose `state = 'pending'` in `crypto_migration_checkpoints` keyed by `(migration_name, team_id, secret_id, version)`. |
| 2. Verify canonical state | For each row, confirm the canonical `secrets` parent row still exists and that the row's persisted `(secret_id, version)` still resolves on the parent. The canonical team_id used to write the checkpoint row and the audit entry is the parent's `team_id`. Audit entries are NEVER consulted as an authorization source. The verification does NOT require `version` to equal a `current_version` field on the parent because `secrets` does not own per-version identity. | Either the row's identity and team ownership are still authoritative or it is removed from the batch. |
| 3. Claim per-row checkpoint | `UPDATE crypto_migration_checkpoints SET state = 'in_progress', attempt_count = attempt_count + 1, updated_at = NOW() WHERE (migration_name, team_id, secret_id, version) = ($1, $2, $3, $4) AND state IN ('pending','failed')` under row-level lock; verify exactly one row was updated. | The targeted row is now `in_progress`; no other worker can double-process it. |
| 4. Local decrypt | Call the local provider's `Decrypt` with the persisted AAD bytes recorded on the row. The local provider used the same canonical AAD `team_id\nsecret_id\nversion\n` at original encryption. | Plaintext exists in State Registry process memory only between this call and step 5 only. |
| 5. Transit encrypt | Call the active provider's `Encrypt` over the verified mTLS-protected in-flight request with the same canonical AAD `team_id\nsecret_id\nversion\n`. Receive the opaque ciphertext whose embedded key version is parsed against the documented default `vault:v<N>:...` shape. | A new opaque encrypted record. |
| 6. Persist atomically (compare-and-swap envelope) | One transaction that holds a row-level lock on both the `crypto_migration_checkpoints` row keyed by `(migration_name, team_id, secret_id, version)` and the targeted `secret_versions` row: `UPDATE secret_versions SET ciphertext = $1, key_id = $2, key_version = $3, crypto_provider = 'transit', migrated_at = $4 WHERE secret_id = $5 AND version = $6 AND crypto_provider = 'local'`; `UPDATE crypto_migration_checkpoints SET state = 'verified', verified_at = NOW() WHERE migration_name = $7 AND team_id = $8 AND secret_id = $5 AND version = $6`; append the canonical `audit_entries` row with `team_id`, `secret_id`, `version`, action `secret_version_migrated`, and outcome `accepted`. The targeted row's `(secret_id, version)` and logical secret identity are NEVER touched by this transaction. | The row's logical identity is unchanged; the cryptographic envelope and the checkpoint state are swapped atomically. |
| 7. Verify persistence | After commit, a subsequent read of `secret_versions` and `crypto_migration_checkpoints` returns the persisted envelope and the `verified` checkpoint; concurrent readers see the complete old envelope or the complete new envelope (never a mixed intermediate state). | Migration progress advances one row. |

The migration's idempotency boundary is the per-row
`crypto_migration_checkpoints` table inside PostgreSQL. A
row whose state is already `verified` is never re-encrypted.
A worker restart reads the per-row checkpoint table under
row-level lock to resume from the next `(team_id, secret_id,
version)` tuple in `state IN ('pending', 'failed')` whose
canonical identity still resolves. The CAS predicate
`WHERE crypto_provider = 'local'` in step 6 ensures that a
retry after a partial failure cannot re-encrypt an
already-committed row.

### Crash and idempotency contract

The worker makes one or more Transit encrypt calls per
immutable secret version. The OpenBao Transit encrypt API
has no idempotency key. A crash between step 5 (provider
returns ciphertext) and step 6 (PostgreSQL commits) MAY
cause the next worker run to invoke the provider a second
time for the same immutable version. The contract
guarantees:

- No already-committed `crypto_provider = 'transit'` row is
  ever encrypted again, because step 6's CAS predicate is
  `WHERE crypto_provider = 'local'`; a row whose marker has
  already been flipped to `'transit'` does not match the
  predicate and is not touched again.
- Exactly one migrated envelope is committed and visible
  per immutable version.
- No canonical `audit_entries` row is duplicated per
  `(team_id, secret_id, version, action)` triple.
- Discarded provider responses (those returned for a row
  whose CAS commit later failed) are NEVER persisted and
  NEVER logged. They live only in process memory before the
  transaction rolls back.
- The per-row checkpoint table records the worker's CAS
  state and reflects the final outcome.

This contract allows the worker to invoke Transit encrypt
more than once per row in pathological cases; it forbids
multiple on-disk envelopes, multiple audit rows, or
provider-discarded ciphertext bleeding into any audit or
log sink.

## Cutover

The local provider CANNOT be removed while any row still
carries `crypto_provider = 'local'`. Cutover is an audited
deployment/operator procedure that runs inside the existing
`state-registry` deployment boundary under the existing
deployment identity; it SHALL be exposed as a
`state-registry` internal command and SHALL NOT be exposed
as a new public HTTP endpoint under the OpenAPI surface.

Cutover is one immediate transactional consistency scan over
the canonical tables; there is no separate worker-state row,
no report table, and no freshness-timestamp artifact. The
scan accepts the cutover when all of the following conditions
hold atomically:

- `SELECT COUNT(*) FROM secret_versions WHERE
  crypto_provider = 'local'` returns `0`.
- For every `secret_versions` row where `migrated_at IS NOT
  NULL` and `crypto_provider = 'transit'`: a matching row in
  `crypto_migration_checkpoints` (matched by
  `migration_name = 'local_to_transit', team_id, secret_id,
  version`, where `team_id` is the canonical team's
  identifier derived from the `secrets` parent of the
  version row) exists with `state = 'verified'` and
  `verified_at >= migrated_at`.
- `SELECT COUNT(*) FROM crypto_migration_checkpoints
  WHERE state IN ('pending','in_progress','failed')
  AND migration_name = 'local_to_transit'` returns `0`. The
  absence of `in_progress` rows is the worker-idle proof;
  there is no other worker-state row.
- The active provider's
  `GET /v1/<mount>/keys/<key_name>` read-key call succeeds
  with the documented JSON response for the canonical key.

Transit-native rows written directly through the active
provider carry `migrated_at IS NULL` and do NOT require a
checkpoint row; they are trusted through their successful
provider write transaction. Cutover does NOT decrypt
arbitrary Transit rows again and does NOT use `rewrap` as
proof.

When every condition above holds, the procedure accepts with
the documented internal acknowledgement and sets
`crypto.local.enabled = false`. After that moment, any read
of a row whose marker is `crypto_provider = 'local'` returns
the non-revealing `404 environment_unknown_or_unavailable`
shape with zero provider operations and zero plaintext
disclosure.

## Rotation workflow

The State Registry exposes an internal rotate procedure as
a `state-registry` internal command authenticated by the
existing deployment identity. It SHALL NOT be exposed as a
new public HTTP endpoint under the OpenAPI surface.

The procedure calls
`POST /v1/<mount>/keys/<key_name>/rotate` on the canonical
Transit key named in the deployment configuration. The
documented rotate request takes no body parameters; the
documented success response carries no body. The State
Registry MAY observe the new latest version through a
subsequent `GET /v1/<mount>/keys/<key_name>` call when it
needs to read the key configuration, but the rotate call
itself does NOT return a version and SHALL NOT be polled for
one.

Effects on existing rows:

- New encrypt calls that do not specify `key_version` now
  use the new latest version. New ciphertext carries the new
  embedded key version.
- Historical AAD-bound ciphertext rows remain bound to
  their embedded older key version and decrypt under that
  older version through the active provider. Plaintext is
  NEVER produced during ordinary rotation; the State
  Registry does not decrypt historical rows.
- The opaque public `key_id` SHALL NOT change as a result
  of a rotate; it is a deployment-time stable identifier
  that maps to a `(mount_path, key_name)` pair through the
  secure runtime configuration.
- `min_encryption_version` and `min_decryption_version` are
  independent key-configuration floors set out of band
  through `POST /v1/<mount>/keys/<key_name>/config`. v0008
  SHALL NOT advance `min_decryption_version` past any key
  version that is still referenced by an existing
  AAD-bound ciphertext row. Retiring historical versions
  requires an explicit future migration change that may
  invalidate the ability to decrypt existing rows; that
  change is not part of v0008.
- The rotate command records the audit action
  `crypto.rotate` with identifier-level metadata only (the
  previous and new latest version, the canonical `key_id`,
  the `request_id`, the outcome status class).

The State Registry does NOT call
`POST /v1/<mount>/rewrap/<key_name>` for any AAD-bound
ciphertext; rewrap has no `associated_data` parameter and
cannot authenticate v0008 ciphertext. This is a documented
constraint of the Transit 2.6 API at
<https://openbao.org/api-docs/secret/transit/#rewrap-data>;
v0008 records it as an explicit non-goal so the constraint
is not silently violated by a future change.

## Logging / audit invariant

The v0002 invariant is preserved strictly: every code path
that handles a plaintext secret, ciphertext, nonce, tag,
`associated_data` value beyond identifier-level metadata,
a Transit token, or any raw Transit error body must scrub
those values before any audit append or slog record. The
audit entry MAY carry:

- `team_id`, `secret_id`, opaque `key_id`, opaque integer
  `key_version`
- operation name (`secret.create`, `secret.replace`,
  `secret.open`, `crypto.encrypt`, `crypto.decrypt`,
  `crypto.rotate`, `crypto.cutover`,
  `crypto.migrate`)
- outcome status class (`accepted`, `rejected`,
  `succeeded`, `failed`, `provider_unavailable`,
  `provider_forbidden`,
  `provider_invalid_aad_or_ciphertext`,
  `provider_key_unknown`, `provider_mount_unknown`)
- `request_id`, `executor_id`, `operator_id`, timestamp.

The audit entry MUST NOT carry:

- the plaintext secret value
- the AAD bytes (not even their hash)
- the ciphertext bytes
- the nonce bytes
- the authentication tag bytes
- the Transit ciphertext form `vault:v<N>:...` (the public
  form is the integer `key_version`)
- the Transit `X-Vault-Token` value or any other Transit
  credential
- the Transit API URL, namespace, mount path, key name, or
  any internal `key_id` -> `(mount_path, key_name)`
  mapping (the public form is the opaque `key_id` only)
- the raw Transit error body or the HTTP status line beyond
  the outcome status class
- any individual scope-token claim beyond identifier-level
  metadata

A provider integration test asserts that scrubbing runs on
every provider error path; a separate test asserts that the
network payload captured by an intercepting client never
contains plaintext.

## Plaintext network posture

Plaintext exists only in State Registry process memory
between the local decrypt step and the active provider
encrypt step of the migration worker. The migration worker
transmits plaintext to the active provider only as the
base64 `plaintext` field of a verified mTLS-protected
in-flight request accepted by Transit, with the canonical
`associated_data` field; the request is accepted by the
Transit endpoint only when the State Registry's mTLS peer
identity has been verified and the Transit server-side
policy permits the encrypt call. Plaintext is never
persisted to PostgreSQL, never persisted to any cache or
log file, never written to any audit append or slog
record, and never transmitted to any destination other
than the verified mTLS-protected Transit request. The
invariant is: plaintext exists only in State Registry
process memory and in the verified mTLS-protected in-flight
request accepted by Transit; it is never persisted, never
logged, never audited, and never transmitted without
verified TLS.

## Failure posture

Every invalid or unavailable open returns the same
non-revealing `404 environment_unknown_or_unavailable`
shape. Provider outages are treated as authorization
failures for the cryptographic step only — they never leak
through the response body. The canonical order of checks
before any provider decrypt operation remains the v0002
order:

1. Transport identity and TLS handshake (mTLS verifier).
2. Trusted API Gateway or Executor service identity.
3. Scope token envelope (HMAC `kid == key_id`, allow-listed
   `HS256 / HS384 / HS512`, `audience`, `issued_at`,
   `expiry`, canonical claims).
4. Canonical authorization (team predicate conditional on
   the Executor's scope: same-team or system-owned scope
   applied to the parent task's `team_id`; non-terminal
   task; current assignment; applicability).
5. THEN AND ONLY THEN: provider `Decrypt`.

Every failure mode (mTLS refused, Gateway identity missing,
scope token invalid, audience wrong, claim mismatch,
foreign team or foreign executor, terminal task,
unassigned, project/task scope fail) returns the
non-revealing `404 environment_unknown_or_unavailable`
shape with zero provider decrypt operations when
authorization is denied; the provider decrypt operation
occurs only after every canonical authorization check
passes. After authorization passes, every provider failure
(sealed, unavailable, ACL denied, mount absent, key absent,
version absent, tampered ciphertext, wrong
`associated_data`) returns the same shape with a
plaintext-free audit entry. There is no error path on
which a plaintext secret value, a Transit ciphertext form,
an `associated_data` value, a Transit token, a Transit
endpoint, a Transit namespace, a Transit mount path, a
Transit `key_name`, or a Transit error body is exposed in a
log, audit entry, or HTTP response.

## 3NF ownership

v0008 extends the v0002 3NF schema with one new column on
`secret_versions`, one per-row auxiliary
`crypto_migration_checkpoints` table. v0008 does NOT add a
direct `team_id` column to `secret_versions`; team
ownership is established exclusively through the
same-team foreign key to the canonical `secrets` parent row.
v0008 does NOT introduce a `key_id` lookup table; the
`key_id` -> `(mount_path, key_name)` mapping is a
deployment-time runtime configuration.

```text
ALTER TABLE secret_versions
  ADD COLUMN crypto_provider TEXT NOT NULL DEFAULT 'local'
    CHECK (crypto_provider IN ('local','transit')),
  ADD COLUMN migrated_at     TIMESTAMPTZ;

CREATE TABLE crypto_migration_checkpoints (
  migration_name  TEXT NOT NULL,
  team_id         TEXT NOT NULL,
  secret_id       TEXT NOT NULL,
  version         BIGINT NOT NULL,
  state           TEXT NOT NULL
                  CHECK (state IN ('pending','in_progress','verified','failed')),
  attempt_count   INTEGER NOT NULL DEFAULT 0,
  verified_at     TIMESTAMPTZ,
  error_class     TEXT,
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  PRIMARY KEY (migration_name, team_id, secret_id, version)
);
```

Every immutable secret version (`team_id`, `secret_id`,
`version`) that is in scope for the migration is recorded as
exactly one row in `crypto_migration_checkpoints`. The
`PRIMARY KEY (migration_name, team_id, secret_id, version)`
composite does NOT assume `secret_id` is globally unique
across teams; including `team_id` makes the reconciliation
key unambiguous even when two teams independently register
distinct logical secrets with the same `secret_id`. A
singleton row cannot represent verification for every
immutable secret version; per-row state is the worker's
single source of truth.

The `secret_versions.crypto_provider` column is non-null.
Every existing v0002 row is backfilled to `'local'` by the
migration that adds the column; the
`crypto_migration_checkpoints` table is initialized with
one row per immutable secret version in
`state = 'pending'`.

Every new write through the active provider records the
provider marker in the same transaction; the audit-friendly
invariant is that `crypto_provider` matches the value used
at the most recent successful `Encrypt` of that row.

The migration worker is owned durably by State Registry.
The `crypto_migration_checkpoints` table is the worker's
single source of truth. The checkpoint row is read under
row-level lock when the worker boots and is updated in one
transaction with every successful batch. There is no
separate service, no in-memory-only state that survives a
restart, and no alternative "summary" row.

All other schema elements of v0002 remain unchanged:
`key_id` is still an opaque string of at most 64 characters
matching the `^[A-Za-z0-9][A-Za-z0-9._:-]*$` pattern in the
OpenAPI schema; the public `key_version` is a `>= 1`
integer; the cell is set in the same transaction as the
ciphertext that produced it; the cross-team foreign keys,
the team ownership invariant, and the same canonical
`SecretVersion` response shape are unchanged. v0008
mutates ONLY the cryptographic envelope columns
(`ciphertext`, `key_id`, `key_version`, `crypto_provider`,
`migrated_at`) of `secret_versions` and ONLY under the
documented compare-and-swap envelope rewrite; it NEVER
mutates the `secret_versions` row's `(secret_id, version)`
identity, the canonical `secrets` parent's team ownership, or
any other team-owned column, and it NEVER exposes a new
public HTTP endpoint that mutates those envelope columns.

## References (official)

The implementation MUST conform to the OpenBao Transit
public behavior documented at:

- <https://openbao.org/docs/secrets/transit/> — Transit
  engine and key configuration overview.
- <https://openbao.org/api-docs/secret/transit/> — public REST
  endpoints for `encrypt`, `decrypt`, `keys/:name`,
  `keys/:name/rotate`, `keys/:name/config`, key
  configuration, and the documented ciphertext form.

Documented behavior cited in this design:

- `encrypt` and `decrypt` accept `associated_data` (AAD)
  and authenticate it like a nonce; mismatch returns `400`.
  Cited at
  <https://openbao.org/api-docs/secret/transit/#encrypt-data>.
- `rewrap` accepts only `ciphertext`, `context`,
  `key_version`, `nonce`, `reference`, `batch_input`. It
  does NOT accept `associated_data`. Therefore `rewrap`
  cannot authenticate a v0008 AAD-bound ciphertext; v0008
  does not call `rewrap`. Cited at
  <https://openbao.org/api-docs/secret/transit/#rewrap-data>.
- The default ciphertext form is `vault:v<N>:...`. The
  official OpenBao 2.6 public API does not expose a
  configurable `version_template`; v0008 pins to the
  documented default.
- `keys/:name/rotate` creates a new latest key version;
  the rotate call itself does NOT raise
  `min_encryption_version` or `min_decryption_version`.
  Existing ciphertext from prior versions remains
  decryptable as long as `min_decryption_version` permits.
  Cited at
  <https://openbao.org/api-docs/secret/transit/#rotate-key>.
- `min_decryption_version` and `min_encryption_version` are
  independent floors set through `keys/:name/config`.
  Cited at
  <https://openbao.org/api-docs/secret/transit/#update-key-configuration>.
- `keys/:name` returns the canonical key configuration as
  documented JSON. Cited at
  <https://openbao.org/api-docs/secret/transit/#read-key>.
- Failures are returned as `{ "errors": ["..."] }` HTTP
  4xx / 5xx bodies; the implementation translates every
  documented error class to a State Registry sentinel
  error and never propagates the raw response body.

## Operational references

Operational references that influence but do not constrain
the implementation:

- OpenBao operator deployment guide for production: TLS
  seal, audit devices, and namespace policy examples at
  <https://openbao.org/docs/operations/>.

This design does NOT claim Transit `rewrap` converts local
AES-GCM ciphertext; that conversion is a one-time migration,
not a rotation. This design does NOT claim `rewrap`
authenticates AAD-bound ciphertext; it has no
`associated_data` parameter and SHALL NOT be called by the
State Registry.