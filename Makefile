.DEFAULT_GOAL := help

.PHONY: help build test fmt vet lint check tidy install

help: ## Display this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-10s\033[0m %s\n", $$1, $$2}'

build: ## Build all packages
	go build ./...

test: ## Run tests with the race detector
	go test -race ./...

fmt: ## Format the code
	gofmt -w .

vet: ## Run go vet
	go vet ./...

lint: ## Static checks: gofmt (check) + go vet
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed in:"; gofmt -l .; exit 1)
	go vet ./...

check: lint test ## Lint + test (mirror CI locally, run before a PR)

tidy: ## go mod tidy
	go mod tidy

install: ## Install the stevedore binary into GOPATH/bin
	go install .
