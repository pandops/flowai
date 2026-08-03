## ADDED Requirements

### Requirement: Repository blocks commits that fail quality gates

The repository SHALL provide an installable pre-commit hook that rejects a
commit when staged Go files are not gofmt-formatted, staged supported text
files fail Prettier or Markdownlint, any Go package fails golangci-lint, or the
staged diff fails Git whitespace validation.

#### Scenario: Unformatted staged file is rejected

- **WHEN** a developer stages an unformatted supported source file and attempts
  to commit
- **THEN** the pre-commit hook exits unsuccessfully and identifies the formatter
  command required before retrying

#### Scenario: Clean staged change passes

- **WHEN** all staged files are formatted and all configured linters pass
- **THEN** the quality gate exits successfully and Git may create the commit
