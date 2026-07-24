# Proposed ADRs for v0008-use-openbao-transit

## ADR: Adopt OpenBao Transit as the active in-cluster secret-at-rest provider behind the opaque `key_id` / `key_version` envelope

### Status

Proposed

### Context

The v0002 State Registry contract ships a local AES-256-GCM
provider for at-rest secret encryption. The State Registry
application and the local key share one deployment boundary;
the local key is loaded from a secure configuration source
and is never persisted alongside ciphertext, but the platform
has no cryptographic separation between the State Registry
deployment boundary and the key material. The v0002 contract
reserves the opaque `key_id` / `key_version` pair on every
`secret_versions` row precisely so that a future change can
replace the local provider with an in-cluster cryptography
service without disturbing the public surface.

OpenBao Transit is the documented engine for application-side
authenticated encryption without managing key material
directly. It generates the nonce internally, authenticates the
ciphertext, accepts `associated_data`, supports a `rotate`
action that creates a new latest key version for the canonical
key, supports a `rewrap` action that intentionally does NOT
accept `associated_data`, and exposes `min_decryption_version`
and `min_encryption_version` as independent key-configuration
floors. Transit is a deployable infrastructure dependency, not
a new FlowAI service; the connection matrix in the root
AGENTS.md is unchanged.

Directly adopting Transit without the abstraction layer would
lock the contract to the Transit ciphertext form, leak
Transit-specific fields through public responses, and couple
the wire format to the operator's choice of cryptography
service. Adopting the abstraction without a migration would
force a maintenance window for every existing `secret_versions`
row, because Transit `rewrap` does not accept `associated_data`
and cannot authenticate v0002 local AES-GCM ciphertext.
Adopting the abstraction with a maintenance window would
leave every open-environment read blocked until the window
closed, breaking the contract that no Executor may start a
runtime before the Registry returns `200 claimed` and that no
open can fail because of an unrelated migration step.

### Decision

Adopt OpenBao Transit as the active in-cluster secret-at-rest
provider by collapsing the v0002 opaque envelope into a generic
`SecretCryptoProvider` interface; ship the OpenBao Transit
implementation as the configured active provider; preserve
the public opaque `key_id` and `key_version` fields on every
`secret_versions` row; resolve the public opaque `key_id` to
a `(mount_path, key_name)` pair through a deployment-time
runtime configuration without exposing `key_name` or the
Transit ciphertext form through any public field; map every
documented Transit failure class to a State Registry sentinel
error and the same non-revealing `404
environment_unknown_or_unavailable` shape v0002 already pins;
and route every secret write and read through the new
interface. The v0002 HTTP paths, JSON shapes, FIFO claim,
lifecycle, image precedence, scope-token wire format, and team
authorization rules (including the `scope = system` support for
assigned Executors) are unchanged. The canonical
`associated_data` binding for both providers is the
byte-identical `team_id + logical_secret_id + version` triple;
the State Registry does NOT add a `provider_marker` to
`associated_data` because routing is decided by the persisted
`crypto_provider` column on the row, not by any authenticated
binding. v0008 pins the implementation to the documented
default ciphertext shape `vault:v<N>:...` (the official
OpenBao 2.6 public API does not expose a configurable
`version_template`).

### Consequences

- Every encrypted secret continues to bind team identifier,
  logical secret identifier, and secret version in the
  active provider's `associated_data`; the binding becomes
  the contract seam. Both providers accept the same byte
  string for the same canonical input triple.
- The State Registry resolves the opaque `key_id` to a
  `(mount_path, key_name)` pair through a deployment-time
  runtime configuration rather than a PostgreSQL lookup
  table. The mapping is never exposed in a response, audit
  entry, or log.
- OpenBao Transit becomes a deployment-time infrastructure
  dependency with documented key configuration, ACLs, mTLS,
  and rotation.
- The Registry expects the same single non-revealing `404
  environment_unknown_or_unavailable` shape to cover every
  provider failure class; provider outages do not propagate
  to clients.
- Local ciphertext written by v0002 requires a separate
  migration step covered by the migration-cutover ADR.
- Existing v0002 E2E tests that cover the public contract
  (FIFO claim, lifecycle, image precedence, scope-token
  verification, plaintext-free audit, system-scope Executor
  support) remain valid because the public surface is
  unchanged.
- Logging and audit scrub every plaintext secret value,
  ciphertext byte, `associated_data` value, Transit token,
  Transit endpoint, namespace, mount path, key name, internal
  `key_id` -> `(mount_path, key_name)` mapping, and raw Transit
  error body before any record is written.
- v0008 does NOT introduce a configurable
  `version_template`; the implementation pins to the
  documented default `vault:v<N>:...` shape and parses it
  strictly.

### More Information

- <https://openbao.org/docs/secrets/transit/> — Transit
  engine overview.
- <https://openbao.org/api-docs/secret/transit/> — public REST
  endpoints for `encrypt`, `decrypt`, `keys/:name`,
  `keys/:name/rotate`, `keys/:name/config`, key configuration,
  and the documented ciphertext form.
- <https://openbao.org/api-docs/secret/transit/#encrypt-data>
  — `associated_data` acceptance for encrypt and decrypt.
- <https://openbao.org/api-docs/secret/transit/#rewrap-data>
  — rewrap accepts only `ciphertext`, `context`,
  `key_version`, `nonce`, `reference`, `batch_input`; it has
  no `associated_data` parameter.
- `openspec/changes/v0002-state-registry/specs/state-registry/spec.md`
  — v0002 requirements on which v0008 builds.
- `openspec/changes/v0008-use-openbao-transit/specs/state-registry/spec.md`
  — v0008 requirements that name every provider-boundary
  invariant.

## ADR: Migrate v0002 local AES-256-GCM ciphertext through a resumable per-(team_id, secret_id, version) reconciliation owned durably by State Registry in PostgreSQL

### Status

Proposed

### Context

OpenBao Transit operates only on Transit ciphertext under
the documented API; in particular `rewrap` cannot accept
`associated_data` and therefore cannot authenticate v0002
local AES-GCM ciphertext. The Registry already serves both
kinds of rows because v0002 wrote every existing
`secret_versions` row with the local provider. v0008 does
NOT introduce a direct `team_id` column on `secret_versions`;
team ownership of every `secret_versions` row is established
exclusively through the same-team foreign key to the
canonical `secrets` parent row. Three strategies were
considered:

1. **Maintenance window cutover.** Stop the State Registry,
   run a batch decrypt+encrypt script against every row,
   restart the Registry with Transit enabled. Risks:
   extended downtime, no safe-batch checkpoint, no in-flight
   open support, no programmatic cutover gate, and a brief
   window during which every assignee is blocked from
   running because the open endpoint refuses reads.
2. **Drop the migration, force a fresh install.** Requires
   every existing secret to be re-entered by an operator and
   breaks the contract that secret history is immutable.
3. **Resumable per-(team_id, secret_id, version) reconciliation
   with a persisted provider marker AND a durable per-row
   in-PostgreSQL migration checkpoint.** Add
   `crypto_provider` to `secret_versions`; let the Registry
   serve reads from whichever provider matches the row; run
   an in-process migration worker inside the
   `state-registry` deployment boundary that joins
   `secret_versions` to the canonical `secrets` parent,
   derives the canonical `team_id` from the parent,
   decrypts locally, encrypts through Transit with the
   canonical `associated_data` byte string, atomically
   rewrites ONLY the cryptographic envelope columns of the
   row in one transaction under row-level lock, and appends
   the durable per-row checkpoint and audit entry. The
   reconciliation key is `(migration_name, team_id,
   secret_id, version)` so `secret_id` is NOT assumed to be
   globally unique across teams.

The v0002 contract declares that no Executor may start a
runtime before a successful `200 claimed`, that open-
environment reads must fail closed, and that the open-
environment response is the single shape for every denial.
A maintenance-window cutover would temporarily fail every
open read and break that invariant. Strategy 3 keeps the
invariant intact because every read routes by the row's
persisted marker; the operator can pause the worker at any
time without breaking live traffic.

### Decision

Adopt the resumable per-(team_id, secret_id, version)
reconciliation. Persist a new `crypto_provider` column on
`secret_versions` whose only legal values are `local` and
`transit`; backfill every existing row to `'local'`; the
writer sets the row's `crypto_provider` to `'transit'` in the
same transaction as the new ciphertext and `key_id` /
`key_version`. The migration worker is an in-process worker
inside the existing `state-registry` deployment boundary,
not a separate service, and it owns a per-row
`crypto_migration_checkpoints(migration_name, team_id,
secret_id, version, state, attempt_count, verified_at,
error_class, updated_at, PRIMARY KEY(migration_name, team_id,
secret_id, version))` table inside the same PostgreSQL
database. The worker selects `crypto_provider = 'local'`
rows in deterministic batches joined to the canonical
`secrets` parent; for each row it verifies the parent still
exists with the same `team_id` and that the row's persisted
`(secret_id, version)` still resolves on that parent, claims
the per-row checkpoint under row-level lock, decrypts
through the local provider with the canonical `associated_data`
byte string, encrypts through the active provider with the
same canonical byte string, and in one transaction that
holds a row-level lock on both the `crypto_migration_checkpoints`
row keyed by `(migration_name, team_id, secret_id, version)`
and the targeted `secret_versions` row, rewrites ONLY the
envelope columns (`ciphertext`, `key_id`, `key_version`,
`crypto_provider = 'transit'`, `migrated_at`) and marks the
checkpoint row `state = 'verified'`. The worker never
consults `audit_entries` as an authorization source or a
source of truth; `audit_entries` is append-only evidence
only. The cutover operator procedure that disables the
local provider is an audited deployment/operator procedure
inside the existing `state-registry` deployment boundary;
it is exposed as a `state-registry` internal command
authenticated by the existing deployment identity and is
never a public HTTP endpoint under the OpenAPI surface.
The procedure is one immediate transactional consistency
scan over the canonical tables; there is no separate
worker-state row, no report table, and no freshness-
timestamp artifact. The absence of `in_progress` rows in
the per-row checkpoint table is the worker-idle proof.

### Consequences

- Reads route by the persisted marker during the migration
  window; no maintenance window is required; live traffic
  is never blocked.
- The migration worker is fail-closed on provider error and
  records the failure in `crypto_migration_checkpoints`.
- Plaintext exists only in State Registry process memory
  between the local decrypt step and the active encrypt
  step; it travels only inside the verified mTLS-protected
  in-flight request accepted by Transit; it is never
  persisted, never logged, never audited, never returned to
  any operator. The invariant is: plaintext exists only in
  State Registry process memory and inside the verified
  mTLS-protected in-flight request accepted by Transit; it
  is never persisted, never logged, never audited, and
  never transmitted without verified TLS.
- The migration worker's durable state lives entirely
  inside PostgreSQL; there is no in-memory-only state that
  survives a restart and no separate service or process;
  per-row state proves verification for every immutable
  secret version rather than collapsing it into a single
  boolean; the composite key `(migration_name, team_id,
  secret_id, version)` does NOT assume `secret_id` is
  globally unique across teams.
- The logical secret identity (`secret_id`, `version`) and
  the canonical `secrets` parent's team ownership are
  immutable for the row's lifetime; the migration worker
  rewrites ONLY the cryptographic envelope columns of the
  row and never the identity columns. Concurrent writers
  and readers see either the complete old envelope or the
  complete new envelope of any row because every rewrite
  runs in one transaction under a row-level lock.
- The cutover procedure is the only path that disables the
  local provider; the worker never disables it implicitly.
- The cutover procedure does not decrypt arbitrary Transit
  rows again and does not use `rewrap` as proof; new
  Transit-native rows are trusted through their successful
  write path and the per-row `verified` checkpoint
  committed in the same transaction as the envelope
  rewrite.
- The local provider implementation MAY be removed in a
  later change after a documented deprecation cycle, but
  v0008 ships it.

### More Information

- <https://openbao.org/api-docs/secret/transit/#rewrap-data>
  — rewrap accepts only `ciphertext`, `context`,
  `key_version`, `nonce`, `reference`, `batch_input`.
- <https://openbao.org/docs/secrets/transit/> — Transit
  engine, `rotate`, key versioning, and per-row data.
- `openspec/changes/v0008-use-openbao-transit/design.md`
  — migration contract, batch boundaries, per-row durable
  checkpoint, and cutover consistency scan.
- `openspec/changes/v0008-use-openbao-transit/specs/state-registry/spec.md`
  — migration requirement, durable per-row checkpoint
  requirement, and cutover consistency-scan requirement.

## ADR: Rotate new writes to a new Transit key version while retaining historical AAD-bound key versions; do not rewrap AAD-bound ciphertext

### Status

Proposed

### Context

OpenBao Transit exposes rotation operations
`POST /v1/<mount>/keys/<key_name>/rotate`, which creates a
new latest key version, and
`POST /v1/<mount>/rewrap/<key_name>`, which upgrades
existing Transit ciphertext to a target version without
returning plaintext.

The documented constraints are:

- `rotate` creates a new (latest) key version. It does NOT
  raise `min_encryption_version` or `min_decryption_version`
  by itself. Subsequent encrypt calls that do not specify
  `key_version` use the new latest version. Existing
  ciphertext from prior versions remains decryptable as
  long as `min_decryption_version` permits. Cited at
  <https://openbao.org/api-docs/secret/transit/#rotate-key>.
- `rewrap` accepts only `ciphertext`, `context`,
  `key_version`, `nonce`, `reference`, `batch_input`. It
  has NO `associated_data` parameter. Therefore rewrap
  CANNOT authenticate v0008 ciphertext, because every
  v0008 ciphertext binds `associated_data` at encrypt time.
  Cited at
  <https://openbao.org/api-docs/secret/transit/#rewrap-data>.
- `min_decryption_version` and `min_encryption_version`
  are independent key-configuration floors set out of
  band through
  `POST /v1/<mount>/keys/<key_name>/config`. Cited at
  <https://openbao.org/api-docs/secret/transit/#update-key-configuration>.

A naive rotation workflow could call `decrypt` followed by
`encrypt` on every row to upgrade ciphertext to a new key
version. That workflow would briefly hold plaintext in
memory per row, violating the v0002 invariant that plaintext
exists only in memory while serving an authorized open-
environment response and never on a rotation path, and
rewrap is structurally incompatible with the AAD-bound
ciphertext v0008 produces.

`rewrap` is therefore intentionally NOT used by v0008. It
remains a documented Transit endpoint at
<https://openbao.org/api-docs/secret/transit/#rewrap-data>;
v0008 records its incompatibility with the AAD-bound
ciphertext v0008 produces as a non-goal at the contract
level so the constraint is not silently violated by a
future change.

### Decision

Use Transit `rotate` to create a new latest Transit key
version. After a successful rotate, new encrypt calls that
do not specify `key_version` use the new latest version;
historical AAD-bound ciphertext rows remain bound to their
embedded older key version and decrypt under that older
version through the active provider. `min_decryption_version`
and `min_encryption_version` remain independent key-
configuration floors; v0008 SHALL NOT advance
`min_decryption_version` past any key version still
referenced by an AAD-bound ciphertext row. Retiring
historical key versions requires an explicit future
migration change and is out of scope for v0008. Never use
`rewrap` on AAD-bound ciphertext (it cannot authenticate
v0008 ciphertext). Never use `decrypt` followed by `encrypt`
on AAD-bound ciphertext (it would briefly hold plaintext in
memory and violates the no-plaintext-on-rotation invariant).
The rotate procedure is exposed as a `state-registry`
internal command authenticated by the existing deployment
identity and never as a new public HTTP endpoint under the
OpenAPI surface. The opaque public `key_id` does not change
as a result of a rotate; it is a deployment-time stable
identifier that maps to a `(mount_path, key_name)` pair
through the secure runtime configuration.

### Consequences

- Ordinary rotation never holds plaintext in memory; the
  no-plaintext-on-rotation invariant holds under v0008.
- Existing v0002 rows whose marker is `local` cannot be
  rewrap'd (rewrap cannot authenticate them anyway) and
  remain under the migration worker until they are
  individually converted by the migration contract.
- Historical Transit key versions may persist on disk for
  arbitrarily long because no operator action can be
  casually issued through v0008 to retire them. Retiring
  historical versions requires an explicit future migration
  change that records a new contract delta.
- Operators must understand that `crypto.rotate` and the
  migration are different mechanisms with different audit
  actions; rotate is its own audit action and never mutates
  data rows.
- A future change MAY swap the provider again behind the
  same interface; the `decrypt + encrypt` anti-pattern and
  the documented `rewrap` incompatibility with AAD-bound
  ciphertext are recorded at the contract level rather than
  as OpenBao-specific implementation notes.

### More Information

- <https://openbao.org/api-docs/secret/transit/#rotate-key>
  — rotate creates a new latest key version; rotate does
  not itself raise `min_encryption_version`.
- <https://openbao.org/api-docs/secret/transit/#rewrap-data>
  — rewrap accepts only `ciphertext`, `context`,
  `key_version`, `nonce`, `reference`, `batch_input`; it
  does NOT accept `associated_data`, so rewrap cannot
  authenticate v0008 ciphertext.
- <https://openbao.org/api-docs/secret/transit/#update-key-configuration>
  — independent `min_decryption_version` /
  `min_encryption_version` floors set out of band.
- <https://openbao.org/api-docs/secret/transit/#read-key>
  — key read for cutover verification.
- `openspec/changes/v0008-use-openbao-transit/specs/state-registry/spec.md`
  — requirement on the rotate-only behavior, the
  compatibility with `min_decryption_version`, and the
  explicit `rewrap` non-goal.
- `openspec/changes/v0008-use-openbao-transit/design.md`
  — rotation workflow and audit format.