GOLANGCI_LINT_VERSION := v2.11.4

.PHONY: format format-check lint precommit install-tools install-hooks

format:
	npm run format

format-check:
	npm run format:check

lint:
	env -u GOROOT golangci-lint run ./...

precommit:
	npm run precommit

install-tools:
	env -u GOROOT go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

install-hooks:
	bash .hooks/install.sh
