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
- [ ] Notification burst eviction on GNOME: gnome-shell destroys an app's oldest notification (reason=expired) beyond MAX_NOTIFICATIONS_PER_SOURCE=3 — coalesce or cap concurrent approval notifications so a burst can't evict a pending approval (see docs/plans/onboarding-and-e2e.md US-7 model)
