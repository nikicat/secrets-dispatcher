# Contributing

Contributions are welcome! Here's how to get started.

## Building

**Prerequisites:** Go 1.26+, Deno 2.x, Make

```bash
git clone https://github.com/nikicat/secrets-dispatcher.git
cd secrets-dispatcher
make build        # Full build (frontend + Go binary)
make backend-dev  # Quick Go build (uses dev frontend proxy)
```

## Testing

```bash
make test-go      # Go unit/integration tests
make test-e2e     # Playwright E2E tests (chromium)
make test-e2e-all # E2E tests in all browsers
make pre-commit   # Lint + format + tests (run before submitting)
```

## Development Workflow

1. `make backend-dev` — builds Go binary to `.build/`
2. `make dev` — starts Vite dev server with API proxy to `:8484`
3. Run `.build/secrets-dispatcher serve` for the backend

The frontend (Svelte) live-reloads. The Go binary needs rebuilding on backend changes.

## Code Style

- **Go:** `gofmt` (enforced by CI). Run `make fmt-go` to auto-format.
- **Frontend:** Deno fmt + lint (enforced by CI). Run `make fmt-frontend`.
- **Static analysis:** `go vet` and `staticcheck` (enforced by CI).

## Submitting Changes

1. Fork the repository
2. Create a feature branch from `master`
3. Make your changes
4. Run `make pre-commit` and ensure it passes
5. Open a pull request with a clear description of what and why

## Project Structure

```
.
├── main.go                 # CLI entrypoint (stdlib flag)
├── internal/
│   ├── api/                # HTTP API + embedded frontend
│   ├── approval/           # Approval engine + trust rules
│   ├── cli/                # CLI commands
│   ├── config/             # Configuration loading
│   ├── dbus/               # D-Bus Secret Service proxy
│   ├── gpgsign/            # GPG signing proxy
│   ├── notification/       # Desktop notifications
│   ├── procutil/           # Process chain detection
│   ├── proxy/              # Proxy core logic
│   └── service/            # systemd service management
├── web/                    # Svelte frontend (Deno + Vite)
└── docs/                   # Documentation
```
