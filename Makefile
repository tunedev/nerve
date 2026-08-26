# One command up, one command down. If you have been away three weeks, `make help`.

GO ?= go
BIN := bin/nerved
# A build is reproducible only when nothing about the build machine reaches the
# binary. -trimpath drops the absolute source paths, CGO_ENABLED=0 drops the host
# C toolchain, and go.mod's toolchain line pins the compiler itself.
BUILD_ENV := CGO_ENABLED=0
BUILD_FLAGS := -trimpath
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
build: ## Compile the binary reproducibly
	$(BUILD_ENV) $(GO) build $(BUILD_FLAGS) -o $(BIN) ./cmd/nerved

.PHONY: repro
repro: ## Build HEAD in two clean checkouts and require identical binaries
	@set -e; tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	for c in one two; do \
		git clone -q --no-local . "$$tmp/$$c"; \
		$(MAKE) -s -C "$$tmp/$$c" build; \
	done; \
	cmp "$$tmp/one/$(BIN)" "$$tmp/two/$(BIN)"; \
	echo "reproducible: $$(sha256sum <"$$tmp/one/$(BIN)" | cut -d' ' -f1)"

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
check: fmt-check lint race repro ## Everything CI runs
