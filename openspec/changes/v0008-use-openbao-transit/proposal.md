# Change: v0008-use-openbao-transit

## Why

The State Registry introduced in `v0002-state-registry` encrypts every
stored secret value with a **local AES-256-GCM** provider backed by a
configuration-provided 256-bit data key. That choice gave us a
self-contained implementation in which the State Registry application
and the local key share the same deployment boundary, the key is
loaded from a secure configuration source and is **never** persisted
alongside ciphertext, and PostgreSQL stores ciphertext only. The
remaining trade-off is that every rotation reloads the local key, and
the platform has no cryptographic separation between the State
Registry deployment boundary and the key material.

The v0002 contract explicitly anticipated this trade-off and reserved
the `key_id` / `key_version` envelope on `secret_versions` as an
opaque, provider-neutral seam. v0008 collapses that seam: it
replaces the local AES-256-GCM implementation with OpenBao Transit
as the active in-cluster secret encryption provider while keeping
every public field, HTTP path, error shape, lifecycle invariant,
HMAC scope-token contract, and team authorization rule v0002 ships.

The replacement is opportunistic and correctness-driven, not
cosmetic. The OpenBao 2.6 Transit API at
<https://openbao.org/api-docs/secret/transit/> confirms several facts
that shape this change:

1. `POST /v1/<mount>/encrypt/<key_name>` and
   `POST /v1/<mount>/decrypt/<key_name>` accept `associated_data`
   (`AAD`); `POST /v1/<mount>/rewrap/<key_name>` does **not** accept
   `associated_data`. Every v0008 ciphertext binds `associated_data`
   at encrypt time, so `rewrap` is structurally incompatible with
   any v0008-encrypted ciphertext. v0008 therefore does NOT use
   `rewrap`; rotation is `rotate` only.
2. `POST /v1/<mount>/keys/<key_name>/rotate` creates a new (latest)
   key version for the named key without itself raising
   `min_encryption_version` or `min_decryption_version`. Subsequent
   new encrypt calls that do not specify `key_version` use the new
   latest version. Historical AAD-bound ciphertext remains bound to
   its embedded older key version; it decrypts under that older
   version as long as `min_decryption_version` permits.
3. `min_decryption_version` and `min_decryption_version` are
   independent key-configuration floors, set out of band through
   `POST /v1/<mount>/keys/<key_name>/config`. v0008 SHALL NOT
   advance `min_decryption_version` past any still-referenced
   Transit ciphertext; retiring historical versions requires an
   explicit future migration change and is out of scope for v0008.
4. The documented default ciphertext form is `vault:v<N>:...`; the
   official OpenBao 2.6 public API does not expose a configurable
   `version_template`, so v0008 pins the implementation to that
   documented default and validates the parsed shape strictly.
5. v0008 does NOT introduce a direct `team_id` column on
   `secret_versions`; team ownership is established exclusively
   through the same-team foreign key to the canonical `secrets`
   parent row. v0002's `secret_versions` inherits team through
   `secrets`; the migration worker joins to derive the canonical
   `team_id` rather than assuming a direct column on the version
   row.

The operational consequence is:

- **Provider abstraction is preserved.** The v0002 opaque `key_id` /
  `key_version` envelope becomes the public contract of a generic
  `SecretCryptoProvider` interface; the active implementation is
  OpenBao Transit. A future change MAY swap the implementation again
  behind the same envelope without disturbing callers.
- **Old ciphertext is migrated safely.** OpenBao Transit operates
  only on Transit ciphertext, so local AES-GCM ciphertext cannot be
  rewrap'd or re-encrypted under any documented Transit API path.
  v0008 defines a resumable, idempotent, per-(team_id, secret_id,
  version) reconciliation that decrypts each local row in memory
  with the existing local provider and immediately re-encrypts it
  through Transit, atomically swapping the
  `secret_versions.ciphertext` together with the persisted provider
  marker and refreshed opaque `key_id` / `key_version`. No plaintext
  is ever persisted or logged. Each row's verification is recorded
  in a per-row durable checkpoint inside State Registry.
- **Reads during migration are dual.** Each row carries its own
  persisted `crypto_provider` marker (`local` or `transit`), and
  reads route through the corresponding provider. Open-environment
  reads therefore never force a downtime window; the migration pace
  is independent of read traffic.
- **Rotation does not decrypt historical ciphertext.** Historical
  rows keep their embedded older key version and remain decryptable
  through the active provider using the canonical
  `associated_data` binding; no plaintext is ever produced during
  rotation.
- **Failure posture is non-revealing.** Every invalid or unavailable
  environment open keeps the v0002 single non-revealing `404
environment_unknown_or_unavailable` shape. Provider outages do
  not become visible to operators through response bodies; every
  Transit token, sealed/unavailable response, ACL rejection, mount
  absence, key absence, version absence, tampered ciphertext, or
  wrong `associated_data` returns the same shape with zero
  provider decrypt operations when authorization is denied; the
  provider decrypt operation occurs only after every canonical
  authorization check passes, and there is zero plaintext
  disclosure in any log or audit entry.
- **Cutover is one transactional consistency scan.** There is no
  separate worker-state row, no report table, and no freshness
  timestamp. Cutover checks zero local rows + every migrated row
  has a matching `verified` checkpoint + zero non-verified
  checkpoints + a successful read-key call, all in one
  transaction.

The change is implementation-ready end to end: it ships the provider
abstraction, the OpenBao Transit implementation, the one-time
migration, the rotate-only rotation workflow, the failure-injection
evidence, and the regression boundaries for FIFO claim and HMAC
scope-token verification.

## What Changes

- Introduce a `SecretCryptoProvider` interface in the State Registry
  whose active implementation is an OpenBao Transit provider;
  preserve the public opaque `key_id` and `key_version` fields on
  every `secret_versions` row unchanged.
- Replace the local AES-256-GCM implementation shipped in v0002 with
  a config-driven OpenBao Transit implementation that uses
  `POST /v1/<mount>/encrypt/<key_name>` and
  `POST /v1/<mount>/decrypt/<key_name>` with documented
  `associated_data` binding team identifier, logical secret
  identifier, and version. Persist the exact opaque ciphertext
  returned by Transit; pin the implementation to the documented
  default ciphertext shape `vault:v<N>:...` (where `<N>` is the
  embedded key version) and fail startup or operation if the
  provider returns ciphertext that does not match that shape after
  a strict parse. The opaque public `key_id` is provider-neutral
  and never exposes `key_name`, the Transit mount path, or the
  Transit ciphertext form. Explicitly DO NOT call
  `POST /v1/<mount>/rewrap/<key_name>` — it has no `associated_data`
  parameter and cannot authenticate or return the AAD-bound
  ciphertext v0008 produces. Rewrap is documented at
  <https://openbao.org/api-docs/secret/transit/#rewrap> and is
  intentionally not used; this is a documented Transit endpoint with
  no ability to receive AAD in its payload.
- Configure the Transit key out of band with `aes256-gcm96`,
  `deletion_allowed=false`, `exportable=false`,
  `allow_plaintext_backup=false`, independent
  `min_encryption_version` and `min_decryption_version` floors,
  and a documented automatic-rotation policy that the platform
  never disables; disable implicit key upsert
  (`POST /v1/<mount>/config/keys` with `disable_upsert=true`) and
  require a separate least-privilege operator workflow to create
  the Transit key out of band.
- Define a resumable one-time migration from local AES-256-GCM
  ciphertext to Transit ciphertext:
  - Add a persisted `crypto_provider` column on `secret_versions`
    whose only legal values are `local` and `transit`. Every
    existing row written by v0002 is treated as `'local'` until the
    worker flips it.
  - v0008 does NOT add a direct `team_id` column on
    `secret_versions`; the migration worker derives the canonical
    `team_id` for each row from the canonical same-team `secrets`
    parent row. v0008 does NOT assume `secret_id` is globally
    unique across teams.
  - Add a per-row durable checkpoint table
    `crypto_migration_checkpoints(migration_name, team_id,
secret_id, version, state, attempt_count, verified_at,
error_class, updated_at, PRIMARY KEY(migration_name, team_id,
secret_id, version))` whose `state` is constrained to
    `pending | in_progress | verified | failed`. The table is the
    worker's single source of truth; a row exists for every
    immutable secret version in scope for the migration, NOT a
    singleton row.
  - The worker selects `crypto_provider = 'local'` rows in
    deterministic batches joined from `secret_versions` to the
    canonical `secrets` parent; per row it SHALL verify the
    parent still exists, derive the canonical `team_id` from the
    parent, claim the per-row checkpoint under row-level lock,
    decrypt locally in memory with the v0002 legacy
    `associated_data` byte string
    `{team_id}\x00{secret_id}\x00{decimal_version}`, re-encrypt
    through Transit with the v0008 active byte string
    `team_id=<team_id>\nsecret_id=<secret_id>\nversion=<uint64>\n`,
    and in one transaction that holds a
    row-level lock on both rows, rewrite ONLY the envelope
    columns (`ciphertext`, `key_id`, `key_version`,
    `crypto_provider = 'transit'`, `migrated_at`) of the targeted
    row, mark the checkpoint row `state = 'verified',
verified_at = NOW()`, and append a plaintext-free audit entry.
  - The CAS commit guarantee replaces any provider-once claim:
    if the worker crashes after the Transit call returns but
    before PostgreSQL commits, a retry may invoke the provider
    a second time; the CAS `WHERE crypto_provider = 'local'`
    predicate ensures no already-committed `'transit'` row is
    encrypted again; at most one envelope is committed per
    immutable secret version; discarded provider responses are
    never persisted or logged.
  - Replace v0002's unconditional `secret_versions_append_only`
    `BEFORE UPDATE OR DELETE` trigger with a migration-aware
    envelope-CAS `BEFORE UPDATE` trigger plus an unconditional
    `BEFORE DELETE` rejection. The replacement permits only the
    documented envelope columns to change, only when the migration
    worker has locked the matching per-row checkpoint and installed
    the matching transaction-local migration authorization marker;
    identity-column changes, unclaimed updates, and every delete
    remain rejected.
  - Plaintext exists only in State Registry process memory and
    inside the verified mTLS-protected in-flight request
    accepted by Transit. It is never persisted, never logged,
    never audited, and never transmitted without verified TLS.
  - Cutover is one transactional consistency scan over the
    canonical tables. The scan accepts the cutover when all
    conditions hold atomically: zero `crypto_provider = 'local'`
    rows; every `migrated_at IS NOT NULL` row has a matching
    `verified` checkpoint with `verified_at >= migrated_at`; zero
    rows in `state IN ('pending','in_progress','failed')`; the
    active provider's
    `GET /v1/<mount>/keys/<key_name>` succeeds. There is no
    separate worker-state row, no report table, and no freshness
    timestamp; the absence of `in_progress` rows in the per-row
    checkpoint table is the worker-idle proof. Transit-native
    rows written directly through the active provider carry
    `migrated_at IS NULL`, do NOT require a checkpoint row, and
    are trusted through their successful provider write
    transaction. Cutover does NOT decrypt arbitrary Transit rows
    again and does NOT use `rewrap` as proof.
- Preserve every v0002 public wire detail that an existing client
  or Executor can observe:
  - HTTP paths, methods, parameter names, and JSON shapes under
    `/admin/teams`, `/admin/source-systems`, `/admin/task-types`,
    `/v1/environments/{environment_id}/secrets`,
    `/v1/environments/{environment_id}/secrets/{secret_id}/versions`,
    and `GET /v1/environments/{environment_id}/open?task_id={task_id}`
    are unchanged.
  - OpenAPI field names including `key_id` and `key_version` on
    `SecretVersion`, `LogicalSecret`, `SecretCreateRequest`,
    `SecretReplaceRequest`, `OpenEnvironmentResponse`, and every
    listener, Executor, task, claim, audit, control, environment,
    and scope-token wire schema are unchanged.
  - The non-revealing `404 environment_unknown_or_unavailable`
    error shape used by every invalid or unavailable open remains
    the single shape returned for provider outages,
    sealed/unavailable conditions, authentication failures, ACL
    denials, mount absence, key absence, version absence,
    tampered ciphertext, wrong `associated_data`, and invalid
    scope tokens.
  - FIFO `(ingested_at ASC, task_id ASC)` claim ordering, the
    `pending (no event) -> created -> running -> finished | failed`
    lifecycle, the four-level image precedence, the allow-listed
    HMAC `HS256 / HS384 / HS512` scope-token wire format and
    verification order, and every team authorization rule (including
    support for `scope = system` assigned Executors) are unchanged.
  - The canonical `associated_data` semantic binding remains the
    v0002 triple (`team_id`, `secret_id`, `version`), but the
    provider byte formats are explicitly versioned. Existing local
    rows decrypt with v0002 legacy bytes
    `{team_id}\x00{secret_id}\x00{decimal_version}`; Transit rows
    encrypt and decrypt with v0008 active bytes
    `team_id=<team_id>\nsecret_id=<secret_id>\nversion=<uint64>\n`.
    Migration re-authenticates plaintext under the active format;
    it does not claim the byte strings are equal. The v0008 design
    DOES NOT add a
    `provider_marker` field to AAD; routing is decided by the
    persisted `crypto_provider` column on the row, not by any
    authenticated binding.
- Extend the v0002 plaintext-free logging/audit invariant: no
  Transit token, no Transit request or response body, no plaintext
  secret value, no decrypted environment value, no
  `associated_data` value beyond identifier-level metadata, no
  ciphertext byte, no nonce byte, no authentication tag byte, no
  key material, no derived key byte, no OpenBao namespace, mount
  path, or secret-path component beyond identifier-level metadata,
  and no raw provider error body may appear in application logs,
  audit entries, or error responses. Identifier-level metadata
  (`team_id`, `secret_id`, `version`, opaque `key_id`, opaque
  `key_version`, operation name, outcome status class, and
  `request_id`) MAY be recorded under the same audit rule v0002
  already carries.
- The opaque public `key_id` is a provider-neutral deployment-time
  stable identifier resolved to a `(mount_path, key_name)` pair
  through the secure runtime configuration; it is NOT a PostgreSQL
  lookup table, NOT mutated by `rotate` (rotate changes
  `key_version`, never `key_id`), and never exposed in any public
  field.
- The platform deploys the OpenBao Transit server alongside
  PostgreSQL under mutually authenticated TLS (mTLS) with verified
  peer certificates; State Registry reaches Transit only over
  mTLS; the Transit token is loaded from a secure configuration
  source at startup and is rotated independently of any key
  rotation. Transit credentials never enter logs, audits, or error
  responses.

## Tenant Assumptions

- Every encryption and decryption request to the active provider is
  scoped to one canonical `team_id`, one canonical `secret_id`,
  and one canonical `version`. The associated data passed to the
  provider binds exactly these three identifiers; any mismatch
  during decryption fails the operation closed before any
  plaintext leaves the State Registry.
- v0008 does NOT introduce a direct `team_id` column on
  `secret_versions`; the canonical `team_id` for a
  `secret_versions` row is the canonical `team_id` of the
  same-team `secrets` parent row. Migration verification joins to
  the canonical `secrets` parent row and derives `team_id` from
  it.
- OpenBao Transit credentials (root namespace token, child token,
  or Kubernetes / Vault Agent token) and any Transit `key_name`
  are deployment-time configuration, never request-time input,
  never log payloads, never audit payloads, never error responses.
  The authoritative identity for a decrypted value is the
  canonical `team_id` carried by the request and by the
  persisted row, not the Transit credential.
- The Transit `key_name`, mount path, namespace, and key
  configuration are deployment-time configuration; they are not
  part of the public OpenAPI surface and not present in audit
  entries beyond identifier metadata.
- `crypto_provider = 'local'` routes to the v0002 legacy AAD
  format `{team_id}\x00{secret_id}\x00{decimal_version}`;
  `crypto_provider = 'transit'` routes to the v0008 active format
  `team_id=<team_id>\nsecret_id=<secret_id>\nversion=<uint64>\n`.
  Both authenticate the same canonical identity triple, but they
  are intentionally not byte-identical. The provider marker is
  routing metadata persisted on the row, not authenticated AAD.

## Impact

- Change type: development
- Affected specs: `specs/state-registry/spec.md` (delta —
  modifies the existing "local AES-256-GCM" requirement to a
  generic "pluggable crypto provider" requirement with a
  documented OpenBao 2.6 Transit AAD contract; restates the
  stored-secret requirement with the canonical AAD binding,
  the per-row immutable identity invariant, and the explicit
  rule that v0008 does NOT introduce a direct `team_id` column
  on `secret_versions`; restates the open-environment requirement
  with the canonical AAD binding and zero-decrypt on denial;
  adds a migration-contract requirement whose verification steps
  use the per-row durable checkpoint
  `(migration_name, team_id, secret_id, version)` and the
  canonical `secrets` parent FK without consulting
  `audit_entries`; adds a rotation requirement that documents
  the `rotate`-creates-new-key-version behavior with full
  `/v1/<mount>/...` paths, the canonical default ciphertext
  shape `vault:v<N>:...`, and explicitly documents the
  no-rewrap-of-AAD-bound-ciphertext decision against the official
  Transit API; adds a failure-posture requirement that pins the
  non-revealing `404` shape to every provider failure class;
  leaves every other v0002 requirement untouched)
- Affected ADRs: `specs/adrs.md` (rewritten delta — three proposed
  ADRs: adopt OpenBao Transit as the active in-cluster provider
  behind the opaque `key_id` / `key_version` envelope with the
  canonical v0002 AAD binding; migrate local ciphertext through
  a resumable per-(team_id, secret_id, version) reconciliation
  owned durably by State Registry in PostgreSQL through per-row
  checkpoints; rotate new writes to a new Transit key version
  while retaining historical AAD-bound key versions, NOT calling
  `rewrap` because `rewrap` cannot receive `associated_data`)
- Affected diagrams (all C4 / sequence, no other diagram family):
  `specs/diagrams/state-registry-transit-c4-container.puml`,
  `specs/diagrams/state-registry-transit-secret-write-open-sequence.puml`,
  `specs/diagrams/state-registry-transit-local-migration-sequence.puml`,
  `specs/diagrams/state-registry-transit-rotation-sequence.puml`
  (rotation diagram; replaces the old rotate-rewrap diagram;
  the rotation diagram is renamed and re-scoped to `rotate`
  only)
- Affected test cases: 14 new E2E definitions under
  `specs/test-cases/v0008.<ordinal>-*.md`, numbered contiguously
  from `v0008.1` through `v0008.14`. `v0008.11` is renamed to
  `v0008.11-aad-bound-ciphertext-is-not-rewrapped.md` keeping
  the same ID and proving that historical AAD-bound ciphertext
  rows are NOT sent to `rewrap` and that ordinary rotation does
  not raise `min_decryption_version` past any still-referenced
  version. `v0008.10` is rewritten to prove the rotate-only
  behavior.
- Affected code (future): `svc/state-registry/` introduces
  `internal/crypto/provider.go` with the interface; a
  transit-package implementation using the public Transit REST
  API; a local-package implementation only used during the
  migration window; an in-process migration worker with a per-row
  durable checkpoint table (`crypto_migration_checkpoints`)
  owned by State Registry in PostgreSQL over a persisted
  `crypto_provider` column on `secret_versions`; internal
  CLI/subcommand operators (`cutover`, `rotate`) authenticated
  by the existing deployment identity and not exposed as new
  public HTTP endpoints; a runtime configuration source for
  Transit endpoint, mount path, `key_name`, token, and certificate
  trust anchors; a deployment-time runtime configuration that
  maps the opaque public `key_id` to the Transit mount path +
  `key_name` pair; an audit assertion that no plaintext, Transit
  token, AAD value, ciphertext byte, nonce byte, tag byte, or
  Transit error body ever reaches a log or audit sink; failure
  injection hooks for the regression suite; and OpenBao deployment
  resources (key config, least-privilege policy, mTLS
  certificates). The implementation SHALL NOT call
  `POST /v1/<mount>/rewrap/<key_name>` for any AAD-bound
  ciphertext. v0002 keeps the same envelope, the same wire format,
  and the same authorization path; no v0002 implementation file
  is rewritten.
- Affected OpenAPI: none. `specs/openapi/state-registry.openapi.yaml`
  in v0002 already describes the opaque `key_id` (string) and
  `key_version` (integer) fields in `SecretVersion`; these are
  the exact fields v0008 continues to expose. The Transit
  ciphertext form is internal-only and never appears in any
  schema. The v0002 OpenAPI file is the contract; v0008 adds
  no fields, removes none, and renames none.
- This change does NOT add a new service, a new Env Registry, a
  new Router, or any new public OpenAPI surface. The OpenBao
  Transit server is a deployment-time infrastructure dependency,
  not a new in-cluster platform service listed in the
  connection matrix. Cutover and rotate are
  deployment/operator procedures that run inside the existing
  `state-registry` process under the deployment identity,
  exposed as `state-registry` internal commands, and never as
  new public HTTP endpoints. The existing `/admin/*` family of
  endpoints v0002 already authorizes is unchanged.

## Non-Goals

- Run any task, control any Executor runtime, or change the
  listener ingestion, FIFO claim, `pending (no event) -> created ->
running -> finished | failed` lifecycle, four-level image
  precedence, HMAC scope-token wire format or verification order,
  or any team authorization rule (including `scope = system`
  support).
- Expose Transit credentials, the Transit key name, the Transit
  mount path, the Transit namespace, the Transit API URL, or any
  raw Transit error body through the State Registry's public
  response, audit, or log surface.
- Use `decrypt` + `encrypt` as a rotation path for Transit
  ciphertext, use `decrypt` + `encrypt` to migrate local
  ciphertext, use `rewrap` to migrate local AES-GCM ciphertext,
  use `rewrap` to rotate AAD-bound Transit ciphertext (because
  `rewrap` cannot receive `associated_data`), or ever persist or
  log plaintext.
- Retire historical Transit key versions by raising
  `min_decryption_version` past still-referenced versions; that
  would require an explicit future migration change and is out
  of scope.
- Grant the State Registry Transit privileges to create,
  delete, export, or plaintext-backup a Transit key, enable
  implicit key upsert, or bypass the same `404
environment_unknown_or_unavailable` shape that v0002 already
  pins.
- Add a configurable Transit `version_template`; v0008 pins the
  implementation to the documented default ciphertext shape
  `vault:v<N>:...` at
  <https://openbao.org/api-docs/secret/transit/> and validates
  the parsed shape strictly.
- Add a direct `team_id` column to `secret_versions`; team
  ownership continues to be established exclusively through the
  same-team foreign key to the canonical `secrets` parent row.
- Maintain a run-level worker-state row, a separate report
  table, or a freshness-timestamp artifact for cutover.
- Replace `audit_entries`, the team-filter predicate, the
  WebSocket team binding, the trusted API Gateway model, or the
  Executor registration / discovery surface.
- Introduce any additional diagram family. C4 and sequence
  diagrams are sufficient to communicate the replacement; no
  ER, class, object, activity, state-machine, deployment,
  use-case, BPMN, timing, mind-map, or Gantt diagram is
  requested.
- Treat the Transit per-row encrypted ciphertext as
  rewritable-in-place during ordinary rotation; ordinary
  rotation creates a new (latest) key version and lets new
  writes use it; historical rows remain bound to their embedded
  older key version and decrypt under it.

## Explicit diagram requests

- The four new diagrams in `specs/diagrams/` are C4 container or
  sequence diagrams as named by the v0008-generated
  `diagram-type` header (`c4-container`, `sequence`). No
  restricted diagram family is requested.
