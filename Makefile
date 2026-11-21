# tia — development tasks.
# Run `make help` to list available targets.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
MAKEFLAGS += --no-print-directory

# ---- Project -----------------------------------------------------------------
BINARY   := tia
CMD_PKG  := ./cmd/tia
BIN_DIR  := $(CURDIR)/bin
PKGS     := ./...

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS  := -s -w -X main.version=$(VERSION)

GO       ?= go
GOFLAGS  ?=
COVER_OUT := coverage.out

# ---- Pinned tool versions ------------------------------------------------------
GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION   := v1.9.0

GOLANGCI_LINT := $(BIN_DIR)/golangci-lint-$(GOLANGCI_LINT_VERSION)
GOVULNCHECK   := $(BIN_DIR)/govulncheck-$(GOVULNCHECK_VERSION)

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"; printf "Usage: make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z0-9_-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

.PHONY: all
all: fmt lint test build ## Format, lint, run unit tests, and build

.PHONY: check
check: tidy-check fmt-check lint test ## CI gate: verify go.mod tidy, formatting, lint, and unit tests

##@ Build

.PHONY: build
build: ## Build the tia binary into ./bin
	$(GO) build $(GOFLAGS) -trimpath -ldflags '$(LDFLAGS)' -o $(BIN_DIR)/$(BINARY) $(CMD_PKG)

.PHONY: install
install: ## Install the tia binary into $GOBIN
	$(GO) install $(GOFLAGS) -trimpath -ldflags '$(LDFLAGS)' $(CMD_PKG)

##@ Test

.PHONY: test
test: ## Run unit (Small) tests only: go test -short
	$(GO) test $(GOFLAGS) -short $(PKGS)

.PHONY: test-all
test-all: ## Run all tests including integration (Medium) tests
	$(GO) test $(GOFLAGS) $(PKGS)

.PHONY: test-race
test-race: ## Run unit tests with the race detector (requires a C compiler)
	CGO_ENABLED=1 $(GO) test $(GOFLAGS) -short -race $(PKGS)

.PHONY: cover
cover: ## Run all tests with coverage and print a summary
	$(GO) test $(GOFLAGS) -covermode=atomic -coverprofile=$(COVER_OUT) $(PKGS)
	$(GO) tool cover -func=$(COVER_OUT) | tail -n 1

.PHONY: cover-html
cover-html: cover ## Open the coverage report in a browser
	$(GO) tool cover -html=$(COVER_OUT)

##@ Code quality

.PHONY: fmt
fmt: $(GOLANGCI_LINT) ## Format code (gofumpt + goimports via golangci-lint)
	$(GOLANGCI_LINT) fmt

.PHONY: fmt-check
fmt-check: $(GOLANGCI_LINT) ## Fail if any file needs formatting
	$(GOLANGCI_LINT) fmt --diff

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint (includes go vet and format checks)
	$(GOLANGCI_LINT) run $(PKGS)

.PHONY: lint-fix
lint-fix: $(GOLANGCI_LINT) ## Run golangci-lint and apply auto-fixes
	$(GOLANGCI_LINT) run --fix $(PKGS)

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(PKGS)

.PHONY: vuln
vuln: $(GOVULNCHECK) ## Scan dependencies for known vulnerabilities
	$(GOVULNCHECK) $(PKGS)

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	$(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod / go.sum are not tidy
	$(GO) mod tidy -diff

##@ Tools

.PHONY: tools
tools: $(GOLANGCI_LINT) $(GOVULNCHECK) ## Install pinned dev tools into ./bin

$(GOLANGCI_LINT):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	@mv $(BIN_DIR)/golangci-lint $@

$(GOVULNCHECK):
	@mkdir -p $(BIN_DIR)
	GOBIN=$(BIN_DIR) $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	@mv $(BIN_DIR)/govulncheck $@

##@ Cleanup

.PHONY: clean
clean: ## Remove build artifacts and coverage output
	rm -f $(BIN_DIR)/$(BINARY) $(COVER_OUT)
	$(GO) clean -testcache
