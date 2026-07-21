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
├── main.go                 # CLI entrypoint (stdlib flag switch)
├── internal/
│   ├── api/                # HTTP API + embedded frontend + WebSocket
│   ├── approval/           # Approval engine + trust rules
│   ├── cli/                # CLI commands (list, approve, deny, login, ...)
│   ├── companion/          # Companion-user provisioning (privsep, exploratory)
│   ├── config/             # Configuration loading
│   ├── daemon/             # System D-Bus daemon path (privsep, exploratory)
│   ├── dbus/               # D-Bus Secret Service proxy
│   ├── dhcrypto/           # Diffie-Hellman session encryption (Secret Service)
│   ├── gpgsign/            # GPG signing proxy
│   ├── logging/            # Structured audit logging
│   ├── notification/       # Desktop notifications
│   ├── procutil/           # Process chain detection
│   ├── proxy/              # Proxy core logic
│   ├── service/            # systemd service management + keyring takeover
│   ├── sshagent/           # SSH agent proxy — gates key signing through approval
│   └── testutil/           # Test helpers
├── cmd/mock-secret-service/ # Test fixture Secret Service backend
├── web/                    # Svelte frontend (Deno + Vite)
├── e2e/                    # Playwright + Tier-2 GNOME VM end-to-end suite
├── packaging/              # AUR PKGBUILD and packaging assets
├── scripts/                # Build / release / CI helper scripts
└── docs/                   # Documentation
```

> The `companion`/`daemon` packages back an exploratory privilege-separation
> path: secrets under a dedicated companion user reached over the **system** bus,
> with a dedicated VT for approvals as the intended trusted-I/O surface (the VT
> TUI is not implemented yet). It runs in parallel with the shipped session-bus
> proxy — neither path is legacy.
