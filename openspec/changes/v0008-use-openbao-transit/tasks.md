# Tasks

Implementation SHALL proceed in the order below. No GREEN
production edit may begin until its corresponding runnable E2E
test has failed for the expected behavior-specific reason. Every
slice SHALL record the exact RED command and the expected
failure, the exact GREEN command and the observed pass, and
the related-suite result before its implementation task is
checked. REFACTOR may run only while the targeted and related
tests remain green.

The change preserves every v0002 public surface: HTTP paths,
OpenAPI field names and patterns (notably `SecretVersion.key_id`
as a 1–64 character string matching
`^[A-Za-z0-9][A-Za-z0-9._:-]*$` and `key_version` as an integer
`>= 1`), the non-revealing `404 environment_unknown_or_unavailable`
shape, the FIFO `(ingested_at ASC, task_id ASC)` claim ordering,
the lifecycle `pending (no event) -> created -> running ->
finished | failed`, the four-level image precedence, the HMAC
scope-token wire format and verification order, and every team
authorization rule (including the `scope = system` support for
assigned Executors). The change introduces one new internal
column (`secret_versions.crypto_provider`), one per-row durable
auxiliary `crypto_migration_checkpoints` table owned by State
Registry, the active OpenBao Transit provider implementation
behind the existing opaque `key_id` / `key_version` envelope, a
resumable in-process worker that converts local AES-GCM
ciphertext to Transit ciphertext under a CAS commit guarantee,
an audited deployment cutover, and a `rotate`-only rotation
workflow that creates a new latest Transit key version without
decrypting historical rows and without calling `rewrap`.

The implementation SHALL NOT call
`POST /v1/<mount>/rewrap/<key_name>` for any AAD-bound
ciphertext row because the documented Transit rewrap endpoint
has no `associated_data` parameter and cannot authenticate
v0008 ciphertext. `rewrap` is recorded as a documented
endpoint that v0008 intentionally does not use.

Cutover and rotate are NEVER new public HTTP endpoints. They
are internal `state-registry` commands authenticated by the
existing deployment identity. No new endpoint appears under
the OpenAPI surface.

The migration worker mutates ONLY the cryptographic envelope
columns (`ciphertext`, `key_id`, `key_version`,
`crypto_provider`, `migrated_at`) of `secret_versions`. It
NEVER mutates the row's `(secret_id, version)` identity or
the canonical `secrets` parent's team ownership. v0008 does
NOT introduce a direct `team_id` column on `secret_versions`;
the canonical `team_id` is derived from the canonical
same-team `secrets` parent row. Concurrent writers and
readers see either the complete old envelope or the complete
new envelope of any row, never a mixed intermediate state.
Plaintext exists only in State Registry process memory and
inside the verified mTLS-protected in-flight Transit request.
It is never persisted, never logged, never audited, and never
transmitted without verified TLS.

v0008 pins the implementation to the documented default
ciphertext shape `vault:v<N>:...`; the official OpenBao 2.6
public API does not expose a configurable `version_template`,
so v0008 does not configure one. The implementation parses
the documented default strictly and fails startup or
operation if the provider returns ciphertext that does not
match. The opaque public `key_id` is a deployment-time stable
identifier mapped to a `(mount_path, key_name)` pair through
the secure runtime configuration; the mapping is NOT a
PostgreSQL lookup table, NOT a runtime-discovered node, and
NOT mutated by `rotate` (rotate changes `key_version`; the
public `key_id` is stable for the lifetime of the
deployment).

## 0. Implementation-start test activation

- [ ] **RED PREPARATION:** preflight destinations, then move
  `specs/test-cases/v0008.1-*.md` through `v0008.14-*.md`
  unchanged into `autotest/test-cases/` as the first
  implementation mutation; verify all 14 immutable IDs are
  contiguous, no destination collides, and no v0008
  definition remains under the change folder.
- [ ] **RED:** ensure the v0002 harness boots real State
  Registry, PostgreSQL, a configured OpenBao Transit engine
  (with the documented `aes256-gcm96` key created out of
  band), mTLS trust anchors, the v0008 crypto-provider
  descriptor, the local provider for the migration window,
  the in-process migration worker, the deployment cutover
  and rotate internal commands, a sealed/open Transit
  simulator for failure injection, a Transit-call counter,
  and an intercepting client that observes every outbound
  call. Verify a smoke test fails only because the protected
  v0008 behavior is not implemented, not because fixtures,
  certificates, or Transit dependencies are broken.
- [ ] **GREEN:** add one runnable Playwright test for every
  moved definition `v0008.1` through `v0008.14`; each title
  SHALL contain its immutable ID, every request SHALL use a
  supported external HTTP/WS interface, and no test SHALL be
  skipped or replaced by an in-process assertion.
- [ ] **GREEN VERIFY:** run
  `npm --prefix autotest/state-registry test -- --list` and
  require exactly one discoverable runnable test per
  `v0008.<ordinal>` from 1 through 14 with no gaps or
  duplicates.
- [ ] **REFACTOR:** centralize only the team fixtures, the
  Transit simulator, the mTLS identities, the process
  lifecycle, the HTTP/WebSocket clients, and the response-
  shape assertions; rerun the harness smoke/list checks and
  require behavior assertions to remain explicit in each
  test.

## 1. Add the `secret_versions.crypto_provider` column, the per-row `crypto_migration_checkpoints` table, and backfill

- [ ] **RED (migration/integration):** add tagged PostgreSQL
  tests that fail because the new
  `secret_versions.crypto_provider`,
  `secret_versions.migrated_at`, and the new per-row
  `crypto_migration_checkpoints(migration_name, team_id,
  secret_id, version, state, attempt_count, verified_at,
  error_class, updated_at, PRIMARY KEY(migration_name,
  team_id, secret_id, version))` table do not exist; failure
  SHALL name the missing column or table rather than an
  unavailable database fixture.
- [ ] **GREEN:** write a forward-only migration that adds
  `secret_versions.crypto_provider TEXT NOT NULL DEFAULT
  'local' CHECK (crypto_provider IN ('local','transit'))`
  and `secret_versions.migrated_at TIMESTAMPTZ`; creates the
  `crypto_migration_checkpoints` table with the documented
  columns, the `state` check constraint
  `('pending','in_progress','verified','failed')`, and the
  per-row primary key; backfills every existing
  `secret_versions` row to `'local'`; inserts one
  `crypto_migration_checkpoints` row per immutable
  `(team_id, secret_id, version)` tuple in `state =
  'pending'`. The migration SHALL be idempotent so re-running
  it is a no-op.
- [ ] **GREEN VERIFY:** run
  `go test -tags=integration ./state-registry/... -run
  'TestSecretVersionsCryptoProviderBackfill|
  TestCryptoMigrationCheckpointsPerRowSchema'`; require the
  column and table to exist, the constraint to reject any
  value outside the documented set, every `secret_versions`
  row backfilled to `'local'`, the per-row checkpoint PK
  keyed by `(migration_name, team_id, secret_id, version)` to
  refuse duplicate inserts, and an initial row-per-tuple
  baseline on a clean database.
- [ ] **REFACTOR:** isolate the migration into a single SQL
  file and one Go runner; rerun the same tagged command.

## 2. Implement the `SecretCryptoProvider` interface and the deployment-time `key_id` mapping

- [ ] **RED E2E:** implement the harness for `v0008.1` and
  `v0008.2`; run
  `npm --prefix autotest/state-registry test -- --grep
  'v0008\.(1|2)\b'` and require behavior-specific failures
  for the happy-path Transit write / open and the persisted
  opaque envelope.
- [ ] **RED unit/integration:** add tests that prove every
  secret operation routes through the interface and that no
  part of the application code path composes its own
  encrypt / decrypt primitive; add tests that the persisted
  `key_id` pattern and the integer `key_version` range match
  the v0002 OpenAPI unchanged; add tests that prove a
  deployment-time `key_id` -> `(mount_path, key_name)`
  mapping is loaded from the secure runtime configuration
  source at startup and never exposes `key_name` through any
  public field; add tests that `CipherAAD` binds ONLY
  `team_id`, `secret_id`, `version` and has no
  `provider_marker` field; add tests that the transit
  implementation does NOT call
  `POST /v1/<mount>/rewrap/<key_name>` and that the
  intercepting client catches and records every outbound
  call.
- [ ] **GREEN:** define `internal/crypto/provider.go` with
  the `SecretCryptoProvider` interface, the `EncryptedRecord`
  and `CipherAAD` value types (without a `ProviderMarker`
  field), the `ProviderDescriptor` value type (without a
  configurable `version_template` field), and the documented
  sentinel-error set (`provider_unavailable`,
  `provider_forbidden`, `provider_invalid_aad_or_ciphertext`,
  `provider_key_unknown`, `provider_mount_unknown`). The
  interface SHALL accept a canonical `associated_data` byte
  string at both encrypt and decrypt time and SHALL return
  the public opaque envelope only.
- [ ] **GREEN:** route every secret encrypt and decrypt
  call site through the interface; remove the v0002
  application-level AES-256-GCM encrypt code path from the
  production code route. The local provider implementation
  SHALL remain available for the bounded migration window
  only. The public opaque `key_id` SHALL resolve through
  the secure runtime configuration's deployment-time
  `(mount_path, key_name)` mapping without exposing
  `key_name`, the Transit mount path, or the Transit
  ciphertext form through any public response, audit entry,
  or application log. The implementation SHALL NOT include a
  code path that calls
  `POST /v1/<mount>/rewrap/<key_name>`.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright
  command and `go test ./state-registry/... -run
  'TestSecretCryptoProviderRouting|TestEncryptedRecordOpaqueEnvelope|
  TestLocalProviderNotUsedOnActivePath|
  TestKeyIdOpaqueDeploymentConfigDoesNotExposeKeyName|
  TestNoRewrapCallRecorded'`; require the interface to be
  the only call site, the persisted envelope to match the
  v0002 OpenAPI pattern, the local provider to remain a
  migration-only implementation, `key_name` to never appear
  in a public response or audit entry, and `rewrap` to never
  be called.
- [ ] **REFACTOR:** isolate the interface, the two
  providers, and the deployment-time `key_id` mapping behind
  a single registered factory; rerun all crypto unit and
  E2E tests.

## 3. Implement the OpenBao Transit provider

- [ ] **RED E2E:** keep `v0008.1` and `v0008.2` green; add
  the harness for `v0008.3` and `v0008.4`; run
  `npm --prefix autotest/state-registry test -- --grep
  'v0008\.(3|4)\b'` and require behavior-specific failures
  for every denied open that runs zero provider decrypt
  operations and for tampered-ciphertext / wrong-
  `associated_data` that returns the non-revealing 404 shape.
- [ ] **RED unit/integration:** add tests for the documented
  Transit REST behavior:
  `POST /v1/<mount>/encrypt/<key_name>` with
  `{"plaintext": "<b64>", "associated_data": "<b64>"}`; the
  absence of a client nonce; the embedded key version
  mapping to the public integer `key_version` through strict
  parsing of the documented default ciphertext shape
  `vault:v<N>:...`; the `associated_data` byte-for-byte
  equality between encrypt and decrypt; the documented
  error classes translating to the documented sentinel set;
  mTLS to the Transit server with a verified peer
  certificate; the Transit token loaded from a secure
  configuration source and never logged; an explicit test
  that the implementation does NOT call
  `POST /v1/<mount>/rewrap/<key_name>`.
- [ ] **GREEN:** implement
  `internal/crypto/transit/transit.go` with a typed Go
  client using only the documented public Transit REST
  contract. Send
  `POST /v1/<mount>/encrypt/<key_name>` and
  `POST /v1/<mount>/decrypt/<key_name>` with
  `associated_data` set to the canonical
  `team_id\nsecret_id\nversion\n` byte string; pass the
  resulting ciphertext through a strict parser that recovers
  the embedded key version from the documented default
  `vault:v<N>:...` shape and exposes it as the public
  `key_version` integer; refuse to log request or response
  bodies, the `X-Vault-Token` header, the Transit endpoint
  or namespace, the mount path, the `key_name`, or any
  internal `key_id` -> `(mount_path, key_name)` mapping.
- [ ] **GREEN:** map every documented Transit failure class
  to the sentinel-error set the interface requires; verify
  mTLS with a pinned CA bundle; verify the documented
  default ciphertext shape is parsed but never exposed
  through any public field; add a code-level guard that
  `POST /v1/<mount>/rewrap/<key_name>` is never called.
- [ ] **GREEN VERIFY:** rerun the targeted Playwright
  command and `go test ./state-registry/... -run
  'TestTransitEncryptContract|TestTransitDecryptContract|
  TestTransitAssociatedDataEquality|TestTransitFailureMapping|
  TestTransitEmbeddedKeyVersion|TestTransitMTLS|
  TestTransitDocumentedCiphertextShape|
  TestNoRewrapCallRecorded'`; require the tests to fail
  closed on every documented Transit failure class, pass
  the opaque envelope contract, never observe a plaintext
  or Transit token in any log or audit capture, and the
  intercepting client SHALL record zero calls to
  `POST /v1/<mount>/rewrap/<key_name>`.
- [ ] **REFACTOR:** consolidate the typed Transit client
  without rewriting the interface or the sentinel-error
  mapping; rerun all crypto unit and E2E tests.

## 4. Implement the failure-injection and the response-shape invariant

- [ ] **RED E2E:** add the harness for `v0008.5`; run
  `npm --prefix autotest/state-registry test -- --grep
  'v0008\.5\b'` and require behavior-specific failures for
  every provider failure class returning the same non-
  revealing 404 shape with zero provider decrypt operations
  when authorization is denied.
- [ ] **RED unit/integration:** add tests that simulate the
  sealed state, the network-unavailable state, the
  ACL-denied response, the mount-not-found response, the
  key-not-found response, and the version-not-found response;
  each test SHALL assert zero provider operations before
  authorization passes.
- [ ] **GREEN:** implement the test-only Transit simulator
  that returns each documented failure class on demand;
  implement the production failure-to-sentinel mapping for
  every documented failure class; route provider outage
  detail only to plaintext-free operator telemetry with the
  documented outcome status class.
- [ ] **GREEN VERIFY:** rerun targeted Playwright and
  `go test ./state-registry/... -run
  'TestProviderFailureClassMapping|TestProviderSentinelMapping|
  TestNonRevealing404ShapePinning'`; require the identical
  404 response across every failure class and zero provider
  operations on a denied authorization path.
- [ ] **REFACTOR:** centralize the failure-class mapping
  without splitting the sentinel set; rerun all
  failure-injection tests.

## 5. Implement the audit / log scrubber for the new provider

- [ ] **RED E2E:** add the harness for `v0008.14`; run
  `npm --prefix autotest/state-registry test -- --grep
  'v0008\.14\b'` and require behavior-specific failures
  for the scrubber.
- [ ] **RED unit/integration:** add a regex-based scanner
  that matches plaintext secret values, ciphertext byte
  sequences, the Transit ciphertext form (the documented
  default `vault:v<N>:...` shape since the official OpenBao
  2.6 public API does not expose a configurable
  `version_template`), the configured Transit token, the
  configured Transit endpoint, namespace, mount path, and
  key name, the internal `key_id` -> `(mount_path,
  key_name)` mapping path, and any `associated_data` byte
  sequence whose decoded value contains the canonical
  `team_id / secret_id / version` triple.
- [ ] **GREEN:** wire the scrubber into every audit append
  and every log append that touches the new provider; the
  scrubber SHALL drop the forbidden payloads and SHALL
  preserve the identifier-level metadata v0002 already
  permits (`team_id`, `secret_id`, `version`, opaque
  `key_id`, opaque `key_version`, operation name, outcome
  status class, `request_id`).
- [ ] **GREEN VERIFY:** rerun the targeted Playwright
  command and `go test ./state-registry/... -run
  'TestAuditLogScrubberForTransitProvider'`; require the
  scanner to find zero matches in every captured log,
  audit, and response body across the success, migration,
  rotate, and failure-injection scenarios from `v0008.14`.
- [ ] **REFACTOR:** reduce the scrubber to a single typed
  helper called from every audit / log entry point; rerun
  all scrubber-driven tests.

## 6. Implement the in-process migration worker with the per-row durable PostgreSQL checkpoint

- [ ] **RED E2E:** add the harness for `v0008.6`, `v0008.7`,
  and `v0008.8`; run
  `npm --prefix autotest/state-registry test -- --grep
  'v0008\.(6|7|8)\b'` and require behavior-specific
  failures for the local-to-Transit migration, the
  restart-safe idempotent resume from the per-row durable
  checkpoint keyed by `(migration_name, team_id, secret_id,
  version)`, the dual-read of mixed rows, the
  no-public-HTTP-endpoint invariant on the worker, and the
  no-provider-once contract under crash recovery.
- [ ] **RED unit/integration:** add tests for the canonical
  `secrets` parent row verification (the worker SHALL
  verify the canonical `secrets` parent still exists with
  the same team and that the row's persisted `(secret_id,
  version)` identity still resolves on the parent; v0008
  does NOT introduce a direct `team_id` column on
  `secret_versions`, so the canonical team is derived from
  the parent row); the in-memory-only plaintext invariant
  over the verified mTLS-protected in-flight Transit
  request; the per-row CAS envelope rewrite under row-level
  lock keyed by `(migration_name, team_id, secret_id,
  version)`; the per-row checkpoint transactional advance
  through `pending` -> `in_progress` -> `verified` (or
  `failed`) state; the CAS commit guarantee that no
  already-committed `crypto_provider = 'transit'` row is
  encrypted again; the deterministic batch size; the
  assertion that `audit_entries` SHALL never be consulted
  as a source of truth; the assertion that the
  implementation SHALL NOT call
  `POST /v1/<mount>/rewrap/<key_name>` on any row.
- [ ] **GREEN:** implement the in-process migration
  worker inside the existing `state-registry` deployment
  boundary; it SHALL NOT be a separate service. The worker
  SHALL use the per-row `crypto_migration_checkpoints`
  table as its single source of truth; it SHALL read the
  table under row-level lock at boot time and SHALL advance
  the row's `state` in one transaction with every successful
  batch. Per row, the worker SHALL verify that the canonical
  `secrets` parent row still exists with the row's
  `(secret_id, version)` identity and SHALL claim the
  per-row checkpoint under row-level lock keyed by
  `(migration_name, team_id, secret_id, version)`, decrypt
  through the local provider with the canonical
  `associated_data` byte string, encrypt through the active
  provider with the same canonical byte string, and in one
  transaction that holds a row-level lock on both the
  `crypto_migration_checkpoints` row keyed by
  `(migration_name, team_id, secret_id, version)` and the
  targeted `secret_versions` row, rewrite ONLY the envelope
  columns (`ciphertext`, `key_id`, `key_version`,
  `crypto_provider = 'transit'`, `migrated_at`) of the
  targeted row, mark the checkpoint row `state = 'verified',
  verified_at = NOW()`, and append a `secret_version_migrated`
  audit entry with identifier-level metadata only. The
  worker SHALL be idempotent: a row whose state is already
  `verified` is never re-encrypted; a checkpoint advance
  commits per row; a worker restart resumes from the next
  `(team_id, secret_id, version)` row in `state IN
  ('pending','failed')`. The worker SHALL fail closed on
  any provider error and SHALL record the failure in
  `crypto_migration_checkpoints`.
- [ ] **GREEN:** make the read path route by the row's
  persisted `crypto_provider` marker so reads during the
  migration window never block on the worker and never
  exchange plaintext between providers. The public open-
  environment authorization path is unchanged.
- [ ] **GREEN VERIFY:** rerun targeted Playwright and
  `go test ./state-registry/... -run
  'TestMigrationWorkerPerRow|TestMigrationCheckpointPerRowInPostgres|
  TestMigrationRestartIdempotency|TestDualReadMixedRows|
  TestMigrationCASEnvelopeRewriteOnlyEnvelopes|
  TestMigrationCrashBetweenTransitAndCommit'`; require the
  worker to fail closed on every provider error, never
  persist or log plaintext, never duplicate the canonical
  `audit_entries` row, never write the row's identity
  columns, produce the same final state as a single
  uninterrupted run, and on crash recovery produce exactly
  one envelope and one audit row per immutable version.
- [ ] **REFACTOR:** isolate the canonical-verification,
  decrypt, encrypt, persist, and audit steps without
  splitting them across transactions; rerun all migration
  tests.

## 7. Implement the deployment cutover procedure as an internal command

- [ ] **RED E2E:** add the harness for `v0008.9`; run
  `npm --prefix autotest/state-registry test -- --grep
  'v0008\.9\b'` and require behavior-specific failures for
  the cutover consistency scan and the documented rejection
  shape.
- [ ] **RED unit/integration:** add tests that prove the
  cutover internal command is rejected unless every
  consistency-scan condition holds, that the command is
  idempotent, that the command is NOT exposed as a new
  public HTTP endpoint under the OpenAPI surface, that the
  command does NOT decrypt arbitrary Transit rows again,
  that the command does NOT call
  `POST /v1/<mount>/rewrap/<key_name>` as proof, that the
  command's `GET /v1/<mount>/keys/<key_name>` read-key
  call succeeds, that the per-row checkpoint for every
  migrated row keyed by `(migration_name, team_id,
  secret_id, version)` is `state = 'verified'` with
  `verified_at >= migrated_at`, and that no row is left in
  `state IN ('pending','in_progress','failed')` for
  `migration_name = 'local_to_transit'`.
- [ ] **GREEN:** implement the `state-registry crypto
  cutover` internal command authenticated by the existing
  deployment identity. The command runs one immediate
  transactional consistency scan over the canonical tables;
  there is no separate worker-state row, no report table,
  and no freshness-timestamp artifact. The scan accepts the
  cutover when all of the following hold atomically: zero
  rows with `crypto_provider = 'local'`; every
  `migrated_at IS NOT NULL` row has a matching per-row
  checkpoint keyed by
  `(migration_name, team_id, secret_id, version)` in
  `state = 'verified'` with `verified_at >= migrated_at`;
  no rows in `state IN ('pending','in_progress','failed')`
  for `migration_name = 'local_to_transit'` (the absence of
  `in_progress` rows is the worker-idle proof); the active
  provider's
  `GET /v1/<mount>/keys/<key_name>` read-key call succeeds
  with the documented JSON response. Transit-native rows
  (migrated_at IS NULL) do NOT require a checkpoint row and
  are trusted through their successful provider write
  transaction. On acceptance the command sets
  `crypto.local.enabled = false`, appends a `crypto.cutover`
  audit entry with identifier-level metadata only, and
  refuses any subsequent read of a row whose marker is
  `'local'` with the non-revealing `404
  environment_unknown_or_unavailable` shape. The command
  SHALL NOT decrypt arbitrary Transit rows again and SHALL
  NOT call `POST /v1/<mount>/rewrap/<key_name>` as proof.
- [ ] **GREEN VERIFY:** rerun targeted Playwright and
  `go test ./state-registry/... -run
  'TestCutoverInternalCommandOnly|TestCutoverConsistencyScan|
  TestCutoverRejection|TestCutoverGuard'`; require zero new
  public HTTP endpoints, the documented per-row consistency
  scan, and zero plaintext disclosure under every failure
  mode.
- [ ] **REFACTOR:** centralize the cutover internal command
  without rewriting the audit entry; rerun all cutover
  tests.

## 8. Implement the rotation workflow as a `rotate`-only internal command

- [ ] **RED E2E:** add the harness for `v0008.10` and
  `v0008.11`; run
  `npm --prefix autotest/state-registry test -- --grep
  'v0008\.(10|11)\b'` and require behavior-specific failures
  for the new-latest-key-version behavior and the explicit
  no-rewrap-of-AAD-bound-ciphertext behavior.
- [ ] **RED unit/integration:** add tests that prove
  `state-registry crypto rotate` calls
  `POST /v1/<mount>/keys/<key_name>/rotate` with no body
  parameters and receives the documented success response
  that has no response body; that the canonical key gains a
  new latest key version read through
  `GET /v1/<mount>/keys/<key_name>`; that ordinary new
  writes then produce ciphertext embedding the new latest
  version; that historical AAD-bound rows remain unchanged;
  that the intercepting client records zero
  `POST /v1/<mount>/rewrap/<key_name>` calls during the
  entire test; that the rotate call itself does NOT raise
  `min_decryption_version` or `min_encryption_version`;
  and that v0008 issues no
  `POST /v1/<mount>/keys/<key_name>/config` request that
  raises `min_decryption_version` past a still-referenced
  older key version.
- [ ] **GREEN:** implement the `state-registry crypto
  rotate` internal command authenticated by the existing
  deployment identity. The command SHALL call
  `POST /v1/<mount>/keys/<key_name>/rotate` on the
  canonical Transit key named in the deployment
  configuration. The rotate request takes no body
  parameters; the documented success response carries no
  body. The State Registry MAY use a subsequent
  `GET /v1/<mount>/keys/<key_name>` call to read the new
  latest version, but the rotate call itself does NOT need
  to read the key. The rotate command SHALL record the
  audit entry `crypto.rotate` with identifier-level metadata
  only. The implementation SHALL NOT call
  `POST /v1/<mount>/rewrap/<key_name>` on any row; v0008
  SHALL NOT issue any
  `POST /v1/<mount>/keys/<key_name>/config` request that
  raises `min_decryption_version` past a still-referenced
  older key version. The opaque public `key_id` SHALL NOT
  change as a result of a rotate; it is a deployment-time
  stable identifier that maps to a `(mount_path, key_name)`
  pair through the secure runtime configuration.
- [ ] **GREEN VERIFY:** rerun targeted Playwright and
  `go test ./state-registry/... -run
  'TestRotateCreatesNewLatestVersion|TestRotateDoesNotRaiseMdv|
  TestRotateDoesNotRewrap|TestRotateNeverDecryptsHistorical|
  TestRotateInternalCommandOnly'`; also confirm via the
  harness that no new public HTTP endpoint was introduced
  under the OpenAPI surface.
- [ ] **REFACTOR:** isolate the rotate internal command
  without rewriting the audit entry; rerun all rotation
  tests.

## 9. Implement the operation-less OpenBao Transit key out-of-band configuration

- [ ] **RED (infrastructure-as-code + integration):** add
  IaC tests that fail because the OpenBao Transit key
  config has not been written; failure SHALL name the
  missing key config rather than an unavailable Transit
  fixture.
- [ ] **GREEN:** provide a `platform/openbao/` (or
  equivalent deployment-test fixture) that creates the
  Transit key out of band with `aes256-gcm96`,
  `deletion_allowed=false`, `exportable=false`,
  `allow_plaintext_backup=false`, independent
  `min_decryption_version` and `min_encryption_version`
  floors, and a documented automatic-rotation policy;
  provide a least-privilege ACL policy that disallows key
  creation, deletion, export, and plaintext backup; provide
  a deployment-time audit device configuration that scrubs
  plaintext.
- [ ] **GREEN VERIFY:** run
  `go test -tags=integration ./platform/openbao/... -run
  'TestTransitKeyOutOfBandConfig|TestTransitLeastPrivilegePolicy'`;
  require the documented key configuration to be in effect
  and the ACL policy to deny key-creation / deletion /
  export / plaintext-backup requests with the documented
  error class.
- [ ] **REFACTOR:** consolidate the deployment-time
  configuration without weakening the key config; rerun
  the same tagged command.

## 10. Run the v0002 regression suite against the v0008 build

- [ ] **RED:** start the v0008 build and run the v0002 E2E
  definitions `v0002.1` through `v0002.78` plus the v0008
  definitions `v0008.1` through `v0008.14` against the same
  fixture stack; capture failures.
- [ ] **GREEN:** ensure every failure is either an
  unrelated v0008 TDD step that has its own fixture, a
  documented v0008-new-test-case failure, or an upstream
  v0002 fixture issue; close out the v0002 invariants
  `v0002.32` (persistence survives restart), `v0002.45`
  (scope-token security and terminal denial), `v0002.46`–
  `v0002.49` (environments and secrets), `v0002.54`–
  `v0002.59` (system-owned Executor registration,
  discovery, claim, task-event envelope, and scope-change
  rejection), and add new v0008 invariants `v0008.10`
  (rotate creates new latest version without raising
  `min_decryption_version`), `v0008.11` (AAD-bound
  ciphertext is never rewrap'd), `v0008.12` (FIFO claim
  unchanged after Transit provider), `v0008.13` (HMAC
  scope-token unchanged after Transit provider), and
  `v0008.14` (audit/log scrubber) under separate harness
  reports.
- [ ] **GREEN VERIFY:** rerun the harness smoke/list
  checks; require every v0002 and v0008 test to be
  discoverable, runnable, and passing under the v0008
  build.
- [ ] **REFACTOR:** centralize the regression report;
  rerun the harness smoke/list checks and the related-suite
  result.

## 11. Wire the runtime configuration and the secure secrets handling

- [ ] **RED (integration):** add tests that fail when the
  runtime configuration source is missing the Transit
  endpoint, mount path, key name, valid token, or verified
  mTLS trust anchors; failure SHALL name the missing
  configuration key rather than an unavailable fixture.
- [ ] **GREEN:** implement the runtime configuration
  loader that reads the active Transit endpoint, mount
  path, key name, and the documented default ciphertext
  shape expectation from a deployment configuration
  source; load the Transit token from a secure configuration
  source (file mode `0400` or sealed secret); pin the
  Transit peer CA bundle; load the `key_id` ->
  `(mount_path, key_name)` mapping from the same kind of
  source; load every other v0002 secret from the same kind
  of source; refuse startup when any required configuration
  value is missing.
- [ ] **GREEN VERIFY:** run
  `go test ./state-registry/... -run
  'TestRuntimeConfigLoadsTransit|TestStartupFailsClosedWithoutConfig|
  TestTransitTokenLoadedSecurely'`; require all three
  checks to pass and require the resulting startup log and
  audit entries to contain no plaintext, no Transit token,
  no Transit endpoint, namespace, mount path, `key_name`,
  or `key_id` -> `(mount_path, key_name)` mapping.
- [ ] **REFACTOR:** isolate the configuration loader;
  rerun all startup-failure tests.

## 12. Compose the implementation completion report

- [ ] **RED:** confirm every step above is checked; rerun
  validation:
  `npx -y @fission-ai/openspec@1.5.0 validate
  v0008-use-openbao-transit --strict` and
  `~/config/ai/skills/puml-diagrams/puml-verify <file>
  --checkonly` for every new diagram; capture any non-zero
  exit.
- [ ] **GREEN:** fix every validation failure until both
  commands exit 0; record the final report describing the
  validator exit code, the diagram verifier exit code, the
  test harness run, the per-row durable checkpoint state,
  every migrated row count, every internal command
  executed, and every ADR reference.
- [ ] **REFACTOR:** archive the change only after every
  `[ ]` is `[x]` and every required check exits 0; do not
  archive with a failing validator, a failing diagram, an
  unfilled implementation reference, an unchecked task, a
  missing RED evidence line, or a missing GREEN evidence
  line.