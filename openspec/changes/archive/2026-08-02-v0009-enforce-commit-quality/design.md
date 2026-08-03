# Design

The hook checks only staged files for formatting so unrelated working-tree
changes are not rewritten. Formatting is explicit through `make format`.
golangci-lint runs across the Go module because cross-package analysis is not
reliably scoped to individual staged files.
