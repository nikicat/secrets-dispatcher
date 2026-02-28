.PHONY: all build frontend backend clean test test-go test-e2e dev version

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

# Full build
build: backend

# Run frontend dev server (with API proxy to localhost:8484)
dev:
	cd web && deno task dev

# Run all tests
test: test-go

# Go tests only
test-go:
	go test -race ./...

# E2E tests (requires built binary)
test-e2e: build
	cd web && deno task test:e2e

# Show the version that will be embedded
version:
	@echo $(VERSION)

# Clean build artifacts
clean:
	rm -rf web/dist internal/api/web secrets-dispatcher

# Type check the frontend
check-frontend:
	cd web && deno task check
