#!/usr/bin/env bash
# Tier-1 GNOME e2e (fast, no VM): run secrets-dispatcher in front of a REAL
# gnome-keyring and drive the Secret Service API through the proxy.
#
# Topology (mirrors production `serve` with downstream=session_bus):
#
#   secret-tool/busctl -> front bus (dbus-run-session) -> secrets-dispatcher
#                      -> backend bus (private dbus-daemon) -> gnome-keyring
#
# The core assertion is the US-6 prompt-forwarding regression gate (issue #1):
# Unlock on a locked collection returns a prompt path, and calling
# org.freedesktop.Secret.Prompt.Dismiss on that path *through the proxy* must
# succeed and propagate Completed to the front bus. Before the PromptHandler
# existed this failed with "Object does not implement ... Prompt".
# Dismiss is used (not Prompt) because it needs no gcr prompter GUI.
#
# With a notifstub binary as $2, also runs the US-7 fast-tier gate: a pending
# approval request must produce a notification with expire_timeout=0 (never
# expire — gnome-shell expires -1 even at critical urgency), critical urgency,
# and Approve/Deny actions. The stub owns org.freedesktop.Notifications on the
# front bus and records what the dispatcher actually sends.
#
# Fully hermetic: private XDG dirs and buses; never touches the invoking
# user's keyrings or session bus.
# Usage: fast.sh <secrets-dispatcher-binary> [notifstub-binary]
set -euo pipefail

BIN=$(readlink -f "${1:?usage: fast.sh <secrets-dispatcher-binary> [notifstub-binary]}")
NOTIFSTUB=${2:+$(readlink -f "$2")}

for tool in dbus-run-session dbus-daemon dbus-monitor gnome-keyring-daemon secret-tool busctl; do
    command -v "$tool" >/dev/null || {
        echo "error: required tool not found: $tool" >&2
        echo "hint: on non-Ubuntu hosts run e2e/gnome/container.sh instead" >&2
        exit 1
    }
done

# Re-exec under a private session bus (the front bus).
if [[ "${E2E_GNOME_WRAPPED:-}" != 1 ]]; then
    export E2E_GNOME_WRAPPED=1
    exec dbus-run-session -- "$0" "$BIN" ${NOTIFSTUB:+"$NOTIFSTUB"}
fi

WORK=$(mktemp -d)
PIDS=()
cleanup() {
    local pid
    for pid in "${PIDS[@]}"; do
        kill "$pid" 2>/dev/null || true
    done
    # gnome-keyring-daemon daemonizes itself, so track it down by the unique
    # $WORK path in its environment — never touches a real user's daemon.
    for pid in $(pgrep -x gnome-keyring-daemon 2>/dev/null); do
        if grep -qzs "$WORK" "/proc/$pid/environ" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
        fi
    done
    rm -rf "$WORK"
}
trap cleanup EXIT

# Hermetic environment: gnome-keyring stores keyrings under XDG_DATA_HOME and
# its control socket under XDG_RUNTIME_DIR.
export XDG_RUNTIME_DIR="$WORK/runtime"
export XDG_DATA_HOME="$WORK/data"
export XDG_CONFIG_HOME="$WORK/config"
export XDG_CACHE_HOME="$WORK/cache"
mkdir -m 700 "$XDG_RUNTIME_DIR" "$XDG_DATA_HOME" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME"

FRONT_ADDR="$DBUS_SESSION_BUS_ADDRESS"
BACKEND_SOCK="$WORK/backend.sock"
BACKEND_ADDR="unix:path=$BACKEND_SOCK"

log() { printf '\n== %s\n' "$*"; }

# wait_for <description> <max-seconds> <command...>
wait_for() {
    local desc=$1 max=$2 i
    shift 2
    for ((i = 0; i < max * 10; i++)); do
        "$@" &>/dev/null && return 0
        sleep 0.1
    done
    echo "error: timed out waiting for $desc" >&2
    return 1
}

has_owner() {
    busctl --address="$1" call org.freedesktop.DBus /org/freedesktop/DBus \
        org.freedesktop.DBus GetNameOwner s org.freedesktop.secrets
}

log "starting private backend bus"
dbus-daemon --session --nofork --address="$BACKEND_ADDR" &
PIDS+=($!)
wait_for "backend bus socket" 10 test -S "$BACKEND_SOCK"

log "starting gnome-keyring (secrets only) with an unlocked login keyring"
# Single-shot start: --unlock daemonizes gnome-keyring AND creates+unlocks the
# login keyring in one go. A --foreground daemon with a separate --unlock call
# never registers the default alias, which sends libsecret down a
# CreateCollection fallback that hangs on a GUI prompt.
printf 'tier1-password\n' | DBUS_SESSION_BUS_ADDRESS="$BACKEND_ADDR" \
    gnome-keyring-daemon --unlock --components=secrets >/dev/null
wait_for "gnome-keyring to own org.freedesktop.secrets on the backend bus" 10 \
    has_owner "$BACKEND_ADDR"

# libsecret stores via the default alias (gnome-keyring serves no alias paths,
# so it falls back to CreateCollection: harmless only once the alias resolves).
default_alias_ready() {
    busctl --address="$BACKEND_ADDR" call org.freedesktop.secrets /org/freedesktop/secrets \
        org.freedesktop.Secret.Service ReadAlias s default | grep -q /collection/login
}
wait_for "default alias to point at the login collection" 10 default_alias_ready

log "starting secrets-dispatcher in front"
cat >"$WORK/config.yaml" <<EOF
listen: 127.0.0.1:18484
state_dir: $WORK/state
serve:
  upstream:
    type: socket
    path: $BACKEND_SOCK
  downstream:
    - type: session_bus
  notifications: false
  rules:
    - name: allow test tools
      action: approve
      process:
        exe: $(command -v secret-tool)
EOF
"$BIN" serve --config "$WORK/config.yaml" --log-level debug &>"$WORK/dispatcher.log" &
DISPATCHER_PID=$!
PIDS+=($!)
if ! wait_for "secrets-dispatcher to own org.freedesktop.secrets on the front bus" 10 \
    has_owner "$FRONT_ADDR"; then
    sed 's/^/  dispatcher: /' "$WORK/dispatcher.log" >&2
    exit 1
fi

log "secret round trip through the proxy (US-4 smoke)"
printf 'tier1-secret' | secret-tool store --label="Tier1 Test" service tier1 user demo
LOOKED_UP=$(secret-tool lookup service tier1 user demo)
if [[ "$LOOKED_UP" != "tier1-secret" ]]; then
    echo "error: lookup returned '$LOOKED_UP', want 'tier1-secret'" >&2
    exit 1
fi

# gnome-keyring exports the login collection object lazily — it appears on the
# bus only after first use (the store above), so wait for it through the proxy.
LOGIN_COLL=/org/freedesktop/secrets/collection/login
login_ready() {
    busctl --address="$FRONT_ADDR" get-property org.freedesktop.secrets "$LOGIN_COLL" \
        org.freedesktop.Secret.Collection Locked
}
wait_for "login collection to be visible through the proxy" 10 login_ready

if [[ -n "$NOTIFSTUB" ]]; then
    log "US-7 gate: a pending approval must notify with expire_timeout=0 + actions"
    # The stub must own org.freedesktop.Notifications BEFORE the dispatcher
    # connects its notifier, so restart the dispatcher: once with rules
    # removed and notifications enabled (a pending request is what notifies),
    # then back to the original config for the prompt gates below.
    "$NOTIFSTUB" >"$WORK/notifstub.log" 2>&1 &
    PIDS+=($!)
    wait_for "notifstub to own org.freedesktop.Notifications" 10 \
        grep -q READY "$WORK/notifstub.log"

    kill "$DISPATCHER_PID" 2>/dev/null || true
    wait "$DISPATCHER_PID" 2>/dev/null || true
    front_name_free() { ! has_owner "$FRONT_ADDR"; }
    wait_for "front bus name to be released" 10 front_name_free

    cat >"$WORK/config-notif.yaml" <<EOF
listen: 127.0.0.1:18484
state_dir: $WORK/state
serve:
  upstream:
    type: socket
    path: $BACKEND_SOCK
  downstream:
    - type: session_bus
  notifications: true
EOF
    "$BIN" serve --config "$WORK/config-notif.yaml" --log-level debug &>"$WORK/dispatcher-notif.log" &
    DISPATCHER_PID=$!
    PIDS+=($!)
    wait_for "dispatcher (notif config) to own the front name" 10 has_owner "$FRONT_ADDR"

    # No rules match, so this lookup blocks pending approval — exactly the
    # state whose notification the stub captures. It dies on its own timeout.
    timeout 10 secret-tool lookup service tier1 user demo >/dev/null 2>&1 &
    LOOKUP_PID=$!

    if ! wait_for "approval notification to reach notifstub" 10 \
        grep -q "^NOTIFY" "$WORK/notifstub.log"; then
        sed 's/^/  dispatcher: /' "$WORK/dispatcher-notif.log" >&2
        exit 1
    fi
    NOTIFY_LINE=$(grep -m1 "^NOTIFY" "$WORK/notifstub.log")
    echo "   $NOTIFY_LINE"
    # The US-7 regression gate: -1 lets gnome-shell expire the notification
    # ~2s after display; approvals must send 0 (never expire), critical
    # urgency, and actionable Approve/Deny buttons.
    grep -qE "expire_timeout=0( |$)" <<<"$NOTIFY_LINE" || {
        echo "error: approval notification does not send expire_timeout=0" >&2
        exit 1
    }
    grep -q "urgency=2" <<<"$NOTIFY_LINE" || {
        echo "error: approval notification is not critical urgency" >&2
        exit 1
    }
    if ! grep -q "approve,Approve" <<<"$NOTIFY_LINE" || ! grep -q "deny,Deny" <<<"$NOTIFY_LINE"; then
        echo "error: approval notification is missing Approve/Deny actions" >&2
        exit 1
    fi

    kill "$LOOKUP_PID" 2>/dev/null || true
    wait "$LOOKUP_PID" 2>/dev/null || true

    # Back to the original config for the prompt-forwarding gates.
    kill "$DISPATCHER_PID" 2>/dev/null || true
    wait "$DISPATCHER_PID" 2>/dev/null || true
    wait_for "front bus name to be released" 10 front_name_free
    "$BIN" serve --config "$WORK/config.yaml" --log-level debug &>>"$WORK/dispatcher.log" &
    DISPATCHER_PID=$!
    PIDS+=($!)
    wait_for "dispatcher to re-own the front name" 10 has_owner "$FRONT_ADDR"
fi

log "locking the login collection"
busctl --address="$FRONT_ADDR" call org.freedesktop.secrets /org/freedesktop/secrets \
    org.freedesktop.Secret.Service Lock ao 1 "$LOGIN_COLL"
LOCKED=$(busctl --address="$FRONT_ADDR" get-property org.freedesktop.secrets "$LOGIN_COLL" \
    org.freedesktop.Secret.Collection Locked)
if [[ "$LOCKED" != "b true" ]]; then
    echo "error: collection not locked after Lock (got: $LOCKED)" >&2
    exit 1
fi

log "Unlock on the locked collection must return a prompt path"
UNLOCK_OUT=$(busctl --address="$FRONT_ADDR" call org.freedesktop.secrets /org/freedesktop/secrets \
    org.freedesktop.Secret.Service Unlock ao 1 "$LOGIN_COLL")
PROMPT_PATH=$(grep -o '/org/freedesktop/secrets/prompt/[[:alnum:]_/]*' <<<"$UNLOCK_OUT" | head -1 || true)
if [[ -z "$PROMPT_PATH" ]]; then
    echo "error: Unlock returned no prompt path (got: $UNLOCK_OUT)" >&2
    exit 1
fi
echo "   prompt path: $PROMPT_PATH"

log "watching the front bus for Prompt.Completed"
dbus-monitor --address "$FRONT_ADDR" \
    "type='signal',interface='org.freedesktop.Secret.Prompt',member='Completed'" \
    >"$WORK/signals.log" &
PIDS+=($!)
sleep 1 # let the monitor's match rule attach before triggering the signal

log "US-6 regression gate: Dismiss the prompt THROUGH the proxy"
# Pre-fix this failed with "Object does not implement org.freedesktop.Secret.Prompt".
busctl --address="$FRONT_ADDR" call org.freedesktop.secrets "$PROMPT_PATH" \
    org.freedesktop.Secret.Prompt Dismiss

log "Completed signal must propagate to the front bus"
wait_for "Prompt.Completed on the front bus" 10 grep -q "member=Completed" "$WORK/signals.log"

log "dismissed prompt must leave the collection locked"
LOCKED=$(busctl --address="$FRONT_ADDR" get-property org.freedesktop.secrets "$LOGIN_COLL" \
    org.freedesktop.Secret.Collection Locked)
if [[ "$LOCKED" != "b true" ]]; then
    echo "error: collection unlocked after dismissed prompt (got: $LOCKED)" >&2
    exit 1
fi

log "PASS: prompt forwarding works against real gnome-keyring"
