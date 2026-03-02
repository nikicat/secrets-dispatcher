.PHONY: all build frontend backend backend-dev clean test test-go test-e2e test-e2e-all dev version \
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

# Build the Go binary (includes embedded frontend)
backend: embed-frontend
	go build -ldflags "-X main.version=$(VERSION)" -o secrets-dispatcher .

# Build Go binary without embedded frontend (for dev/testing)
backend-dev: frontend
	go build -tags dev -ldflags "-X main.version=$(VERSION)" -o secrets-dispatcher .

# Full build
build: backend

# Run frontend dev server (with API proxy to localhost:8484)
dev:
	cd web && deno task dev

# Run all tests
test: test-go test-e2e

# Go tests only
test-go:
	go test -race ./...

# E2E tests (no embed needed — proxy serves frontend from web/dist)
test-e2e: backend-dev
	cd web && deno task test:e2e

# E2E tests in all browsers (chromium + firefox)
test-e2e-all: backend-dev
	cd web && ALL_BROWSERS=1 deno task test:e2e

# Show the version that will be embedded
version:
	@echo $(VERSION)

# Clean build artifacts
clean:
	rm -rf web/dist internal/api/web secrets-dispatcher

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

# Go vet
check-go-vet:
	go vet ./...

# Go staticcheck (filter stdlib vendor noise — go1.25 ships a go1.26 file)
check-go-staticcheck:
	@out=$$(staticcheck ./... 2>&1 | grep -v '^-:'); \
	if [ -n "$$out" ]; then \
		echo "$$out"; \
		exit 1; \
	fi

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
