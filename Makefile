MAKEFLAGS += -j

.PHONY: all build install frontend backend backend-dev backend-go notifstub notifprobe clean test test-go test-e2e test-e2e-all test-e2e-browser \
	test-e2e-gnome test-e2e-gnome-container demo playwright-install dev version release pre-commit screenshots \
	check check-go check-go-fmt check-go-vet check-go-staticcheck check-frontend check-frontend-fmt check-frontend-lint \
	fmt fmt-go fmt-frontend

all: build

# Build the frontend with Deno
frontend:
	cd web && deno task build

# Copy frontend dist to internal/api for embedding. The dist is committed
# (go-install support), so the copy must be exact: remove first, or stale
# content-hashed bundles from previous builds accumulate and the freshness
# check (CI) can never pass.
embed-frontend: frontend
	mkdir -p internal/api/web
	rm -rf internal/api/web/dist
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

# Install destination. Override PREFIX (e.g. PREFIX=/usr) or set DESTDIR for
# staged/packaging installs.
PREFIX ?= $(HOME)/.local

# Install the built ./secrets-dispatcher binary to $(PREFIX)/bin
# (default: ~/.local/bin). Run `make build` first.
install:
	install -Dm755 secrets-dispatcher $(DESTDIR)$(PREFIX)/bin/secrets-dispatcher

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
	cd web && ALL_BROWSERS=1 deno run -A npm:@playwright/test@1.61.1/cli test --project=$(BROWSER)

# Go-only dev binary (no frontend, no deno). Distinct output so it can't race
# a parallel backend-dev build of .build/secrets-dispatcher.
backend-go:
	@mkdir -p .build
	CGO_ENABLED=0 go build -tags dev -ldflags "-X main.version=$(VERSION)" -o .build/secrets-dispatcher-go .

# US-7 e2e helpers: notifstub captures Notify args (Tier-1), notifprobe
# asserts no expired-close against real gnome-shell (Tier-2, pushed into the VM).
notifstub:
	@mkdir -p .build
	CGO_ENABLED=0 go build -o .build/notifstub ./e2e/gnome/notifstub

notifprobe:
	@mkdir -p .build
	CGO_ENABLED=0 go build -o .build/notifprobe ./e2e/gnome/notifprobe

# Tier-1 GNOME e2e: real gnome-keyring behind the proxy on private buses.
# Needs gnome-keyring + libsecret installed (Ubuntu/CI); elsewhere use
# test-e2e-gnome-container.
test-e2e-gnome: backend-go notifstub
	timeout 120 e2e/gnome/fast.sh .build/secrets-dispatcher-go .build/notifstub

# Same, inside an ubuntu:24.04 container (podman or docker)
test-e2e-gnome-container: backend-go notifstub
	e2e/gnome/container.sh .build/secrets-dispatcher-go .build/notifstub

# Tier-2 GNOME e2e: real Ubuntu desktop VM (qemu+KVM+cloud-init), covers the
# takeover/re-grab/reversal acceptance gates. First run provisions a cached
# desktop base image (~10 min); each run boots a throwaway overlay.
# UBUNTU_SERIES picks the release (noble = 24.04 LTS, resolute = 26.04 LTS):
#   make test-e2e-gnome-vm UBUNTU_SERIES=resolute
UBUNTU_SERIES ?= noble
export UBUNTU_SERIES
test-e2e-gnome-vm: backend-go notifprobe
	e2e/gnome/vm/run.sh provision
	e2e/gnome/vm/run.sh destroy
	e2e/gnome/vm/run.sh boot
	e2e/gnome/vm/run.sh wait-desktop
	NOTIFPROBE=.build/notifprobe e2e/gnome/vm/scenario.sh .build/secrets-dispatcher-go
	e2e/gnome/vm/run.sh destroy

# Screen-recorded product demos from the Tier-2 GNOME VM (same cached desktop
# base as test-e2e-gnome-vm). Output: .build/demos/*.webm (+ .mp4 when ffmpeg
# is installed) — throwaway artifacts, never committed; demos.yml uploads
# them from CI. GO_REF picks what the on-camera `go install` fetches.
demo: backend-go
	e2e/gnome/vm/run.sh provision
	e2e/gnome/vm/run.sh destroy
	e2e/gnome/vm/run.sh boot
	e2e/gnome/vm/run.sh wait-desktop
	e2e/gnome/vm/demo.sh .build/demos .build/secrets-dispatcher-go
	e2e/gnome/vm/run.sh destroy

# Install Playwright browser with system deps (usage: make playwright-install BROWSER=chromium)
playwright-install:
	cd web && deno run -A npm:@playwright/test@1.61.1/cli install --with-deps $(BROWSER)

# Generate screenshots for docs (output: docs/screenshots/)
screenshots: backend-dev
	cd web && deno cache --node-modules-dir playwright.config.ts tests/screenshots.spec.ts && \
		deno run -A npm:@playwright/test@1.61.1/cli test --project=chromium tests/screenshots.spec.ts

# Show the version that will be embedded
version:
	@echo $(VERSION)

# Cut a new release and block until the Release workflow succeeds (binary
# uploads + AUR publish). See scripts/release.sh for details and guards.
# Usage:
#   make release BUMP=patch|minor|major   # bump the latest vX.Y.Z tag
#   make release TAG=v1.2.3               # explicit tag
release:
	@BUMP="$(BUMP)" TAG="$(TAG)" scripts/release.sh

# Clean build artifacts. internal/api/web/dist is COMMITTED (go-install
# support) — never remove it here; `make embed-frontend` refreshes it.
clean:
	rm -rf web/dist secrets-dispatcher .build

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
