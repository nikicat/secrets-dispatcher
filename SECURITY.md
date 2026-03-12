# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in secrets-dispatcher, please [open a GitHub issue](https://github.com/nikicat/secrets-dispatcher/issues/new).

Include:
- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

## Scope

The following are in scope:
- Bypassing approval prompts (accessing secrets without user consent)
- Privilege escalation through the D-Bus proxy
- Trust rule bypass (request approved that should have been denied)
- Audit log tampering or evasion
- Information disclosure through the web UI or API
- Process chain detection spoofing

## Security Model

secrets-dispatcher operates as a **same-user proxy** — it runs with the same privileges as the user's session. It does not provide isolation between privilege levels; its purpose is to add visibility and approval controls to operations that would otherwise happen silently.

See the [README](README.md) for the full architecture.

## Supported Versions

Only the latest release is supported with security updates.
