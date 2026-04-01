.PHONY: help build test lint install clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

build: ## Build the plancritic binary
	go build -o plancritic ./cmd/plancritic

test: ## Run all tests
	go test ./...

lint: ## Run golangci-lint
	golangci-lint run

install: ## Install plancritic to GOPATH/bin
	go install ./cmd/plancritic

clean: ## Remove build artifacts
	rm -f plancritic
