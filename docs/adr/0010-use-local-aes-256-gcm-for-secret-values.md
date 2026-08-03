# ADR: Use local AES-256-GCM authenticated encryption for at-rest secret values

### Status

Accepted

### Context

State Registry must store immutable secret versions while preventing plaintext persistence, logging, operator reads, cross-team access, and access by unassigned Executors. The encryption-key service must support centralized policy, rotation, auditability, and free self-hosting without binding FlowAI to one public cloud.

Earlier drafts of this contract used OpenBao Transit as the cryptographic key service. The revised contract replaces that provider with local AES-256-GCM authenticated encryption backed by a configuration-provided 256-bit key, while keeping the wire formats and the public HMAC scope-token model unchanged. The change allows the platform to ship and test without depending on an external cryptography service, while leaving a future change free to replace the local provider behind the same opaque `key_id` / `key_version` envelope.

FlowAI needs at-rest secret encryption that operates without a network dependency, keeps ciphertext and key material on the same trust boundary as the canonical database, and survives key rotation through the same opaque identifier pair that consumers of the Registry already see.

### Considered Options

#### Local AES-256-GCM authenticated encryption

The Registry encrypts each secret value with AES-256-GCM using a configuration-provided 256-bit data key, a fresh random 96-bit nonce per encryption, and a 128-bit authentication tag. The authenticated associated data binds at minimum the team identifier, the logical secret identifier, and the secret version. The opaque `key_id` and `key_version` recorded on each `secret_versions` row allow rotation; the consumer code path remains unchanged when the provider changes.

Advantages:

- No external cryptography service; the Registry and the key live on the same trust boundary.
- AES-256-GCM is a standard, well-analyzed authenticated encryption mode.
- Configuration-provided key rotation through opaque identifiers allows the platform to replace the provider later without changing the contract.
- The authenticated associated data binding prevents silent misuse of a ciphertext outside its team / logical secret / version.

Disadvantages:

- Database compromise exposes the key alongside ciphertext unless additional separation is added later.
- Application-level key handling increases the chance of custom cryptographic mistakes; mitigation requires audited code paths and reference tests.
- The Registry fails closed on startup when the key is missing.

#### OpenBao Transit (previously selected)

Strong cryptographic separation from PostgreSQL, durable multi-node storage, and audit devices at the cost of an extra network service.

Rejected for v0002 implementation because the platform is not yet ready to deploy an external key service in this change. A future change MAY re-introduce a dedicated provider behind the same `key_id` / `key_version` envelope.

### Decision

State Registry SHALL encrypt every secret value with local AES-256-GCM authenticated encryption. The data key SHALL be a configuration-provided 256-bit secret loaded from a secure configuration source, never logged, never persisted alongside ciphertext, and rotated through opaque `key_id` / `key_version` envelopes recorded on each immutable `secret_versions` row. Each encryption call SHALL generate a fresh random 96-bit nonce and SHALL compute a 128-bit authentication tag. The authenticated associated data SHALL bind at minimum the team identifier, the logical secret identifier, and the secret version; missing or mismatched binding SHALL cause decryption to fail before any plaintext leaves the Registry.

The Registry SHALL refuse decryption when the configured key is missing, the key identifier is outside the documented active window, the associated data binding does not match canonical records, the nonce or tag is invalid, or the decrypted plaintext fails any canonical authorization check. Startup SHALL fail closed when the AES-256-GCM key is missing; thereafter, secret writes and open-environment reads SHALL fail closed on every error. Application logs, audit records, and error responses SHALL NOT contain plaintext secret values, key material, key bytes, nonce bytes, ciphertext bytes, authentication tag bytes, individual associated-data values beyond identifier-level metadata, or any derived key bytes. The State Registry SHALL NOT depend on an external cryptography service to encrypt or decrypt secret values. A future change MAY replace the local provider behind the same opaque `key_id` / `key_version` envelope without altering this contract.

### Consequences

Positive consequences:

- One well-known authenticated encryption mode provides confidentiality, integrity, and binding of ciphertext to team/secret/version.
- No external cryptography dependency means the contract is implementable in a single trust boundary.
- Fail-closed behavior on missing key, bad associated data, or invalid tag prevents silent fallback to plaintext.
- The opaque `key_id` / `key_version` envelope allows a future migration to an external provider without a contract change.

Negative consequences:

- Compromise of the configuration that holds the AES key exposes all ciphertexts; this is the trade-off accepted by removing an external cryptography service.
- The Registry must produce authenticated associated data on every encryption call and validate it on every decryption call, increasing per-operation cost.
- Startup and decrypt failures must be carefully logged to avoid disclosing key material, nonce, ciphertext, or plaintext while still allowing operators to diagnose outages.
- Future rotation requires loading multiple keys by `key_id`; the Registry must manage that active window correctly.

Failure isolation:

- When the configured key is missing or invalid, the Registry SHALL refuse secret writes and open-environment reads closed without falling back to plaintext.
- All logs and audit entries SHALL contain only permitted identifier metadata, decision, and outcome; never plaintext, nonce, ciphertext, authentication tag, key material, or derived key bytes.

Implementation follow-up:

- Define the configuration source for the 256-bit AES-GCM key, the active key window, and rotation procedure before production deployment.
- Implement a typed Go AES-GCM provider with constant-time tag verification, a cryptographically secure random nonce source, key rotation compatibility, bounded retries, and no plaintext or key logging.
- Add integration tests proving encryption and decryption, key rotation compatibility, fail-closed behavior, same-team authorization, foreign-team denial before any decrypt operation, and recovery of older ciphertext versions.

## More Information

Supersedes: `v0002-state-registry`
