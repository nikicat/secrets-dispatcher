---
status: todo
tags: [approval, remote]
---
# Trust rules match a remote client

## Goal
A trust rule can match the client a request came from: a remote's name for `remote:<name>` requests, and a sockets-directory client's name (the socket file's name) for a bus served from `serve.downstream` `sockets`. A `process.exe` rule can never match those requests, because the dispatcher sees no local process for them. Done when a rule like this approves gh's reads from one dev container and nothing else:

```yaml
- name: symbiosis-gh
  action: approve
  request_types: [get_secret]
  client: symbiosis
  secret:
    collection: default
    attributes: {service: "gh:github.com"}
```

## Notes
- First user: zxc-fleet's dev environments (its ADR 0022, task Tcjmu8), where gh in each container reads the laptop's gh token through a session bus forwarded into the sockets directory.
- `T1j0d0` hides the auto-approve offer on remote requests because the rule it makes cannot match; with a client field it could make a client rule instead.
- A client name is chosen by whoever forwards the socket, so it identifies the tunnel, not the process (ADR 0003).
