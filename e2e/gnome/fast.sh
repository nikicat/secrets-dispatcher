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
#
# Structure (mirrors the Tier-2 scenario.sh leg layout): a set of setup helpers
# bring the topology up, then gate_* functions run one acceptance gate each, and
# main() wires them in order. The gates are stateful — gate_prompt needs the
# login collection that gate_roundtrip creates, and gate_notification restarts
# the dispatcher and restores it — so main runs them as a fixed sequence.
#
# Usage: fast.sh <secrets-dispatcher-binary> [notifstub-binary]
set -euo pipefail

BIN=$(readlink -f "${1:?usage: fast.sh <secrets-dispatcher-binary> [notifstub-binary]}")
NOTIFSTUB=${2:+$(readlink -f "$2")}

check_tools() {
    local tool
    for tool in dbus-run-session dbus-daemon dbus-monitor gnome-keyring-daemon secret-tool busctl; do
        command -v "$tool" >/dev/null || {
            echo "error: required tool not found: $tool" >&2
            echo "hint: on non-Ubuntu hosts run e2e/gnome/container.sh instead" >&2
            exit 1
        }
    done
}
check_tools

# Re-exec under a private session bus (the front bus).
if [[ "${E2E_GNOME_WRAPPED:-}" != 1 ]]; then
    export E2E_GNOME_WRAPPED=1
    exec dbus-run-session -- "$0" "$BIN" ${NOTIFSTUB:+"$NOTIFSTUB"}
fi

WORK=$(mktemp -d)
PIDS=()
DISPATCHER_PID=
LOGIN_COLL=/org/freedesktop/secrets/collection/login

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

# --- predicates (poll targets for wait_for) --------------------------------

has_owner() {
    busctl --address="$1" call org.freedesktop.DBus /org/freedesktop/DBus \
        org.freedesktop.DBus GetNameOwner s org.freedesktop.secrets
}

front_name_free() { ! has_owner "$FRONT_ADDR"; }

# libsecret stores via the default alias (gnome-keyring serves no alias paths,
# so it falls back to CreateCollection: harmless only once the alias resolves).
default_alias_ready() {
    busctl --address="$BACKEND_ADDR" call org.freedesktop.secrets /org/freedesktop/secrets \
        org.freedesktop.Secret.Service ReadAlias s default | grep -q /collection/login
}

# gnome-keyring exports the login collection object lazily — it appears on the
# bus only after first use (the store in gate_roundtrip), so wait for it through
# the proxy.
login_ready() {
    busctl --address="$FRONT_ADDR" get-property org.freedesktop.secrets "$LOGIN_COLL" \
        org.freedesktop.Secret.Collection Locked
}

# --- topology setup --------------------------------------------------------

# Hermetic environment: gnome-keyring stores keyrings under XDG_DATA_HOME and
# its control socket under XDG_RUNTIME_DIR.
setup_env() {
    export XDG_RUNTIME_DIR="$WORK/runtime"
    export XDG_DATA_HOME="$WORK/data"
    export XDG_CONFIG_HOME="$WORK/config"
    export XDG_CACHE_HOME="$WORK/cache"
    mkdir -m 700 "$XDG_RUNTIME_DIR" "$XDG_DATA_HOME" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME"

    FRONT_ADDR="$DBUS_SESSION_BUS_ADDRESS"
    BACKEND_SOCK="$WORK/backend.sock"
    BACKEND_ADDR="unix:path=$BACKEND_SOCK"
}

start_backend_bus() {
    log "starting private backend bus"
    dbus-daemon --session --nofork --address="$BACKEND_ADDR" &
    PIDS+=($!)
    wait_for "backend bus socket" 10 test -S "$BACKEND_SOCK"
}

start_gnome_keyring() {
    log "starting gnome-keyring (secrets only) with an unlocked login keyring"
    # Single-shot start: --unlock daemonizes gnome-keyring AND creates+unlocks
    # the login keyring in one go. A --foreground daemon with a separate
    # --unlock call never registers the default alias, which sends libsecret
    # down a CreateCollection fallback that hangs on a GUI prompt.
    printf 'tier1-password\n' | DBUS_SESSION_BUS_ADDRESS="$BACKEND_ADDR" \
        gnome-keyring-daemon --unlock --components=secrets >/dev/null
    wait_for "gnome-keyring to own org.freedesktop.secrets on the backend bus" 10 \
        has_owner "$BACKEND_ADDR"
    wait_for "default alias to point at the login collection" 10 default_alias_ready
}

# write_config <file> <notifications> [with_rules]: emit a serve config for the
# front proxy. The rules block (approve the test tools) is included only when
# with_rules is set — the US-7 gate needs a rule-less config so a lookup blocks
# pending approval.
write_config() {
    local file=$1 notifications=$2 with_rules=${3:-}
    cat >"$file" <<EOF
listen: 127.0.0.1:18484
state_dir: $WORK/state
serve:
  upstream:
    type: socket
    path: $BACKEND_SOCK
  downstream:
    - type: session_bus
  notifications: $notifications
EOF
    if [[ -n "$with_rules" ]]; then
        cat >>"$file" <<EOF
  rules:
    - name: allow test tools
      action: approve
      process:
        exe: $(command -v secret-tool)
EOF
    fi
}

# start_dispatcher <config> <logfile>: bring the proxy up in front and wait for
# it to own org.freedesktop.secrets on the front bus, dumping the log and
# failing if it never does. Log is appended so a restart's output joins the
# original run's for post-mortem.
start_dispatcher() {
    local config=$1 logfile=$2
    "$BIN" serve --config "$config" --log-level debug &>>"$logfile" &
    DISPATCHER_PID=$!
    PIDS+=($!)
    if ! wait_for "secrets-dispatcher to own org.freedesktop.secrets on the front bus" 10 \
        has_owner "$FRONT_ADDR"; then
        sed 's/^/  dispatcher: /' "$logfile" >&2
        exit 1
    fi
}

# stop_dispatcher: kill the running proxy and wait for the front name to be
# released, so the next start_dispatcher can claim it cleanly.
stop_dispatcher() {
    kill "$DISPATCHER_PID" 2>/dev/null || true
    wait "$DISPATCHER_PID" 2>/dev/null || true
    wait_for "front bus name to be released" 10 front_name_free
}

# --- gates -----------------------------------------------------------------

gate_roundtrip() {
    log "secret round trip through the proxy (US-4 smoke)"
    printf 'tier1-secret' | secret-tool store --label="Tier1 Test" service tier1 user demo
    local looked_up
    looked_up=$(secret-tool lookup service tier1 user demo)
    if [[ "$looked_up" != "tier1-secret" ]]; then
        echo "error: lookup returned '$looked_up', want 'tier1-secret'" >&2
        exit 1
    fi
    wait_for "login collection to be visible through the proxy" 10 login_ready
}

gate_notification() {
    log "US-7 gate: a pending approval must notify with expire_timeout=0 + actions"
    # The stub must own org.freedesktop.Notifications BEFORE the dispatcher
    # connects its notifier, so restart the dispatcher: once with rules removed
    # and notifications enabled (a pending request is what notifies), then back
    # to the original config for the prompt gates below.
    "$NOTIFSTUB" >"$WORK/notifstub.log" 2>&1 &
    PIDS+=($!)
    wait_for "notifstub to own org.freedesktop.Notifications" 10 \
        grep -q READY "$WORK/notifstub.log"

    stop_dispatcher
    write_config "$WORK/config-notif.yaml" true
    start_dispatcher "$WORK/config-notif.yaml" "$WORK/dispatcher-notif.log"

    # No rules match, so this lookup blocks pending approval — exactly the state
    # whose notification the stub captures. It dies on its own timeout.
    timeout 10 secret-tool lookup service tier1 user demo >/dev/null 2>&1 &
    local lookup_pid=$!

    if ! wait_for "approval notification to reach notifstub" 10 \
        grep -q "^NOTIFY" "$WORK/notifstub.log"; then
        sed 's/^/  dispatcher: /' "$WORK/dispatcher-notif.log" >&2
        exit 1
    fi
    local notify_line
    notify_line=$(grep -m1 "^NOTIFY" "$WORK/notifstub.log")
    echo "   $notify_line"
    # The US-7 regression gate: -1 lets gnome-shell expire the notification ~2s
    # after display; approvals must send 0 (never expire), critical urgency, and
    # actionable Approve/Deny buttons.
    grep -qE "expire_timeout=0( |$)" <<<"$notify_line" || {
        echo "error: approval notification does not send expire_timeout=0" >&2
        exit 1
    }
    grep -q "urgency=2" <<<"$notify_line" || {
        echo "error: approval notification is not critical urgency" >&2
        exit 1
    }
    if ! grep -q "approve,Approve" <<<"$notify_line" || ! grep -q "deny,Deny" <<<"$notify_line"; then
        echo "error: approval notification is missing Approve/Deny actions" >&2
        exit 1
    fi

    kill "$lookup_pid" 2>/dev/null || true
    wait "$lookup_pid" 2>/dev/null || true

    # Back to the original config for the prompt-forwarding gates.
    stop_dispatcher
    start_dispatcher "$WORK/config.yaml" "$WORK/dispatcher.log"
}

gate_prompt() {
    log "locking the login collection"
    busctl --address="$FRONT_ADDR" call org.freedesktop.secrets /org/freedesktop/secrets \
        org.freedesktop.Secret.Service Lock ao 1 "$LOGIN_COLL"
    local locked
    locked=$(busctl --address="$FRONT_ADDR" get-property org.freedesktop.secrets "$LOGIN_COLL" \
        org.freedesktop.Secret.Collection Locked)
    if [[ "$locked" != "b true" ]]; then
        echo "error: collection not locked after Lock (got: $locked)" >&2
        exit 1
    fi

    log "Unlock on the locked collection must return a prompt path"
    local unlock_out prompt_path
    unlock_out=$(busctl --address="$FRONT_ADDR" call org.freedesktop.secrets /org/freedesktop/secrets \
        org.freedesktop.Secret.Service Unlock ao 1 "$LOGIN_COLL")
    prompt_path=$(grep -o '/org/freedesktop/secrets/prompt/[[:alnum:]_/]*' <<<"$unlock_out" | head -1 || true)
    if [[ -z "$prompt_path" ]]; then
        echo "error: Unlock returned no prompt path (got: $unlock_out)" >&2
        exit 1
    fi
    echo "   prompt path: $prompt_path"

    log "watching the front bus for Prompt.Completed"
    dbus-monitor --address "$FRONT_ADDR" \
        "type='signal',interface='org.freedesktop.Secret.Prompt',member='Completed'" \
        >"$WORK/signals.log" &
    PIDS+=($!)
    sleep 1 # let the monitor's match rule attach before triggering the signal

    log "US-6 regression gate: Dismiss the prompt THROUGH the proxy"
    # Pre-fix this failed with "Object does not implement org.freedesktop.Secret.Prompt".
    busctl --address="$FRONT_ADDR" call org.freedesktop.secrets "$prompt_path" \
        org.freedesktop.Secret.Prompt Dismiss

    log "Completed signal must propagate to the front bus"
    wait_for "Prompt.Completed on the front bus" 10 grep -q "member=Completed" "$WORK/signals.log"

    log "dismissed prompt must leave the collection locked"
    locked=$(busctl --address="$FRONT_ADDR" get-property org.freedesktop.secrets "$LOGIN_COLL" \
        org.freedesktop.Secret.Collection Locked)
    if [[ "$locked" != "b true" ]]; then
        echo "error: collection unlocked after dismissed prompt (got: $locked)" >&2
        exit 1
    fi
}

# --- driver ----------------------------------------------------------------

main() {
    setup_env
    start_backend_bus
    start_gnome_keyring

    log "starting secrets-dispatcher in front"
    write_config "$WORK/config.yaml" false with_rules
    start_dispatcher "$WORK/config.yaml" "$WORK/dispatcher.log"

    gate_roundtrip
    if [[ -n "$NOTIFSTUB" ]]; then
        gate_notification
    fi
    gate_prompt

    log "PASS: prompt forwarding works against real gnome-keyring"
}

main
