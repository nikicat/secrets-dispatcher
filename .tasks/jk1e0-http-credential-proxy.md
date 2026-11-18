---
status: todo
tags: [proxy, adr-0014]
---
# PoC: an HTTP proxy that attaches GitHub and Slack tokens after approval

## Goal
The proxy of ADR 0014, for two services. Done means a remote's socket serves the proxy, gh and git through it get a placeholder `GH_TOKEN` and slackcli a placeholder Slack token, and:
- GitHub GETs and `git-upload-pack` pass by rule; REST writes, GraphQL mutations and `git-receive-pack` wait for approval; endpoints that mint tokens are denied;
- Slack's `conversations.history` passes by rule and `chat.postMessage` waits for approval;
- the approval shows the decoded request: GraphQL operation and variables, the refs a push updates, the form or JSON body;
- every host without a pinned credential gets a plain `CONNECT` tunnel.

## Notes
- elazarl/goproxy for the TLS interception and per-host certificates.
- New package `internal/httpproxy`; a `http_request` request type next to `gpg_sign`; an `HTTP` matcher on `TrustRule` next to `SecretMatcher`; a renderer in the notification, web UI and CLI.
- The CA key lives in the backend Secret Service.
