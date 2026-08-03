# ADR: Sign scope tokens with an allow-listed HMAC and rotate server-controlled keys

### Status

Accepted

### Context

State Registry issues short-lived scope tokens that authorized Executors present at `GET /v1/environments/{environment_id}/open?task_id={task_id}`. Those tokens gate decrypted environment and secret access through the local AES-256-GCM provider, so a token forgery, replay, or post-terminal reuse would directly expose secret plaintext. The tokens must remain verifiable without round trips to an external service, must be bound to the authenticated assigned Executor, must resist replay after terminal task state or unassignment, and must allow graceful key rotation without weakening audit or operational logging safety.

The platform needs a token security model that is compact, deterministic, and auditable, with explicit guarantees for algorithm strength, key control, claim verification, comparison safety, replay handling, rotation, and logging.

### Decision

State Registry SHALL be the sole issuer and verifier of open-environment scope tokens. Each token SHALL be signed using an allow-listed HMAC algorithm from the set `HS256`/`HS384`/`HS512` (the "HMAC-SHA-256 or a stronger HMAC" family), keyed with a State Registry-controlled key selected by a `key_id` claim included in the payload. The compact three-part wire format is `<header>.<payload>.<signature>`; the protected header SHALL carry `alg` (allow-listed), `kid`, and `typ`; the payload SHALL carry `team_id`, `project_id` (required claim; nullable only when the canonical environment has no project scope), `task_id`, `environment_id`, `executor_id`, `audience` (literal `state-registry.environment.open`), `issued_at`, `expiry`, and `key_id`. The protected-header `kid` SHALL equal the payload `key_id`. `issued_at` SHALL satisfy `issued_at <= server_now + 30 seconds`, `expiry` SHALL be strictly later than `issued_at`, and `expiry - issued_at` SHALL NOT exceed five minutes.

State Registry SHALL verify that the protected-header `kid` equals the payload `key_id` before any MAC computation, SHALL recompute the signature under the declared allow-listed algorithm using the server-side key handle, SHALL compare the result with a constant-time comparison before any other check, SHALL accept only `key_id` values inside the documented active key window for HMAC rotation, SHALL require an `audience` claim that matches the documented literal identifier `state-registry.environment.open`, SHALL perform canonical claim verification (presence, type, encoding, and allowed values for every claim, including that `project_id` is required but nullable only when the canonical environment has no project scope) before any team, assignment, applicability, or decrypt operation, and SHALL never log token plaintext, individual claim values, MAC bytes, key material, or derived key bytes in application logs, audit records, or error responses.

A token SHALL be accepted only when the calling authenticated Executor identity is the currently assigned Executor for the referenced task and the referenced task is in a non-terminal state. State Registry SHALL reject any other identity, a terminal task state, or an unassigned Executor before any decrypt operation. State Registry MAY allow a retry of the same token within its TTL by the same assigned Executor; the retry SHALL NOT bypass canonical claim, transition, or assignment checks, SHALL NOT extend TTL, and SHALL NOT revive an expired token.

Key material SHALL be loaded from a secure configuration source. State Registry SHALL document an active key window: each `key_id` maps to a server-controlled key, retired keys SHALL be accepted only during a documented overlap period for rotation, and key material SHALL NOT be persisted alongside token material or logs. Tokens SHALL be issued on atomic successful claim; re-issuance after a transition that invalidates a token SHALL require a fresh successful claim and SHALL NOT reuse the prior `key_id`-signed envelope.

### Consequences

Positive consequences:

- Allow-listed HMAC (`HS256`/`HS384`/`HS512`) with a constant-time comparison gives a stable, well-analyzed algorithm family and removes timing-side-channel exposure during verification.
- Binding every token to a `key_id` with a documented active window lets State Registry rotate keys without breaking in-flight tokens and without admitting forged or retired-key tokens.
- The five-minute expiry ceiling and required `audience` claim minimize replay opportunity and let multiple cooperating services reject tokens minted for a different verifier.
- Canonical claim verification before any team, assignment, applicability, or local AES-256-GCM decrypt step ensures malformed or unexpected tokens cannot trigger downstream state reads or decryption.
- Denying tokens for terminal task state, unassigned Executors, or different Executor identities before any decrypt operation keeps secret access strictly within authorized task windows.
- Banning token plaintext, claim values, MAC bytes, and key material from logs and audit records keeps the most sensitive token and key data out of long-lived observability surfaces.
- Allowing same-identity retry within TTL without bypassing canonical checks improves Executor robustness against transient network or restart failures without weakening security.

Negative consequences:

- State Registry becomes the sole token verifier and must own the documented active key window, the configuration source for keys, and the rotation policy.
- The five-minute TTL requires Executors to refresh tokens promptly after a successful claim and may complicate retries that span task reassignment or restart; explicit recovery flows remain necessary.
- Storing key material separately from token data and ensuring no logging of sensitive material adds configuration, deployment, and CI hygiene requirements.
- A documented active key window for rotation adds operational coordination between key provisioning, deployment, and Registry restarts.
- Constant-time MAC verification and canonical claim checking add small per-request CPU costs compared with non-verifying paths, but these costs are acceptable for open-environment traffic.

Failure isolation:

- When State Registry cannot verify a token (expired, foreign team, foreign identity, terminal task state, retired `key_id`, malformed claim, or constant-time MAC failure), it SHALL reject the request closed without any decrypt operation and SHALL append a plaintext-free audit entry.
- When the AES-256-GCM provider fails (missing key, bad associated data, invalid tag), the Registry SHALL still perform token verification and the audit append; only the decrypt step depends on provider availability.
- Logs and audit records SHALL contain only permitted identifier metadata, decision, and outcome; never token plaintext, individual claim values, MAC bytes, key material, or derived key bytes.

## More Information

Supersedes: `v0002-state-registry`
