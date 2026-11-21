#!/usr/bin/env bash
# Tier-2 GNOME desktop VM harness: raw QEMU + Ubuntu cloud image + cloud-init.
# Chosen for local<->CI parity: the same script, series and KVM acceleration
# in both places (ADR 0010).
#
# Subcommands:
#   provision      build the cached desktop base image (one-time, ~10 min)
#   boot           start a throwaway VM (overlay) from the desktop base
#   ssh [cmd...]   run a command in the VM (or interactive shell)
#   scp <file...> <path>  copy files into the VM, path relative to the e2e home
#   qmp-sock       print the QMP socket path (host-side input injection)
#   screen-size    print the guest screen's width and height in pixels
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
#     $UBUNTU_SERIES-server-cloudimg-$ARCH.img  pristine cloud image (downloaded)
#     desktop-base-$UBUNTU_SERIES-$ARCH.qcow2   provisioned GNOME desktop image
#     id_ed25519[.pub]                          SSH keypair baked into the base
#   instance (throwaway): $VM_DIR — overlay disk, seed iso, pidfile, logs
set -euo pipefail

die() {
    echo "error: $*" >&2
    exit 1
}

arm64_firmware() {
    local fw
    for fw in /usr/share/qemu-efi-aarch64/QEMU_EFI.fd /usr/share/edk2/aarch64/QEMU_EFI.fd; do
        [[ -f $fw ]] && echo "$fw" && return
    done
    die "no arm64 UEFI firmware: install qemu-efi-aarch64 (Debian/Ubuntu) or edk2-aarch64 (Fedora/Arch)"
}

# The guest runs the host's architecture (ADR 0010).
NIC=virtio-net-pci,netdev=net0
case "$(uname -m)" in
x86_64)
    ARCH=amd64 GPU=virtio-vga GPU_GL=virtio-vga-gl
    qemu() { qemu-system-x86_64 "$@"; }
    ;;
aarch64)
    # The arm64 virt machine has no VGA, so it gets the plain virtio GPU. It
    # boots UEFI firmware, which drives virtio-net itself, so the NIC loads no
    # option ROM.
    ARCH=arm64 GPU=virtio-gpu-pci GPU_GL=virtio-gpu-gl-pci NIC+=,romfile=
    qemu() {
        local fw
        fw=$(arm64_firmware) || exit
        qemu-system-aarch64 -machine virt -bios "$fw" "$@"
    }
    ;;
*) die "no VM support for $(uname -m)" ;;
esac

UBUNTU_SERIES=${UBUNTU_SERIES:-noble}
CACHE_DIR=${CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/secrets-dispatcher/e2e}
VM_DIR=${VM_DIR:-$CACHE_DIR/instance-$UBUNTU_SERIES}
SSH_PORT=${SSH_PORT:-2222}
VM_MEM=${VM_MEM:-4G}
VM_CPUS=${VM_CPUS:-4}
# Unset, the guest renders in software (llvmpipe) and mutter disables its
# animations. VM_GL=1 gives the guest the host GPU's GL through virgl, which the
# demo's window glide needs. Local only: CI runners have no /dev/dri.
VM_GL=${VM_GL:-}
# The host DRM render node that VM_GL renders on.
VM_GL_RENDERNODE=${VM_GL_RENDERNODE:-/dev/dri/renderD128}
# The virtio GPU gives gdm/gnome-shell a virtual monitor with no host display.
# Under VM_GL, its GL variant renders through virgl on an EGL-headless context,
# and VNC (VNC_DISP) still reads back the composited framebuffer.
if [[ -n "$VM_GL" ]]; then
    GPU_DEVICE=$GPU_GL QEMU_DISPLAY=egl-headless,rendernode=$VM_GL_RENDERNODE
else
    GPU_DEVICE=$GPU QEMU_DISPLAY=none
fi
# EDID pins the monitor to this size instead of the connector's default mode,
# so host-side pointer input over QMP_SOCK can address guest pixels.
SCREEN_W=1280 SCREEN_H=800
GPU_DEVICE+=,edid=on,xres=$SCREEN_W,yres=$SCREEN_H
# When VNC_DISP is a display number N, qemu exposes the guest framebuffer over
# VNC on 127.0.0.1:590N for host-side recording, because the in-guest GNOME
# screencast cannot span a session restart. Two VMs at once need different N.
VNC_DISP=${VNC_DISP:-}
IMG_URL=https://cloud-images.ubuntu.com/$UBUNTU_SERIES/current/$UBUNTU_SERIES-server-cloudimg-$ARCH.img
PRISTINE=$CACHE_DIR/$UBUNTU_SERIES-server-cloudimg-$ARCH.img
BASE=$CACHE_DIR/desktop-base-$UBUNTU_SERIES-$ARCH.qcow2
SSH_KEY=$CACHE_DIR/id_ed25519
SSH_OPTS=(-i "$SSH_KEY" -o IdentitiesOnly=yes
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null
    -o LogLevel=ERROR)
QMP_SOCK=$VM_DIR/qmp.sock
# mutter reads only virtio input devices, not emulated PS/2, so the tablet (an
# absolute pointer) and keyboard are explicit.
INPUT_DEVICES=(-device virtio-tablet-pci -device virtio-keyboard-pci)
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)

ssh_vm() {
    ssh -p "$SSH_PORT" "${SSH_OPTS[@]}" -o ConnectTimeout=5 e2e@127.0.0.1 "$@"
}

# scp_vm <file...> <path>
scp_vm() {
    (($# >= 2)) || die "usage: $0 scp <file...> <path>"
    scp -P "$SSH_PORT" "${SSH_OPTS[@]}" "${@:1:$#-1}" "e2e@127.0.0.1:${!#}"
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

# overlay <backing> <path> [size]
# Creates a qcow2 image at path whose writes leave the backing image untouched.
overlay() {
    qemu-img create -q -f qcow2 -b "$1" -F qcow2 "$2" ${3:+"$3"}
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

# instance_file <provision|vm> <pid|serial.log|seed.iso|qcow2>
# Returns the path of one file of the provisioning VM or the test VM.
instance_file() {
    echo "$VM_DIR/$1.$2"
}

# start_qemu <provision|vm> <disk>
# Boots disk with a fresh cloud-init seed as a daemon that outlives this script.
start_qemu() {
    local name=$1 disk=$2 seed
    seed=$(instance_file "$name" seed.iso)
    make_seed "$seed"
    local vnc=()
    [[ -n "$VNC_DISP" ]] && vnc=(-vnc "127.0.0.1:$VNC_DISP")
    qemu \
        -enable-kvm -cpu host -m "$VM_MEM" -smp "$VM_CPUS" \
        -drive "file=$disk,format=qcow2,if=virtio" \
        -device "$GPU_DEVICE" \
        "${INPUT_DEVICES[@]}" \
        -display "$QEMU_DISPLAY" \
        "${vnc[@]}" \
        -qmp "unix:$QMP_SOCK,server,nowait" \
        -netdev "user,id=net0,hostfwd=tcp:127.0.0.1:$SSH_PORT-:22" \
        -device "$NIC" \
        -serial "file:$(instance_file "$name" serial.log)" \
        -pidfile "$(instance_file "$name" pid)" \
        -daemonize \
        -drive "file=$seed,format=raw,if=virtio,readonly=on"
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
    # The .part image becomes the base only once the guest powers off, which it
    # does only after a successful provision (power_state in user-data). A
    # failed provision runs into the timeout below.
    rm -f "$BASE.part"
    overlay "$PRISTINE" "$BASE.part" 20G
    start_qemu provision "$BASE.part"

    local pid
    pid=$(cat "$(instance_file provision pid)")
    echo "waiting for provisioning to finish (qemu pid $pid, up to 20 min)..."
    if ! wait_for "provisioning power-off" 1200 test '!' -d "/proc/$pid"; then
        kill "$pid" 2>/dev/null || true
        die "provisioning timed out; see $(instance_file provision serial.log)"
    fi
    mv "$BASE.part" "$BASE"
    echo "desktop base ready: $BASE"
}

cmd_boot() {
    [[ -f "$BASE" ]] || die "no desktop base — run '$0 provision' first"
    # The keypair is baked into the base at provision time; a base without
    # its keypair (e.g. an incomplete CI cache restore) is unreachable.
    [[ -f "$SSH_KEY" ]] || die "SSH keypair missing: $SSH_KEY (must accompany $BASE — delete the base to re-provision)"
    mkdir -p "$VM_DIR"
    local pidfile
    pidfile=$(instance_file vm pid)
    [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null &&
        die "VM already running (pid $(cat "$pidfile"))"

    local disk
    disk=$(instance_file vm qcow2)
    overlay "$BASE" "$disk"
    # Boot with a NoCloud seed carrying the same instance-id: cloud-init
    # treats it as the same instance and skips re-provisioning.
    start_qemu vm "$disk"

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
    local pidfile
    pidfile=$(instance_file vm pid)
    if [[ -f "$pidfile" ]]; then
        kill "$(cat "$pidfile")" 2>/dev/null || true
        rm -f "$pidfile"
    fi
    # A qemu that outlived its pidfile still holds SSH_PORT and breaks the next
    # boot, so kill whatever runs off the test VM's disk (no other VM does).
    # kill is async, so wait for it to exit.
    local disk
    disk=$(instance_file vm qcow2)
    pkill -f "$disk" 2>/dev/null || true
    local i
    for ((i = 0; i < 20; i++)); do
        pgrep -f "$disk" >/dev/null || return 0
        sleep 0.5
    done
    pkill -9 -f "$disk" 2>/dev/null || true
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
scp)
    shift
    scp_vm "$@"
    ;;
qmp-sock) echo "$QMP_SOCK" ;;
screen-size) echo "$SCREEN_W $SCREEN_H" ;;
wait-desktop) cmd_wait_desktop ;;
stop) cmd_stop ;;
destroy) cmd_destroy ;;
*)
    echo "usage: $0 provision|boot|ssh [cmd...]|scp <file...> <path>|qmp-sock|screen-size|wait-desktop|stop|destroy" >&2
    exit 2
    ;;
esac
