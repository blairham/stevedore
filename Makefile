.DEFAULT_GOAL := help

.PHONY: help build test fmt vet check tidy install

help: ## Display this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build all packages
	go build ./...

test: ## Run tests with the race detector
	go test -race ./...

fmt: ## Format the code
	go tool gofumpt -w .

vet: ## Run go vet
	go vet ./...

# There is no lint target: golangci-lint runs as a pre-commit hook and in CI.
check: vet test ## What CI runs (minus lint, which is the commit hook's job)
	go build ./...

tidy: ## go mod tidy
	go mod tidy

install: ## Install the stevedore binary into GOPATH/bin
	go install .
