# Tasks

- [ ] Create ADR choosing Env Registry database technology and migration approach before schema implementation.
- [ ] Create ADR choosing secret encryption/KMS strategy before storing ciphertext.
- [ ] Implement PostgreSQL schema for env metadata, secret metadata, ciphertext, and audit entries.
- [ ] Implement store surface for non-secret env vars and secret values.
- [ ] Implement encryption for secret values at rest.
- [ ] Implement open-env surface with scope-token validation.
- [ ] Add Env Registry backend tests in the selected programming language.
- [ ] Add Env Registry integration tests in the selected programming language for PostgreSQL persistence, encryption, store, and open-env APIs.
- [ ] Verify plaintext secrets are not logged or returned to non-Executor callers.
- [ ] Render proposed diagrams and keep only `.puml` sources.
- [ ] Run `npx -y @fission-ai/openspec@1.5.0 validate v0003-env-registry --strict --no-interactive`.
