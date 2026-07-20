#!/usr/bin/env bash
# Run the Tier-1 GNOME e2e (fast.sh) inside an Ubuntu container — for hosts
# that don't have gnome-keyring installed (or to reproduce CI locally).
# Usage: container.sh <secrets-dispatcher-binary> [notifstub-binary]
# (build both with CGO_ENABLED=0)
set -euo pipefail

BIN=$(readlink -f "${1:?usage: container.sh <secrets-dispatcher-binary> [notifstub-binary]}")
NOTIFSTUB=${2:+$(readlink -f "$2")}
REPO=$(cd "$(dirname "$0")/../.." && pwd)

RUNTIME=$(command -v podman || command -v docker) || {
    echo "error: need podman or docker" >&2
    exit 1
}

STUB_ARGS=()
STUB_MOUNT=()
if [[ -n "$NOTIFSTUB" ]]; then
    STUB_MOUNT=(-v "$NOTIFSTUB:/usr/local/bin/notifstub:ro")
    STUB_ARGS=(/usr/local/bin/notifstub)
fi

"$RUNTIME" run --rm \
    -v "$REPO/e2e:/e2e:ro" \
    -v "$BIN:/usr/local/bin/secrets-dispatcher:ro" \
    "${STUB_MOUNT[@]}" \
    ubuntu:24.04 \
    bash -c 'export DEBIAN_FRONTEND=noninteractive &&
        apt-get update -qq &&
        apt-get install -y -qq --no-install-recommends \
            dbus dbus-daemon dbus-bin gnome-keyring libsecret-tools systemd >/dev/null &&
        /e2e/gnome/fast.sh /usr/local/bin/secrets-dispatcher "$@"' -- "${STUB_ARGS[@]}"
