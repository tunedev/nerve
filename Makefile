# One command up, one command down. If you have been away three weeks, `make help`.

GO ?= go
BIN := bin/nerved
# The Go trees. Named rather than walking `.`, which would descend into .data.
SRC := cmd internal

# Load .env into the environment when it exists, so `make run` and `make up` read
# the same file. Settings still layer as usual; this is only the environment layer.
RUN_ENV := set -a; [ -f .env ] && . ./.env; set +a;

.DEFAULT_GOAL := help

.PHONY: help
help: ## List targets
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sort | awk -F':.*##' '{printf "  %-12s %s\n", $$1, $$2}'

.PHONY: up
up: ## Start Postgres, RabbitMQ and Ollama, and wait until they answer
	docker compose up -d --wait

.PHONY: down
down: ## Stop the stack, keep the data
	docker compose down

.PHONY: status
status: ## Show what is running and whether it is healthy
	docker compose ps

.PHONY: logs
logs: ## Follow the stack's logs
	docker compose logs -f

.PHONY: clean
clean: ## Stop the stack, delete its data and the binary
	docker compose down -v
	rm -rf $(BIN)

.PHONY: build
build: ## Compile the binary
	$(GO) build -o $(BIN) ./cmd/nerved

.PHONY: run
run: ## Run the binary against the stack
	@$(RUN_ENV) $(GO) run ./cmd/nerved

.PHONY: test
test: ## Run tests fast, no race detector (go vet runs as part of this)
	$(GO) test ./...

.PHONY: race
race: ## Run tests under the race detector
	$(GO) test -race ./...

.PHONY: fmt
fmt: ## Format
	$(GO) fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail if anything is unformatted
	@out=$$(gofmt -l $(SRC)); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

.PHONY: lint
lint: ## Verify the lint config, then run golangci-lint
	golangci-lint config verify
	golangci-lint run

.PHONY: check
check: fmt-check lint race ## Everything CI runs
