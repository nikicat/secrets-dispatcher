# Target Audience & User Personas

## Overview

secrets-dispatcher targets developers and engineers who want **visibility and control over what accesses their secrets and signing keys** — whether the requestor is a local application, an AI coding agent, or a script on a remote server. The common thread is that the user's secrets live behind a keyring, and they want to know (and decide) who gets access.

## Market Context

Three security gaps exist in the standard Linux desktop secret management:

**Local apps access secrets silently.** Any process running as your user can call the Secret Service D-Bus API and read any unlocked secret. Browsers, Electron apps, CLI tools, and — increasingly — AI coding agents (Claude Code, Codex, Cursor, etc.) can read your keyring contents without you ever knowing. There's no audit trail and no per-access approval.

**Git commit signing is blind.** `git commit -S` calls GPG with zero human-visible context. In automated environments or when AI agents make commits on your behalf, arbitrary content can be signed without the key holder reviewing what they're approving.

**gpg-agent forwarding is all-or-nothing.** Forwarding a GPG agent socket over SSH gives the remote machine access to decrypt *any* secret encrypted to that key. No per-secret approval, no audit trail, no visibility.

**secrets-dispatcher** interposes between the requestor and the secret (or signing key), adding per-operation approval with full context: which process, which secret, which commit.

---

## Persona 1: The AI-Augmented Developer

**Name:** Alex, 33, Full-stack Developer (Remote)

**Background:** Uses Claude Code and GitHub Copilot daily. Has API keys, database credentials, and service tokens in their keyring (via GoPass or GNOME Keyring). Runs AI agents that can execute shell commands, read files, and interact with development tools — all under their user account.

**Pain point:** AI coding agents run with full user permissions. They can call `secret-tool lookup`, read `~/.config/` files, or access the D-Bus Secret Service — all without any prompt or audit trail. Alex trusts these tools for coding but not for silent credential access. The asymmetry is uncomfortable: the agent can read any secret but the developer has no visibility into whether or when it does.

**What they want:**
- See a notification when *any* process accesses a secret, including AI agents
- Approve or deny per-secret access in real-time
- Auto-approve rules for known-safe patterns (e.g., their IDE accessing git credentials)
- Audit log showing exactly which secrets were accessed and by which process chain
- Process chain visibility: not just "dbus-daemon asked for a secret" but "claude-code → node → dbus-send"

**How they found the project:** Searching for "secret service access control linux" after wondering what their AI agent could see.

**Technical comfort:** High. Comfortable with CLI, systemd, D-Bus concepts.

**Usage pattern:**
- Runs secrets-dispatcher as a systemd user service (always on)
- Trust rules auto-approve their browser and known tools
- All other access requires explicit approval via desktop notification
- Reviews audit log periodically to spot unexpected access
- Adjusts rules as they adopt new tools

---

## Persona 2: The Commit Signing Gatekeeper

**Name:** Daniel, 31, Backend Developer (Amsterdam)

**Background:** Works at a fintech startup. Company policy requires signed commits. Uses GPG signing but has always been uncomfortable that `git commit -S` signs blindly. After the xz supply chain incident, he's extra cautious — especially now that AI agents can make commits on his behalf.

**Pain point:** He signs 20+ commits a day. Most are fine — it's the one malicious or accidental commit that worries him. When an AI agent rewrites a file and commits it, GPG signs without showing him what changed. There's no human review gate between "agent wants to commit" and "commit is cryptographically signed by me."

**What he wants:**
- See exactly what he's signing (repo, author, message, changed files) before GPG touches his key
- Quick approve/deny flow via desktop notification for routine commits
- Web UI for reviewing larger changes (rebases, merges)
- Auto-approve rules for his own local commits on trusted repos
- Explicit approval required for CI-originated or agent-originated commits

**How he found the project:** Searching for "git gpg signing approval" after a team discussion about supply chain security.

**Technical comfort:** High. Comfortable with GPG, systemd, git internals.

**Usage pattern:**
- `secrets-dispatcher gpg-sign setup` once
- Daemon runs as systemd user service
- Approves most commits via desktop notification (quick tap)
- Opens web UI for release tags and merge commits
- Trusted signer rules for his own editor but not for AI agents

---

## Persona 3: The Remote Server Operator

**Name:** Lena, 36, SRE (Berlin)

**Background:** Manages 30+ servers. Uses GoPass for secrets (API keys, database passwords, service tokens). Currently SSH-forwards her GPG agent to servers so scripts can decrypt secrets. Knows this is insecure — a rooted server could drain her entire password store — but hasn't found a better workflow.

**Pain point:** She needs server-side scripts to access specific secrets (e.g., a deploy script needs the deploy token), but forwarding gpg-agent gives blanket access to everything. She wants per-secret approval with visibility into which process on which server is requesting which secret.

**What she wants:**
- Access secrets on remote servers without exposing all secrets
- See which process on which server is requesting which secret
- Pre-authorize known-good patterns (deploy script → deploy token)
- Block everything else by default
- Audit trail for compliance

**How she found the project:** GoPass community, searching for alternatives to gpg-agent forwarding.

**Technical comfort:** Very high. Writes Ansible playbooks, manages SSH infrastructure, familiar with D-Bus and Secret Service protocol.

**Usage pattern:**
- SSH config with LocalForward for each managed server
- secrets-dispatcher running as a service on her laptop
- Trust rules for known deploy scripts and services
- Reviews audit logs weekly
- Web UI open in a browser tab for real-time monitoring

---

## Persona 4: The Local Access Control Advocate

**Name:** Mei, 30, Security Engineer (Singapore)

**Background:** Runs Arch Linux with Sway. Has 300+ secrets in GoPass covering personal, work, and client credentials. Uses multiple development tools, some proprietary (Slack, VS Code, various CLI tools). Wants to know exactly which applications are touching her keyring.

**Pain point:** Any process running as her user can silently read any unlocked secret from the Secret Service. She has no way to know that a newly installed CLI tool is reading her AWS credentials, or that an Electron app is enumerating her entire keyring. GNOME Keyring and GoPass both authorize at the session level — once unlocked, everything is open.

**What she wants:**
- Per-application secret access control (not just per-user)
- Real-time notifications when secrets are accessed
- Process chain identification (see the full ancestry, not just "dbus-daemon")
- Auto-approve for trusted apps, prompt for everything else
- Ignore rules for noisy apps (e.g., Chrome's dummy secret probe)
- Audit log as a security record

**How she found the project:** Was building something similar herself, found this on GitHub.

**Technical comfort:** Very high. Contributes to security tools, reads D-Bus specs, writes systemd units.

**Usage pattern:**
- secrets-dispatcher wraps her local GoPass secret-service
- Trust rules by process exe and CWD for known-safe tools
- Desktop notifications for all unapproved access
- `ignore_chrome_dummy_secret: true` to suppress browser noise
- Periodic audit log review

---

## Common Traits Across All Personas

| Trait | Detail |
|-------|--------|
| **OS** | Linux (D-Bus and Secret Service are Linux/freedesktop-specific) |
| **Security-conscious** | Actively thinks about trust boundaries and access control |
| **Uses GPG** | For commit signing, secret encryption, or both |
| **CLI-comfortable** | Terminal is primary interface; GUI is supplementary |
| **Wants visibility** | Needs to know what's happening with their secrets and keys |
| **Automation-adjacent** | Uses tools (AI agents, CI, scripts) that act on their behalf |

## Who This Is NOT For

- **Users who don't care about per-secret access control** — if session-level unlocking is fine, the default keyring works
- **Non-Linux users** — D-Bus is Linux/freedesktop-specific
- **Users without GPG or a Secret Service keyring** — this is a proxy/gateway, not a secret store
- **Teams using cloud-only secret managers** — HashiCorp Vault, AWS Secrets Manager, etc. have their own access control
- **Non-technical users** — setup requires understanding of systemd, D-Bus, and GPG
