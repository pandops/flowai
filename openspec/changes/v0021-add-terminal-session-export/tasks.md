## 1. Contracts and E2E matrix

- [ ] 1.1 Add `v0021.*` cases for all nine `cancelled | goal_failed | runtime_failed` by target-provider combinations, manual startup, source unavailability, authorization, idempotency, retention, and leakage prevention.
- [ ] 1.2 Update Registry/Gateway/Web UI contracts for handoff source, target generation, package metadata, content streaming, and the `opencode | codex | claude_code` enum.
- [ ] 1.3 Define versioned provider-neutral source, `FLOWAI_HANDOFF.md`, `flowai-handoff.json`, checksums, and three `START.md` templates without executable launchers.

## 2. Source capture and persistence

- [ ] 2.1 Add RED migrations for one immutable source per task, derived-package keys/statuses, cancellation linkage, retention, and rollback compatibility.
- [ ] 2.2 Implement Registry source/package persistence, constraints, private storage, and completed-cancellation source linkage.
- [ ] 2.3 Add RED Docker and K8s tests for completed-cancellation and Goal/runtime failed capture, no-session, unsafe workspace, limits, timeout, retry, cleanup, and unchanged control/lifecycle outcomes.
- [ ] 2.4 Implement duplicated portable-source capture in both OpenHands Executors without shared service code.

## 3. Safe target package generation

- [ ] 3.1 Add RED Registry tests for deterministic OpenCode, Codex, and Claude Code packages from each source kind and unchanged source lifecycle.
- [ ] 3.2 Implement safe source extraction, digest/path/file/size validation, content filtering, fixed templates, atomic packaging, and checksum verification.
- [ ] 3.3 Add adversarial tests proving no native session-ID forgery, executable launcher, secrets, credentials, hidden reasoning, raw judge prompts, or locators enter packages.
- [ ] 3.4 Implement stable idempotency, conflicting-request rejection, partial cleanup, and target-independent package coexistence.

## 4. Authorization, download, and retention

- [ ] 4.1 Add RED same-team/admin generation and download tests plus listener/Executor/foreign non-revealing denials before storage access.
- [ ] 4.2 Implement Gateway/admin generation and streaming routes with content-free audit, sanitized filenames, backpressure, and no redirects.
- [ ] 4.3 Add RED full/range/resumed download, invalid range, integrity failure, concurrent expiry, regeneration, and source-retention tests.
- [ ] 4.4 Implement single-range streaming and reference-safe source/package retention.

## 5. Web UI manual flow

- [ ] 5.1 Add RED UI tests for three target choices, generation progress, available/unavailable/expired states, repeated requests, safe metadata, and new-session disclosure.
- [ ] 5.2 Implement target selection, idempotent generation, Gateway-only download, and manual extract/open/bootstrap guidance.
- [ ] 5.3 Verify UI creates no FlowAI task, direct provider call, local credential request, or synchronization claim.

## 6. Container-native verification

- [ ] 6.1 Produce deterministic cancelled, Goal-failed, and runtime-failed sources in Docker and k3d production-image E2E environments.
- [ ] 6.2 Generate and inspect all nine packages; manually smoke-start a new OpenCode, Codex, and Claude Code session from representative packages without native OpenHands resume claims.
- [ ] 6.3 Run adversarial E2E for tampering, unsafe paths, secret fixtures, duplicate generation, foreign access, range resume, expiry, and source reuse.
- [ ] 6.4 Run affected Go/Playwright/image tests and strict OpenSpec validation for the change and full baseline.
