GOLANGCI_LINT_VERSION := v2.12.2

.PHONY: test lint lint-install check

test:
	go test ./...

lint:
	golangci-lint run

lint-install:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

check: lint test
