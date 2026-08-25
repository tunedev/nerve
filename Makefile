# One command up, one command down. If you have been away three weeks, `make help`.

GO ?= go
BIN := bin/nerved

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sort | awk -F':.*##' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: up
up: ## Start Postgres, RabbitMQ and Ollama
	docker compose up -d --wait

.PHONY: down
down: ## Stop the stack, keep the data
	docker compose down

.PHONY: clean
clean: ## Stop the stack and delete local data
	docker compose down -v
	rm -rf .data $(BIN)

.PHONY: build
build: ## Compile the binary
	$(GO) build -o $(BIN) ./cmd/nerved

.PHONY: run
run: ## Run the binary
	$(GO) run ./cmd/nerved

.PHONY: test
test: ## Run tests (go vet runs as part of this)
	$(GO) test ./...

.PHONY: fmt
fmt: ## Format
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if anything is unformatted
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

.PHONY: lint
lint: ## Run golangci-lint
	golangci-lint run

.PHONY: check
check: fmt-check lint test ## Everything CI runs
