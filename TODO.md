# TODO

- [x] Implement desktop notifications for incoming requests
- [x] Implement browser notifications for incoming requests
- [ ] Implement auto accept/reject rule loading from YAML file
- [ ] Implement updating rules using additional actions on requests, including past requests (history)
- [ ] Implement intercepting secret requests from local machine (wrapping of local secret service)
- [ ] Config file support
- [x] Colored logs
- [x] Fix: no debug logs
- [x] Click on timestamp in webui switches display mode between relative and absolute
- [x] Implement CLI for approving/rejecting
- [ ] Refactor
- [ ] Proxy webserver mode for development - load webapp from vite webserver instead of embedded html

## Future Ideas

- [ ] systemd-creds integration: use TPM2-encrypted credential to auto-unlock companion's GPG store at session start (alternative to manual VT passphrase entry). `LoadCredentialEncrypted=` delivers passphrase, `ExecStartPre=` calls `gpg-preset-passphrase`. Opt-in mode alongside manual unlock (VT-08). Weaker security (auto-decrypt when TPM2 + machine running) but enables unattended reboot.
- [ ] SSH agent forwarding to companion: move SSH private keys to companion user's vault, proxy desktop user's SSH requests via Unix socket. Same privilege separation pattern as GPG. (Deferred from v2.0 discuss-phase)
