# onboard — developer Makefile.
# Run `make` (or `make help`) to see every target.

MODULE  := github.com/VerifiedOrganic/onboard
BINARY  := onboard
PORT    ?= 8080

# Version metadata stamped into the binary (overridable: `make build VERSION=v1.2.3`).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(MODULE)/cmd.version=$(VERSION) \
	-X $(MODULE)/cmd.commit=$(COMMIT) \
	-X $(MODULE)/cmd.date=$(DATE)

GOFLAGS      := -trimpath
GOLANGCI     := golangci-lint
GOLANGCI_VER := v2.5.0

# Cross-compile matrix for `make cross`.
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.DEFAULT_GOAL := help

## --- Help ---

.PHONY: help
help: ## Show this help (default target)
	@echo "onboard $(VERSION) — make targets:"
	@echo ""
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- Build & install ---

.PHONY: build
build: ## Build the onboard binary into ./onboard
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

.PHONY: build-core
build-core: ## Build a smaller binary (curated Core100 grammar set)
	go build $(GOFLAGS) -tags grammar_set_core -ldflags "$(LDFLAGS)" -o $(BINARY) .

.PHONY: install
install: ## Install the binary onto your PATH (go install -> GOBIN)
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" .

.PHONY: agents
agents: build ## Wire onboard into every detected agent (runs `onboard init`)
	./$(BINARY) init

.PHONY: cross
cross: ## Cross-compile to all release targets into ./dist (CGO disabled)
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "  building dist/$(BINARY)_$${os}_$${arch}$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o dist/$(BINARY)_$${os}_$${arch}$$ext . || exit 1; \
	done

## --- Run ---

.PHONY: serve
serve: build ## Run the MCP server over stdio (what agents launch)
	./$(BINARY) serve

.PHONY: serve-http
serve-http: build ## Run the MCP server over Streamable HTTP (PORT=8080)
	./$(BINARY) serve --http :$(PORT)

.PHONY: skills
skills: build ## List the skills embedded in the binary
	./$(BINARY) skills

## --- Quality ---

.PHONY: check
check: tidy-check fmt-check vet lint test ## Run everything CI runs (tidy/fmt/vet/lint/test)

.PHONY: test
test: ## Run the test suite
	go test ./...

.PHONY: test-race
test-race: ## Run the test suite with the race detector
	go test -race ./...

.PHONY: cover
cover: ## Run tests with coverage and print a summary
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: cover-html
cover-html: cover ## Open the HTML coverage report
	go tool cover -html=coverage.out

.PHONY: lint
lint: ## Run golangci-lint (see `make tools` to install it)
	$(GOLANGCI) run

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format all Go code (gofmt -w)
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-clean
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then echo "not gofmt-clean:"; echo "$$unformatted"; exit 1; fi

.PHONY: tidy
tidy: ## Tidy go.mod / go.sum
	go mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod / go.sum are not tidy
	@cp go.mod go.mod.bak; cp go.sum go.sum.bak; \
	go mod tidy; \
	if ! diff -q go.mod go.mod.bak >/dev/null || ! diff -q go.sum go.sum.bak >/dev/null; then \
		echo "go.mod/go.sum not tidy — run 'make tidy'"; \
		mv go.mod.bak go.mod; mv go.sum.bak go.sum; exit 1; \
	fi; \
	rm -f go.mod.bak go.sum.bak

## --- Misc ---

.PHONY: tools
tools: ## Install dev tools (golangci-lint v2.5.0)
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VER)

.PHONY: version
version: build ## Print the built binary's version string
	./$(BINARY) --version

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BINARY) dist coverage.out
