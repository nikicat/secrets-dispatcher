#!/usr/bin/env bash
# Tier-2 GNOME desktop VM harness: raw QEMU + Ubuntu cloud image + cloud-init.
# Chosen for true local<->CI parity (same script/image/accel both places, KVM
# available on GitHub ubuntu-latest runners) — see docs/plans/onboarding-and-e2e.md.
#
# Subcommands:
#   provision      build the cached desktop base image (one-time, ~10 min)
#   boot           start a throwaway VM (overlay) from the desktop base
#   ssh [cmd...]   run a command in the VM (or interactive shell)
#   wait-desktop   wait until the autologin GNOME session is up
#   stop           kill the running VM (overlay is kept for post-mortem)
#   destroy        stop + delete the instance work dir
#
# UBUNTU_SERIES picks the release under test (noble = 24.04 LTS, resolute =
# 26.04 LTS). Images and the instance dir are per-series, so bases for
# different series coexist in the cache; to run two VMs at once also set
# SSH_PORT.
#
# Layout:
#   cache (shared, immutable after provision): $CACHE_DIR
#     $UBUNTU_SERIES-server-cloudimg-amd64.img  pristine cloud image (downloaded)
#     desktop-base-$UBUNTU_SERIES.qcow2         provisioned GNOME desktop image
#     id_ed25519[.pub]                          SSH keypair baked into the base
#   instance (throwaway): $VM_DIR — overlay disk, seed iso, pidfile, logs
set -euo pipefail

UBUNTU_SERIES=${UBUNTU_SERIES:-noble}
CACHE_DIR=${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}
VM_DIR=${VM_DIR:-$CACHE_DIR/instance-$UBUNTU_SERIES}
SSH_PORT=${SSH_PORT:-2222}
VM_MEM=${VM_MEM:-4G}
VM_CPUS=${VM_CPUS:-4}
IMG_URL=https://cloud-images.ubuntu.com/$UBUNTU_SERIES/current/$UBUNTU_SERIES-server-cloudimg-amd64.img
PRISTINE=$CACHE_DIR/$UBUNTU_SERIES-server-cloudimg-amd64.img
BASE=$CACHE_DIR/desktop-base-$UBUNTU_SERIES.qcow2
SSH_KEY=$CACHE_DIR/id_ed25519
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)

die() {
    echo "error: $*" >&2
    exit 1
}

ssh_vm() {
    ssh -p "$SSH_PORT" -i "$SSH_KEY" -o IdentitiesOnly=yes \
        -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR -o ConnectTimeout=5 \
        e2e@127.0.0.1 "$@"
}

# wait_for <description> <max-seconds> <command...>
wait_for() {
    local desc=$1 max=$2 i
    shift 2
    for ((i = 0; i < max; i++)); do
        "$@" &>/dev/null && return 0
        sleep 1
    done
    echo "error: timed out waiting for $desc" >&2
    return 1
}

make_seed() {
    local seed=$1 pubkey
    pubkey=$(cat "$SSH_KEY.pub")
    sed "s|@SSH_PUBKEY@|$pubkey|" "$SCRIPT_DIR/user-data" >"$VM_DIR/user-data"
    printf 'instance-id: secrets-dispatcher-e2e\nlocal-hostname: gnome-e2e\n' >"$VM_DIR/meta-data"
    if command -v cloud-localds >/dev/null; then
        cloud-localds "$seed" "$VM_DIR/user-data" "$VM_DIR/meta-data"
    else
        genisoimage -output "$seed" -volid cidata -joliet -rock -quiet \
            "$VM_DIR/user-data" "$VM_DIR/meta-data"
    fi
}

start_qemu() {
    local disk=$1 pidfile=$2 log=$3
    shift 3
    # virtio-vga gives gdm/gnome-shell a virtual monitor with no host display;
    # daemonize keeps qemu alive across the calling shell. The QMP socket
    # plus the virtio input devices are host-side input injection (demo.sh
    # types keys and clicks notification buttons over QMP); mutter only
    # consumes virtio-input, not the emulated PS/2 devices, so both the
    # tablet (absolute-coordinate pointer) and keyboard must be explicit.
    # All three are inert for the test path.
    qemu-system-x86_64 \
        -enable-kvm -cpu host -m "$VM_MEM" -smp "$VM_CPUS" \
        -drive "file=$disk,format=qcow2,if=virtio" \
        -device virtio-vga \
        -device virtio-tablet-pci \
        -device virtio-keyboard-pci \
        -display none \
        -qmp "unix:$VM_DIR/qmp.sock,server,nowait" \
        -netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22" \
        -device virtio-net-pci,netdev=net0 \
        -serial "file:$log" \
        -pidfile "$pidfile" \
        -daemonize \
        "$@"
}

cmd_provision() {
    mkdir -p "$CACHE_DIR" "$VM_DIR"
    if [[ -f "$BASE" ]]; then
        echo "desktop base already provisioned: $BASE (delete it to re-provision)"
        return 0
    fi

    if [[ ! -f "$PRISTINE" ]]; then
        echo "downloading Ubuntu cloud image..."
        curl -SL -C - -o "$PRISTINE.part" "$IMG_URL"
        mv "$PRISTINE.part" "$PRISTINE"
    fi

    [[ -f "$SSH_KEY" ]] || ssh-keygen -t ed25519 -N '' -q -f "$SSH_KEY"

    echo "provisioning desktop base (installs the GNOME stack, then powers off)..."
    qemu-img create -q -f qcow2 -b "$PRISTINE" -F qcow2 "$BASE" 20G
    make_seed "$VM_DIR/seed-provision.iso"
    start_qemu "$BASE" "$VM_DIR/provision.pid" "$VM_DIR/provision-serial.log" \
        -drive "file=$VM_DIR/seed-provision.iso,format=raw,if=virtio,readonly=on"

    # cloud-init powers the VM off when done (power_state in user-data).
    local pid
    pid=$(cat "$VM_DIR/provision.pid")
    echo "waiting for provisioning to finish (qemu pid $pid, up to 20 min)..."
    if ! wait_for "provisioning power-off" 1200 test '!' -d "/proc/$pid"; then
        kill "$pid" 2>/dev/null || true
        die "provisioning timed out; see $VM_DIR/provision-serial.log"
    fi
    echo "desktop base ready: $BASE"
}

cmd_boot() {
    [[ -f "$BASE" ]] || die "no desktop base — run '$0 provision' first"
    # The keypair is baked into the base at provision time; a base without
    # its keypair (e.g. an incomplete CI cache restore) is unreachable.
    [[ -f "$SSH_KEY" ]] || die "SSH keypair missing: $SSH_KEY (must accompany desktop-base.qcow2 — delete the base to re-provision)"
    mkdir -p "$VM_DIR"
    [[ -f "$VM_DIR/vm.pid" ]] && kill -0 "$(cat "$VM_DIR/vm.pid")" 2>/dev/null &&
        die "VM already running (pid $(cat "$VM_DIR/vm.pid"))"

    qemu-img create -q -f qcow2 -b "$BASE" -F qcow2 "$VM_DIR/disk.qcow2"
    # Boot with a NoCloud seed carrying the same instance-id: cloud-init
    # treats it as the same instance and skips re-provisioning.
    make_seed "$VM_DIR/seed.iso"
    start_qemu "$VM_DIR/disk.qcow2" "$VM_DIR/vm.pid" "$VM_DIR/serial.log" \
        -drive "file=$VM_DIR/seed.iso,format=raw,if=virtio,readonly=on"

    wait_for "SSH to come up" 180 ssh_vm true
    echo "VM up (ssh -p $SSH_PORT e2e@127.0.0.1)"
}

cmd_wait_desktop() {
    # The autologin GNOME session is the whole point of Tier 2 — wait for
    # gnome-shell to be running as the e2e user before driving tests.
    wait_for "autologin gnome-shell session" 180 ssh_vm pgrep -u e2e -x gnome-shell
    echo "GNOME session is up"
}

cmd_stop() {
    if [[ -f "$VM_DIR/vm.pid" ]]; then
        kill "$(cat "$VM_DIR/vm.pid")" 2>/dev/null || true
        rm -f "$VM_DIR/vm.pid"
    fi
    # Belt and braces: a qemu that outlived its pidfile would still hold
    # SSH_PORT and break the next boot. Match on this instance's overlay
    # disk path (unique per instance; the provision VM runs off the base
    # image, so it never matches). Then wait for the port to actually free —
    # kill is async and boot may follow immediately.
    pkill -f "$VM_DIR/disk.qcow2" 2>/dev/null || true
    local i
    for ((i = 0; i < 20; i++)); do
        pgrep -f "$VM_DIR/disk.qcow2" >/dev/null || return 0
        sleep 0.5
    done
    pkill -9 -f "$VM_DIR/disk.qcow2" 2>/dev/null || true
    sleep 1
}

cmd_destroy() {
    cmd_stop
    rm -rf "$VM_DIR"
}

case "${1:-}" in
provision) cmd_provision ;;
boot) cmd_boot ;;
ssh)
    shift
    ssh_vm "$@"
    ;;
wait-desktop) cmd_wait_desktop ;;
stop) cmd_stop ;;
destroy) cmd_destroy ;;
*)
    echo "usage: $0 provision|boot|ssh [cmd...]|wait-desktop|stop|destroy" >&2
    exit 2
    ;;
esac
