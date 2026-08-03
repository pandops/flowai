# Tasks

- [x] **RED:** define an E2E shell scenario that stages an unformatted Go file
      in a disposable Git repository and observes the pre-commit command reject it.
- [x] **GREEN:** add pinned formatters, Markdownlint and golangci-lint
      configuration, staged-file quality script, Make targets, and pre-commit hook.
- [x] **GREEN VERIFY:** install dependencies and the hook, format the staged
      change, then require `make precommit` and the disposable Git scenario to
      pass.
- [x] **REFACTOR:** keep the hook read-only and staged-file-aware; validate and
      archive this tooling change after verification.
