# Promotion Plan

Checklist for increasing project visibility and adoption.

## Social Promotion

### High-Impact, Low-Effort

- [ ] **AI/agentic development communities** — This is a unique selling point no other tool has. Post in:
  - [ ] Claude Code GitHub Discussions / Discord
  - [ ] AI-assisted coding subreddits (r/ClaudeAI, r/ChatGPTCoding)
  - [ ] Dev Twitter/Mastodon — "Here's what Claude Code can access in your keyring (and how to control it)"
  - Angle: "Your AI coding agent has full access to your secret keyring. Here's how to see what it reads."

- [ ] **Security communities** — The supply chain / commit signing angle resonates here:
  - [ ] r/netsec — technical writeup on GPG signing blind trust
  - [ ] r/linux — "Per-application secret access control for your Linux keyring"
  - [ ] r/privacy — audit trail angle
  - [ ] lobste.rs — technical audience, tag: security, linux

- [ ] **Blog post: "Your AI Agent Can Read All Your Secrets"** — Cover:
  - How D-Bus Secret Service works (no per-app access control)
  - Demo: AI agent silently reading keyring secrets
  - How secrets-dispatcher makes this visible and controllable
  - Trust rules for allowing known tools while blocking unknowns
  - Publish on personal blog, dev.to, lobste.rs
  - This is highly topical given the rise of agentic coding.

- [ ] **Blog post: "What GPG Signing Actually Signs (And Why You Should Care)"** — Cover:
  - The blind trust problem in `git commit -S`
  - Demo: signing arbitrary content through forwarded GPG agent
  - How per-commit approval with full context solves this
  - Relevant to the xz backdoor / supply chain discussion

- [ ] **Hacker News** — "Show HN" timed with blog post or v1.0. Two strong angles:
  - AI agent security: "Show HN: See which secrets your AI coding agent accesses"
  - Supply chain: "Show HN: Per-commit approval for GPG signing"

- [ ] **GoPass ecosystem** — Cross-promote with gopass-secret-service:
  - Mention in gopass-secret-service README as a complementary tool
  - GoPass community channels

### Medium-Term Community Building

- [ ] **Conference talks** — Two strong pitches:
  - [ ] "Your AI Agent Has Access to All Your Secrets" — security conferences, AI dev conferences
  - [ ] "Per-Operation Approval for Secret Access on Linux" — FOSDEM Security devroom, All Systems Go
  - [ ] NixCon, Linux Plumbers Conference

- [ ] **Linux distro forums** — Answer existing threads about:
  - [ ] Secret Service access control
  - [ ] Alternatives to gpg-agent forwarding
  - [ ] Keyring security
  - Target: Arch forums, Fedora Discussion, NixOS Discourse

- [ ] **Supply chain security discussions** — Engage in discussions about:
  - [ ] Git signing best practices
  - [ ] xz/supply chain attack prevention
  - [ ] Sigstore/gitsign ecosystem (complementary, not competing)

## Technical Promotion

### Packaging

- [ ] **AUR** — Create package (Arch Linux users are a core audience)
- [ ] **Nixpkgs** — High priority. Nix users care deeply about reproducibility and security.
- [ ] **Homebrew (linuxbrew)** — Easy entry point for developers
- [ ] **Fedora COPR** — Covers Fedora users
- [ ] **Pre-built binaries** — GitHub Releases with Linux amd64/arm64 binaries (release workflow may need setup)

### Project Credibility Signals

- [x] **CONTRIBUTING.md** — Short guide: how to build, test, submit PRs. Signals openness.

- [x] **Issue templates** — Bug report + feature request in `.github/ISSUE_TEMPLATE/`.

- [ ] **Additional badges in README**:
  - [ ] Code coverage
  - [x] Go Report Card
  - [ ] Latest release version

- [ ] **CHANGELOG.md** — Running changelog ([keepachangelog.com](https://keepachangelog.com/) format).

- [x] **SECURITY.md** — Especially important for a security tool. Vulnerability reporting instructions.

- [x] **Screenshots / GIFs** — Web UI screenshot in README. Visual proof that it works is powerful for a UI-heavy tool.

### Discoverability

- [x] **GitHub topics** — Add: `secret-service`, `dbus`, `gpg`, `commit-signing`, `access-control`, `audit-logging`, `linux`, `security`, `ai-safety`, `keyring`.

- [x] **GitHub repo description** — "Per-operation approval and audit logging for secret access and git commit signing on Linux"

- [x] **Search-relevant phrases** — Ensure README covers the terms people Google:
  - "linux keyring access control"
  - "secret service per-app permissions"
  - "gpg signing approval"
  - "ai agent secret access"
  - "dbus secret service proxy"
  - "gnome keyring audit log"

### Features That Drive Adoption

- [ ] **Migration / onboarding wizard** — `secrets-dispatcher setup` interactive command that:
  - Detects running Secret Service provider
  - Configures itself as proxy
  - Installs systemd service
  - Suggests initial trust rules based on running processes

- [ ] **Trust rule suggestions** — When a new process accesses a secret, suggest a rule in the web UI: "Firefox accessed 'login/github'. [Create auto-approve rule]"

- [ ] **Export audit logs** — CSV/JSON export for compliance review. Useful for the team lead persona.

- [ ] **Shell completions** — Bash, Zsh, Fish. Low effort, improves polish.

- [ ] **Man page** — `secrets-dispatcher(1)`. Expected by distro packagers.

- [ ] **Grafana dashboard template** — For users who ship logs to Loki/Elasticsearch. Pre-built dashboard showing secret access patterns.

## Cross-Promotion with gopass-secret-service

These two projects form a natural stack:

```
secrets-dispatcher  →  gopass-secret-service  →  GoPass  →  GPG
  (access control)       (Secret Service API)     (store)    (encryption)
```

- [ ] Add "Works great with" section to both READMEs linking to each other
- [ ] Blog post: "The complete GoPass desktop stack" covering both projects
- [ ] Joint announcement if both hit v1.0 around the same time

## Priority Order

Top 5 highest-ROI actions:

| # | Action | Type | Rationale |
|---|--------|------|-----------|
| 1 | "AI Agent Can Read Your Secrets" blog post | Social | Highly topical, unique angle, long-tail SEO |
| 2 | GitHub topics + repo metadata | Technical | 5 minutes, permanent discoverability |
| 3 | Screenshots in README | Technical | Visual proof, huge impact on first impression |
| 4 | AI/agentic community posts | Social | Unique positioning no competitor has |
| 5 | AUR + Nixpkgs packages | Technical | Meet core audience where they install |

Next 5:

| # | Action | Type | Rationale |
|---|--------|------|-----------|
| 6 | HN "Show HN" post | Social | Broad reach, AI security angle is topical |
| 7 | SECURITY.md + CONTRIBUTING.md | Technical | Table-stakes credibility for a security tool |
| 8 | r/netsec + r/linux posts | Social | Direct access to target audience |
| 9 | Trust rule suggestions in web UI | Technical | Reduces onboarding friction |
| 10 | Cross-promotion with gopass-secret-service | Social | Multiplies reach for both projects |
