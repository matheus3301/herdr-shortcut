# herdr-shortcut developer tasks.
# `make check` runs every local gate that needs no network or credentials.

GO ?= go
PKG := ./...
COVERAGE_MIN ?= 80
COVERAGE_FILE := coverage.txt
BIN := bin/herdr-shortcut

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: fmt
fmt: ## Format all Go sources
	$(GO) fmt $(PKG)

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean
	@out="$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@echo "gofmt: clean"

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy (restores originals via trap)
	@set -e; \
	cp go.mod go.mod.bak; cp go.sum go.sum.bak; \
	trap 'mv -f go.mod.bak go.mod; mv -f go.sum.bak go.sum' EXIT INT TERM; \
	if ! $(GO) mod tidy; then echo "go mod tidy failed"; exit 1; fi; \
	if ! cmp -s go.mod go.mod.bak || ! cmp -s go.sum go.sum.bak; then \
		echo "go.mod/go.sum are not tidy; run 'make tidy'"; exit 1; fi; \
	echo "go mod tidy: clean"

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKG)

.PHONY: test
test: ## Run tests
	$(GO) test $(PKG)

.PHONY: test-race
test-race: ## Run tests with the race detector
	$(GO) test -race $(PKG)

.PHONY: coverage
coverage: ## Run tests with coverage and enforce the threshold
	$(GO) test -covermode=atomic -coverprofile=$(COVERAGE_FILE) $(PKG)
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | awk '/^total:/ {print $$3}' | tr -d '%'); \
	echo "total coverage: $$total% (min $(COVERAGE_MIN)%)"; \
	awk "BEGIN{exit !($$total+0 >= $(COVERAGE_MIN))}" || \
		{ echo "coverage $$total% is below the $(COVERAGE_MIN)% threshold"; exit 1; }

.PHONY: build
build: ## Build the binary to ./bin
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) ./cmd/herdr-shortcut

.PHONY: verify-plugin
verify-plugin: ## Validate the manifest against Herdr in isolated state
	sh scripts/verify-plugin.sh

.PHONY: smoke-nogo
smoke-nogo: ## Smoke-test the no-Go install fallback with local fake release assets
	sh scripts/smoke-nogo-install.sh

.PHONY: sh-syntax
sh-syntax: ## Check POSIX shell syntax of the scripts
	@for f in scripts/*.sh; do sh -n "$$f" && echo "sh -n ok: $$f"; done

.PHONY: check
check: fmt-check tidy-check vet test-race coverage build sh-syntax smoke-nogo ## Run all local quality gates
	@echo "all checks passed"

.PHONY: clean
clean: ## Remove build and coverage artifacts
	rm -rf bin dist $(COVERAGE_FILE) coverage.html
