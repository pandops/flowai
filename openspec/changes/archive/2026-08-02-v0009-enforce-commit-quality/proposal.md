# Change: v0009-enforce-commit-quality

## Why

Formatting and lint quality must be deterministic before every commit instead
of relying on manual cleanup after staging.

## What changes

Add pinned Prettier and Markdownlint tooling, a repository golangci-lint
configuration, staged-file formatting commands, and a repository pre-commit
hook that blocks commits until all checks pass.

## Impact

- Change type: development
- Affected specs: developer-tooling
- Affected code: root tooling configuration, scripts, and Git hooks
- Affected test cases: `autotest/test-cases/v0009.1-precommit-rejects-unformatted-change.md`

## Out of scope

- Changing production service behavior
- Automatically staging formatter output
