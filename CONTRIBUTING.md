# Contributing to FlowAI

Start with the [README](README.md), [documentation index](docs/README.md), and
[repository conventions](AGENTS.md). Check the [planned changes](docs/README.md#planned-changes)
and [archive](docs/README.md#archived-changes) before proposing overlapping work.

## Development setup

Use a POSIX shell with Bash, Git, the Go toolchain declared in [go.mod](go.mod),
Node.js/npm, and a Docker-compatible runtime available through the `docker` CLI.
The E2E runner uses Node's `import.meta.dirname`; use a Node version that supports
it and the installed Playwright version.

From the repository root:

```bash
npm ci
env -u GOROOT go mod download
make install-tools
make install-hooks
```

`make install-tools` installs the pinned golangci-lint and pulls the pinned
Gitleaks and TruffleHog images. Make sure the Go binary installation directory
is on `PATH`. `make install-hooks` configures this checkout to use `.hooks/`.
The pre-commit hook scans staged changes for secrets and runs formatting and
lint checks. Never commit real credentials, decrypted environments, or local
runtime data.

## Service boundaries

- Keep concrete Executors under `executor/` and other runtime services under `svc/`.
- Keep code, configuration, migrations, clients, and fakes within the owning service. Do not import another service's internals or introduce shared application code.
- Use the single root Go module. Keep cross-service tests under `qa-e2e/` and exercise public interfaces.
- Build and run real services from their own production `Containerfile` in integration and E2E environments. Host package tests are appropriate when they do not launch runtime services; in-process fakes remain test fixtures.
- Preserve State Registry ownership, team isolation, FIFO claim semantics, and the Web UI → Gateway → Registry boundary.

## Proposing a change

For behavior, API, persistence, or topology changes, start a numbered OpenSpec
change under `openspec/changes/`. Check both active and archived IDs before
choosing the next `vNNNN-verb-noun` identifier.

Keep proposals and spec deltas in that change until implementation, verification,
acceptance, and sync. Before implementing a development change, prepare its
design, tasks, proposed decisions/diagrams, and requirement-linked E2E test-case
definitions. A backlog-only proposal is not implementation-ready.

Move E2E definitions from the change's `specs/test-cases/` to
`qa-e2e/test-cases/` when implementation starts, preserving their
`vNNNN.ordinal-name.md` IDs. Write the corresponding executable E2E tests and
establish a behavior-specific failing result before implementing the behavior;
then implement and run them to a passing result.

Validate the change before implementation and before completion:

```bash
npx -y @fission-ai/openspec@1.5.0 validate <change-id> --strict
npx -y @fission-ai/openspec@1.5.0 validate --all --strict
```

Routine documentation corrections that describe existing behavior do not require
a new runtime capability proposal. Update the [documentation index](docs/README.md)
when changes are added or archived. Promote accepted spec deltas, ADRs, and
diagrams only at the appropriate acceptance/sync step; preserve rejected or
superseded proposals as clearly labeled history.

## Checks

For Go changes, run the affected packages and the relevant cross-service suites.
The repository-wide package command is:

```bash
env -u GOROOT go test ./...
```

Before the full E2E run, install each suite's locked dependencies:

```bash
for suite in state-registry api-gateway web-ui containerized-runtime-services executor_k8s_openhands; do
  npm --prefix "qa-e2e/$suite" ci
done
(cd qa-e2e/web-ui && npx playwright install --with-deps chromium)
npm --prefix qa-e2e test
```

The complete runner requires a working container runtime, `k3d`, `kubectl`,
`helm`, `helmfile`, and `openssl` for the suite fixtures.
It creates a temporary k3d cluster shared by the Kubernetes suites and removes
it after the run. Review [the runner](qa-e2e/scripts/run-all.mjs) and the
[E2E guide](qa-e2e/README.md) for environment-specific requirements and artifacts.
A single suite can be run with `npm --prefix qa-e2e/<suite> test`.

Formatting commands operate on **staged files**:

```bash
make format
# Inspect formatter edits and stage the intended result again.
make format-check
make precommit
```

For unstaged Markdown edits, check explicit paths instead:

```bash
npx prettier --check README.md CONTRIBUTING.md docs/README.md
npx markdownlint-cli2 README.md CONTRIBUTING.md docs/README.md
```

Check relative documentation links and `git diff --check`. Documentation-only
changes do not need a runtime deployment unless they alter executable behavior.

## Submitting work

Use focused branches and Conventional Commit messages, such as
`docs: refresh project navigation` or `fix(executor): preserve claim identity`.
In the pull or merge request, describe the problem, resulting behavior, related
OpenSpec change, and the exact checks run. State any unverified scope. Include
UI evidence when relevant and keep generated reports, images, and local secrets
out of the commit.

Contributions are provided under the repository's [MIT License](LICENSE).
Preserve applicable third-party license notices.
