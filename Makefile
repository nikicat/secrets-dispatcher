MAKEFLAGS += -j

.PHONY: all build frontend backend backend-dev clean test test-go test-e2e test-e2e-all test-e2e-browser \
	playwright-install dev version pre-commit screenshots \
	check check-go check-go-fmt check-go-vet check-go-staticcheck check-frontend check-frontend-fmt check-frontend-lint \
	fmt fmt-go fmt-frontend

all: build

# Build the frontend with Deno
frontend:
	cd web && deno task build

# Copy frontend dist to internal/api for embedding
# This creates the web/dist directory that embed.go expects
embed-frontend: frontend
	mkdir -p internal/api/web
	cp -r web/dist internal/api/web/

# Version from git (fallback for untagged repos)
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "v0.0.0-g$$(git rev-parse --short HEAD)")

# Extra flags passed to the Go linker (e.g. "-linkmode=external -s -w")
GO_LDFLAGS ?=

# Build the Go binary (includes embedded frontend)
backend: embed-frontend
	go build -ldflags "-X main.version=$(VERSION) $(GO_LDFLAGS)" -o secrets-dispatcher .

# Build Go binary without embedded frontend (for dev/testing)
# Outputs to .build/ to avoid overwriting ./secrets-dispatcher used by local service
backend-dev: frontend
	@mkdir -p .build
	go build -tags dev -ldflags "-X main.version=$(VERSION)" -o .build/secrets-dispatcher .

# Full build
build: backend

# Run frontend dev server (with API proxy to localhost:8484)
dev:
	cd web && deno task dev

# Run all tests
test: test-go test-e2e

# Go tests only
test-go:
	go test -tags dev -race ./...

# E2E tests (no embed needed — proxy serves frontend from web/dist)
test-e2e: backend-dev
	cd web && deno task test:e2e

# E2E tests in all browsers (chromium + firefox)
test-e2e-all: backend-dev
	cd web && ALL_BROWSERS=1 deno task test:e2e

# E2E tests for a single browser (usage: make test-e2e-browser BROWSER=firefox)
BROWSER ?= chromium
test-e2e-browser: backend-dev
	cd web && ALL_BROWSERS=1 deno run -A npm:@playwright/test@latest/cli test --project=$(BROWSER)

# Install Playwright browser with system deps (usage: make playwright-install BROWSER=chromium)
playwright-install:
	cd web && deno run -A npm:@playwright/test@latest/cli install --with-deps $(BROWSER)

# Generate screenshots for docs (output: docs/screenshots/)
screenshots: backend-dev
	cd web && deno cache --node-modules-dir playwright.config.ts tests/screenshots.spec.ts && \
		deno run -A npm:@playwright/test@latest/cli test --project=chromium tests/screenshots.spec.ts

# Show the version that will be embedded
version:
	@echo $(VERSION)

# Clean build artifacts
clean:
	rm -rf web/dist internal/api/web secrets-dispatcher .build

# Run checks and tests in parallel
pre-commit: check test

# --- Checks (linters, formatters, static analysis) ---

# Run all checks
check: check-go check-frontend

# All Go checks
check-go: check-go-fmt check-go-vet check-go-staticcheck

# Go formatting check (fails if any files need formatting)
check-go-fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "Go files need formatting:"; \
		echo "$$unformatted"; \
		echo "Run 'make fmt-go' to fix."; \
		exit 1; \
	fi

# Go vet (dev tag skips embed of frontend dist)
check-go-vet:
	go vet -tags dev ./...

# Go staticcheck
check-go-staticcheck:
	go tool staticcheck -tags dev ./...

# All frontend checks (types + formatting + lint)
check-frontend: check-frontend-fmt check-frontend-lint
	cd web && deno task check

# Frontend formatting check
check-frontend-fmt:
	cd web && deno fmt --check

# Frontend linting
check-frontend-lint:
	cd web && deno lint

# --- Auto-formatters ---

# Format all code
fmt: fmt-go fmt-frontend

# Format Go code
fmt-go:
	gofmt -w .

# Format frontend code
fmt-frontend:
	cd web && deno fmt
